package discovery

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRequestResolvesNestedSchemas(t *testing.T) {
	doc := schemaTestDocument()
	valid := map[string]any{
		"title":      "Planning",
		"visibility": "team",
		"attendees": []any{
			map[string]any{"email": "person@example.com"},
		},
	}
	if err := doc.ValidateRequest(&SchemaRef{Ref: "Event"}, valid); err != nil {
		t.Fatal(err)
	}
}

func TestGoogleDiscoveryBooleanRequiredMarker(t *testing.T) {
	var doc Document
	err := json.Unmarshal([]byte(`{
		"schemas": {
			"Item": {
				"type": "object",
				"properties": {
					"name": {"type": "string", "required": true}
				}
			}
		}
	}`), &doc)
	if err != nil {
		t.Fatal(err)
	}
	err = doc.ValidateRequest(&SchemaRef{Ref: "Item"}, map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "$.name") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateRequestReportsPreciseFailures(t *testing.T) {
	doc := schemaTestDocument()
	tests := []struct {
		name  string
		body  map[string]any
		match string
	}{
		{name: "required", body: map[string]any{}, match: "$.title"},
		{name: "enum", body: map[string]any{"title": "x", "visibility": "world"}, match: "$.visibility"},
		{name: "nested type", body: map[string]any{"title": "x", "attendees": []any{map[string]any{"email": true}}}, match: "$.attendees[0].email"},
		{name: "unknown", body: map[string]any{"title": "x", "bogus": true}, match: "$.bogus"},
		{name: "read only", body: map[string]any{"title": "x", "id": "server-id"}, match: "read-only"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := doc.ValidateRequest(&SchemaRef{Ref: "Event"}, test.body)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("error = %v, want match %q", err, test.match)
			}
		})
	}
}

func TestDescribeSchemaResolvesReferencesAndStopsCycles(t *testing.T) {
	doc := schemaTestDocument()
	doc.Schemas["Person"].Properties["manager"] = &Schema{Ref: "Person"}
	description, err := doc.DescribeSchema("Event")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(description)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"email"`) || !strings.Contains(text, `"recursive":true`) {
		t.Fatalf("description = %s", text)
	}
}

func TestExampleForSchemaUsesRequiredAndMetadataFields(t *testing.T) {
	doc := schemaTestDocument()
	doc.Schemas["Event"].Properties["optionalWithDefault"] = &Schema{Type: "boolean", Default: true}
	example, err := doc.ExampleForSchema("Event")
	if err != nil {
		t.Fatal(err)
	}
	object := example.(map[string]any)
	if object["title"] != "string" || object["visibility"] != "team" || object["optionalWithDefault"] != true {
		t.Fatalf("example = %#v", example)
	}
	if _, exists := object["id"]; exists {
		t.Fatalf("read-only id appeared in example: %#v", example)
	}
}

func schemaTestDocument() *Document {
	return &Document{Schemas: map[string]*Schema{
		"Event": {
			Type:     "object",
			Required: []string{"title"},
			Properties: map[string]*Schema{
				"id":         {Type: "string", ReadOnly: true},
				"title":      {Type: "string"},
				"visibility": {Type: "string", Enum: []any{"team", "private"}},
				"attendees":  {Type: "array", Items: &Schema{Ref: "Person"}},
			},
		},
		"Person": {
			Type: "object",
			Properties: map[string]*Schema{
				"email": {Type: "string"},
			},
		},
	}}
}
