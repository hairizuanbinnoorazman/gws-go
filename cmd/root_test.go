package cmd

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestRootHasRequestedServices(t *testing.T) {
	root := newRootCommand(dependencies{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	for _, name := range []string{"auth", "docs", "calendar", "slides", "gmail", "drive"} {
		if root.Commands()[0] == nil {
			t.Fatal("unexpected empty command list")
		}
		if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
			t.Fatalf("missing command %q: command=%v err=%v", name, command, err)
		}
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
