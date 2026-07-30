package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestSheetsHelpersAreRegistered(t *testing.T) {
	doc := sheetsTestDocument()
	serviceCommand := buildServiceTree(doc, &bytes.Buffer{}, &bytes.Buffer{})
	addServiceHelpers("sheets", serviceCommand, doc, dependencies{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	for _, name := range []string{"read", "append"} {
		command, _, err := serviceCommand.Find([]string{name})
		if err != nil || command.Name() != name {
			t.Fatalf("helper %q command=%v err=%v", name, command, err)
		}
	}
}

func TestSheetsReadDryRun(t *testing.T) {
	var output bytes.Buffer
	command := newSheetsReadCommand(sheetsTestDocument(), &output)
	command.SetArgs([]string{
		"--spreadsheet", "sheet-1",
		"--range", "Sheet1!A1:B2",
		"--major-dimension", "ROWS",
		"--dry-run",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, `"dry_run": true`) ||
		!strings.Contains(got, `sheet-1`) ||
		!strings.Contains(got, `majorDimension=ROWS`) {
		t.Fatalf("output = %q", got)
	}
}

func TestSheetsAppendDryRun(t *testing.T) {
	var output bytes.Buffer
	command := newSheetsAppendCommand(sheetsTestDocument(), &output)
	command.SetIn(strings.NewReader(`[["Name","Score"],["Ada",10]]`))
	command.SetArgs([]string{
		"--spreadsheet", "sheet-1",
		"--range", "Sheet1!A:B",
		"--values", "-",
		"--input-option", "RAW",
		"--dry-run",
	})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	if !strings.Contains(got, `"Ada"`) ||
		!strings.Contains(got, `valueInputOption=RAW`) ||
		!strings.Contains(got, `insertDataOption=INSERT_ROWS`) {
		t.Fatalf("output = %q", got)
	}
}

func TestSheetsAppendRejectsInvalidChoice(t *testing.T) {
	command := newSheetsAppendCommand(sheetsTestDocument(), &bytes.Buffer{})
	command.SetArgs([]string{
		"--spreadsheet", "sheet-1",
		"--range", "A1",
		"--values", `[[1]]`,
		"--input-option", "INVALID",
		"--dry-run",
	})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "--input-option") {
		t.Fatalf("error = %v", err)
	}
}

func sheetsTestDocument() *discovery.Document {
	return &discovery.Document{
		Name:    "sheets",
		Version: "v4",
		BaseURL: "https://sheets.googleapis.com/",
		Resources: map[string]*discovery.Resource{
			"spreadsheets": {
				Resources: map[string]*discovery.Resource{
					"values": {
						Methods: map[string]*discovery.Method{
							"get": {
								HTTPMethod: "GET",
								Path:       "v4/spreadsheets/{spreadsheetId}/values/{range}",
								Parameters: map[string]*discovery.Parameter{
									"spreadsheetId":  {Location: "path", Required: true},
									"range":          {Location: "path", Required: true},
									"majorDimension": {},
								},
							},
							"append": {
								HTTPMethod: "POST",
								Path:       "v4/spreadsheets/{spreadsheetId}/values/{range}:append",
								Parameters: map[string]*discovery.Parameter{
									"spreadsheetId":           {Location: "path", Required: true},
									"range":                   {Location: "path", Required: true},
									"valueInputOption":        {Required: true},
									"insertDataOption":        {},
									"includeValuesInResponse": {},
								},
							},
						},
					},
				},
			},
		},
	}
}
