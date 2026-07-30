package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestWorkspaceHelpersAreRegistered(t *testing.T) {
	doc := workspaceHelpersTestDocument()
	tests := map[string][]string{
		"calendar": {"agenda", "create-event"},
		"docs":     {"write"},
		"drive":    {"upload", "download", "share"},
		"gmail":    {"read", "search", "export"},
	}
	for serviceName, helpers := range tests {
		command := buildServiceTree(doc, &bytes.Buffer{}, &bytes.Buffer{})
		addServiceHelpers(serviceName, command, doc, dependencies{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
		for _, helper := range helpers {
			found, _, err := command.Find([]string{helper})
			if err != nil || found.Name() != helper {
				t.Fatalf("service=%s helper=%s command=%v err=%v", serviceName, helper, found, err)
			}
		}
	}
}

func TestCalendarHelpersDryRun(t *testing.T) {
	doc := workspaceHelpersTestDocument()
	var agenda bytes.Buffer
	agendaCommand := newCalendarAgendaCommand(doc, &agenda)
	agendaCommand.SetArgs([]string{
		"--from", "2026-07-01T00:00:00Z",
		"--to", "2026-07-08T00:00:00Z",
		"--dry-run",
	})
	if err := agendaCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(agenda.String(), "orderBy=startTime") || !strings.Contains(agenda.String(), "singleEvents=true") {
		t.Fatalf("agenda = %q", agenda.String())
	}

	var create bytes.Buffer
	createCommand := newCalendarCreateEventCommand(doc, &create)
	createCommand.SetArgs([]string{
		"--summary", "Planning",
		"--start", "2026-07-01",
		"--end", "2026-07-02",
		"--attendees", "ada@example.com,lin@example.com",
		"--dry-run",
	})
	if err := createCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(create.String(), `"summary": "Planning"`) || !strings.Contains(create.String(), `"ada@example.com"`) {
		t.Fatalf("create = %q", create.String())
	}
}

func TestDocsWriteDryRunFromStdin(t *testing.T) {
	var output bytes.Buffer
	command := newDocsWriteCommand(workspaceHelpersTestDocument(), &output)
	command.SetIn(strings.NewReader("Hello document"))
	command.SetArgs([]string{"--document", "doc-1", "--text", "-", "--index", "3", "--dry-run"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"index": 3`) || !strings.Contains(output.String(), `"Hello document"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestDriveHelpersDryRun(t *testing.T) {
	doc := workspaceHelpersTestDocument()
	localFile := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(localFile, []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	var upload bytes.Buffer
	uploadCommand := newDriveUploadCommand(doc, &upload)
	uploadCommand.SetArgs([]string{"--file", localFile, "--folder", "folder-1", "--dry-run"})
	if err := uploadCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(upload.String(), `"folder-1"`) || !strings.Contains(upload.String(), `"bytes": 6`) {
		t.Fatalf("upload = %q", upload.String())
	}

	var download bytes.Buffer
	downloadCommand := newDriveDownloadCommand(doc, &download)
	downloadCommand.SetArgs([]string{"--file", "file-1", "--output", filepath.Join(t.TempDir(), "saved.txt"), "--dry-run"})
	if err := downloadCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(download.String(), "alt=media") {
		t.Fatalf("download = %q", download.String())
	}

	var share bytes.Buffer
	shareCommand := newDriveShareCommand(doc, &share)
	shareCommand.SetArgs([]string{"--file", "file-1", "--email", "ada@example.com", "--role", "writer", "--dry-run"})
	if err := shareCommand.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(share.String(), `"emailAddress": "ada@example.com"`) {
		t.Fatalf("share = %q", share.String())
	}
}

func TestGmailHelpersDryRun(t *testing.T) {
	doc := workspaceHelpersTestDocument()
	tests := []struct {
		name    string
		command func(*bytes.Buffer) interface {
			SetArgs([]string)
			Execute() error
		}
		args  []string
		match string
	}{
		{name: "read", command: func(out *bytes.Buffer) interface {
			SetArgs([]string)
			Execute() error
		} {
			return newGmailReadCommand(doc, out)
		}, args: []string{"--id", "msg-1", "--dry-run"}, match: "format=full"},
		{name: "search", command: func(out *bytes.Buffer) interface {
			SetArgs([]string)
			Execute() error
		} {
			return newGmailSearchCommand(doc, out)
		}, args: []string{"--query", "from:ada", "--dry-run"}, match: "from%3Aada"},
		{name: "export", command: func(out *bytes.Buffer) interface {
			SetArgs([]string)
			Execute() error
		} {
			return newGmailExportCommand(doc, out)
		}, args: []string{"--id", "msg-1", "--output", filepath.Join(t.TempDir(), "message.eml"), "--dry-run"}, match: "format=raw"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			command := test.command(&output)
			command.SetArgs(test.args)
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.match) {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func workspaceHelpersTestDocument() *discovery.Document {
	return &discovery.Document{
		Name:    "workspace",
		BaseURL: "https://workspace.example/",
		Parameters: map[string]*discovery.Parameter{
			"alt": {},
		},
		Resources: map[string]*discovery.Resource{
			"events": {Methods: map[string]*discovery.Method{
				"list": {
					HTTPMethod: "GET", Path: "calendars/{calendarId}/events",
					Parameters: parameters("calendarId", "timeMin", "timeMax", "singleEvents", "orderBy", "maxResults"),
				},
				"insert": {
					HTTPMethod: "POST", Path: "calendars/{calendarId}/events",
					Parameters: parameters("calendarId", "sendUpdates"),
				},
			}},
			"documents": {Methods: map[string]*discovery.Method{
				"batchUpdate": {
					HTTPMethod: "POST", Path: "documents/{documentId}:batchUpdate",
					Parameters: parameters("documentId"),
				},
			}},
			"files": {Methods: map[string]*discovery.Method{
				"create": {
					HTTPMethod: "POST", Path: "files", SupportsMediaUpload: true,
					MediaUpload: &discovery.MediaUpload{Protocols: discovery.MediaUploadProtocols{
						Simple: &discovery.MediaUploadProtocol{Multipart: true, Path: "upload/drive/v3/files"},
					}},
				},
				"get": {
					HTTPMethod: "GET", Path: "files/{fileId}",
					Parameters: parameters("fileId"),
				},
			}},
			"permissions": {Methods: map[string]*discovery.Method{
				"create": {
					HTTPMethod: "POST", Path: "files/{fileId}/permissions",
					Parameters: parameters("fileId", "sendNotificationEmail"),
				},
			}},
			"users": {Resources: map[string]*discovery.Resource{
				"messages": {Methods: map[string]*discovery.Method{
					"get": {
						HTTPMethod: "GET", Path: "users/{userId}/messages/{id}",
						Parameters: parameters("userId", "id", "format"),
					},
					"list": {
						HTTPMethod: "GET", Path: "users/{userId}/messages",
						Parameters: parameters("userId", "q", "maxResults", "includeSpamTrash"),
					},
				}},
			}},
		},
	}
}

func parameters(names ...string) map[string]*discovery.Parameter {
	result := make(map[string]*discovery.Parameter, len(names))
	for _, name := range names {
		parameter := &discovery.Parameter{}
		switch name {
		case "calendarId", "documentId", "fileId", "userId", "id":
			parameter.Location = "path"
			parameter.Required = true
		}
		result[name] = parameter
	}
	return result
}
