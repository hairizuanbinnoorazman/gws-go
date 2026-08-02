package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestRootHasRequestedServices(t *testing.T) {
	root := newRootCommand(dependencies{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	for _, name := range []string{"auth", "docs", "calendar", "slides", "gmail", "drive", "sheets", "photos", "maps", "schema"} {
		if root.Commands()[0] == nil {
			t.Fatal("unexpected empty command list")
		}
		if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
			t.Fatalf("missing command %q: command=%v err=%v", name, command, err)
		}
	}
}

func TestMethodHelpIncludesSchemaMetadataAndExample(t *testing.T) {
	doc := &discovery.Document{
		Name: "calendar",
		Schemas: map[string]*discovery.Schema{
			"Event": {
				Type:     "object",
				Required: []string{"summary"},
				Properties: map[string]*discovery.Schema{
					"summary": {Type: "string"},
				},
			},
		},
	}
	method := &discovery.Method{
		Description: "Creates an event.",
		Parameters: map[string]*discovery.Parameter{
			"calendarId":  {Type: "string", Location: "path", Required: true},
			"sendUpdates": {Type: "string", Enum: []string{"all", "none"}},
		},
		Request: &discovery.SchemaRef{Ref: "Event"},
	}
	command := buildMethodCommandAtPath(doc, "insert", method, &bytes.Buffer{}, []string{"events"})
	if !strings.Contains(command.Long, "calendarId (string, required, path)") ||
		!strings.Contains(command.Long, "values: all, none") ||
		!strings.Contains(command.Example, `"summary":"string"`) {
		t.Fatalf("long=%q example=%q", command.Long, command.Example)
	}
}

func TestFindNestedResource(t *testing.T) {
	resources := map[string]*discovery.Resource{
		"users": {Resources: map[string]*discovery.Resource{"messages": {}}},
	}
	for _, path := range []string{"users.messages", "users/messages"} {
		resource, err := findResource(resources, path)
		if err != nil || resource == nil {
			t.Fatalf("path=%q resource=%v err=%v", path, resource, err)
		}
	}
}

func TestRunRendersMachineReadableInputErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := Run([]string{"--error-format=json", "unknown-command"}, &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exit code = %d", exitCode)
	}
	if !strings.Contains(stderr.String(), `"code":"invalid_argument"`) || !strings.Contains(stderr.String(), `"exit_code":2`) {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestExtractErrorFormatAnywhere(t *testing.T) {
	args, format, err := extractErrorFormat([]string{"calendar", "events", "list", "--error-format", "json"})
	if err != nil || format != "json" || strings.Join(args, " ") != "calendar events list" {
		t.Fatalf("args=%q format=%q err=%v", args, format, err)
	}
}

func TestBuildServiceTreeIncludesNestedMethods(t *testing.T) {
	doc := &discovery.Document{Name: "calendar", Resources: map[string]*discovery.Resource{
		"calendars": {Resources: map[string]*discovery.Resource{
			"events": {Methods: map[string]*discovery.Method{"list": {HTTPMethod: "GET", Path: "events"}}},
		}},
	}}
	command := buildServiceTree(doc, &bytes.Buffer{}, &bytes.Buffer{})
	events, _, err := command.Find([]string{"calendars", "events", "list"})
	if err != nil || events.Name() != "list" {
		t.Fatalf("method=%v err=%v", events, err)
	}
}

func TestUploadFlagsOnlyAppearForMultipartMethods(t *testing.T) {
	doc := &discovery.Document{Name: "drive", Resources: map[string]*discovery.Resource{
		"files": {Methods: map[string]*discovery.Method{
			"create": {
				HTTPMethod:          http.MethodPost,
				Path:                "files",
				SupportsMediaUpload: true,
				MediaUpload: &discovery.MediaUpload{Protocols: discovery.MediaUploadProtocols{
					Simple: &discovery.MediaUploadProtocol{Multipart: true, Path: "upload/drive/v3/files"},
				}},
			},
			"list": {HTTPMethod: http.MethodGet, Path: "files"},
		}},
	}}
	command := buildServiceTree(doc, &bytes.Buffer{}, &bytes.Buffer{})
	create, _, err := command.Find([]string{"files", "create"})
	if err != nil || create.Flags().Lookup("upload") == nil || create.Flags().Lookup("upload-content-type") == nil {
		t.Fatalf("multipart method is missing upload flags: command=%v err=%v", create, err)
	}
	list, _, err := command.Find([]string{"files", "list"})
	if err != nil || list.Flags().Lookup("upload") != nil {
		t.Fatalf("non-upload method unexpectedly has upload flag: command=%v err=%v", list, err)
	}
}

func TestDiscoveredParametersHaveNativeFlags(t *testing.T) {
	doc := &discovery.Document{
		Name:    "drive",
		BaseURL: "https://drive.example/",
		Parameters: map[string]*discovery.Parameter{
			"fields": {Type: "string"},
		},
	}
	method := &discovery.Method{
		HTTPMethod: http.MethodGet,
		Path:       "files/{fileId}",
		Parameters: map[string]*discovery.Parameter{
			"fileId":      {Type: "string", Location: "path", Required: true},
			"maxResults":  {Type: "integer"},
			"prettyPrint": {Type: "boolean"},
			"labelIds":    {Type: "string", Repeated: true},
		},
	}
	var output bytes.Buffer
	command := buildMethodCommandAtPath(doc, "get", method, &output, []string{"files"})
	command.SetArgs([]string{
		"--params", `{"maxResults":1}`,
		"--file-id", "file-1",
		"--max-results", "25",
		"--pretty-print",
		"--label-ids", "one",
		"--label-ids", "two",
		"--api-fields", "id,name",
		"--dry-run",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	result := output.String()
	for _, expected := range []string{
		"files/file-1", "maxResults=25", "prettyPrint=true",
		"labelIds=one", "labelIds=two", "fields=id%2Cname",
	} {
		if !strings.Contains(result, expected) {
			t.Fatalf("missing %q in %s", expected, result)
		}
	}
}
