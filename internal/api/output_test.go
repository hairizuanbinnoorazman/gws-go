package api

import (
	"strings"
	"testing"
)

func TestMergePagesAggregatesArrays(t *testing.T) {
	merged := mergePages([]any{
		map[string]any{"files": []any{map[string]any{"id": "1"}}, "nextPageToken": "next"},
		map[string]any{"files": []any{map[string]any{"id": "2"}}},
	}).(map[string]any)
	files := merged["files"].([]any)
	if len(files) != 2 {
		t.Fatalf("merged = %#v", merged)
	}
	if _, exists := merged["nextPageToken"]; exists {
		t.Fatalf("nextPageToken was retained: %#v", merged)
	}
}

func TestRenderOutputFormatsAndFieldSelection(t *testing.T) {
	value := map[string]any{"files": []any{
		map[string]any{"id": "1", "name": "Report", "owners": map[string]any{"name": "Ada"}},
		map[string]any{"id": "2", "name": "Plan", "owners": map[string]any{"name": "Lin"}},
	}}
	tests := []struct {
		format string
		match  string
	}{
		{format: "json", match: `"owners"`},
		{format: "jsonl", match: `{"id":"1","owners":{"name":"Ada"}}`},
		{format: "table", match: "owners.name"},
		{format: "yaml", match: "owners:"},
		{format: "csv", match: "id,owners.name"},
	}
	for _, test := range tests {
		t.Run(test.format, func(t *testing.T) {
			var output strings.Builder
			err := renderOutput(&output, value, Options{Format: test.format, Fields: "id,owners.name"})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), test.match) || strings.Contains(output.String(), "Report") {
				t.Fatalf("output = %q", output.String())
			}
		})
	}
}

func TestRenderQuietUsesIDsOrSelectedFields(t *testing.T) {
	value := map[string]any{"items": []any{
		map[string]any{"id": "one", "name": "First"},
		map[string]any{"id": "two", "name": "Second"},
	}}
	var ids strings.Builder
	if err := renderOutput(&ids, value, Options{Quiet: true}); err != nil {
		t.Fatal(err)
	}
	if ids.String() != "one\ntwo\n" {
		t.Fatalf("ids = %q", ids.String())
	}
	var fields strings.Builder
	if err := renderOutput(&fields, value, Options{Quiet: true, Fields: "name,id"}); err != nil {
		t.Fatal(err)
	}
	if fields.String() != "First\tone\nSecond\ttwo\n" {
		t.Fatalf("fields = %q", fields.String())
	}
}

func TestRenderOutputRejectsUnknownFormat(t *testing.T) {
	err := renderOutput(&strings.Builder{}, map[string]any{"id": "1"}, Options{Format: "xml"})
	if err == nil || !strings.Contains(err.Error(), "unsupported format") {
		t.Fatalf("error = %v", err)
	}
}
