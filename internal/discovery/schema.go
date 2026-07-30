package discovery

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ResolveSchema returns the named top-level schema.
func (doc *Document) ResolveSchema(ref string) (*Schema, error) {
	if doc == nil {
		return nil, errors.New("discovery document is required")
	}
	schema, ok := doc.Schemas[ref]
	if !ok {
		return nil, fmt.Errorf("discovery schema %q was not found", ref)
	}
	return schema, nil
}

// ValidateRequest validates a decoded JSON request body against a method's
// Discovery schema.
func (doc *Document) ValidateRequest(ref *SchemaRef, value any) error {
	if ref == nil || value == nil {
		return nil
	}
	schema, err := doc.ResolveSchema(ref.Ref)
	if err != nil {
		return err
	}
	return doc.validateSchema(schema, value, "$", map[string]int{})
}

func (doc *Document) validateSchema(schema *Schema, value any, path string, refs map[string]int) error {
	if schema == nil {
		return nil
	}
	if schema.Ref != "" {
		if refs[schema.Ref] > 64 {
			return fmt.Errorf("%s: schema reference depth exceeded for %q", path, schema.Ref)
		}
		resolved, err := doc.ResolveSchema(schema.Ref)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		refs[schema.Ref]++
		defer func() { refs[schema.Ref]-- }()
		return doc.validateSchema(resolved, value, path, refs)
	}
	if value == nil {
		return fmt.Errorf("%s: null is not valid for type %s", path, schema.Type)
	}
	if len(schema.Enum) > 0 && !enumContains(schema.Enum, value) {
		return fmt.Errorf("%s: value must be one of %s", path, enumJSON(schema.Enum))
	}

	switch schema.Type {
	case "", "any":
		return nil
	case "object":
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: expected object, got %s", path, jsonType(value))
		}
		for _, name := range requiredPropertyNames(schema) {
			if _, exists := object[name]; !exists {
				return fmt.Errorf("%s.%s: required property is missing", path, name)
			}
		}
		for name, propertyValue := range object {
			property, known := schema.Properties[name]
			if known {
				if property.ReadOnly {
					return fmt.Errorf("%s.%s: property is read-only", path, name)
				}
				if err := doc.validateSchema(property, propertyValue, path+"."+name, refs); err != nil {
					return err
				}
				continue
			}
			if schema.AdditionalProperties != nil {
				if err := doc.validateSchema(schema.AdditionalProperties, propertyValue, path+"."+name, refs); err != nil {
					return err
				}
				continue
			}
			if len(schema.Properties) > 0 {
				return fmt.Errorf("%s.%s: unknown property", path, name)
			}
		}
	case "array":
		array, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s: expected array, got %s", path, jsonType(value))
		}
		for index, item := range array {
			if err := doc.validateSchema(schema.Items, item, fmt.Sprintf("%s[%d]", path, index), refs); err != nil {
				return err
			}
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: expected string, got %s", path, jsonType(value))
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: expected boolean, got %s", path, jsonType(value))
		}
	case "integer":
		if !isInteger(value) {
			return fmt.Errorf("%s: expected integer, got %s", path, jsonType(value))
		}
	case "number":
		if !isNumber(value) {
			return fmt.Errorf("%s: expected number, got %s", path, jsonType(value))
		}
	}
	return nil
}

func enumContains(allowed []any, value any) bool {
	got, err := json.Marshal(value)
	if err != nil {
		return false
	}
	for _, candidate := range allowed {
		want, marshalErr := json.Marshal(candidate)
		if marshalErr == nil && string(got) == string(want) {
			return true
		}
	}
	return false
}

func enumJSON(values []any) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func isNumber(value any) bool {
	switch value.(type) {
	case json.Number, float32, float64, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func isInteger(value any) bool {
	switch number := value.(type) {
	case json.Number:
		text := number.String()
		return !strings.ContainsAny(text, ".eE")
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float64:
		return number == float64(int64(number))
	case float32:
		return number == float32(int64(number))
	default:
		return false
	}
}

func jsonType(value any) string {
	switch value.(type) {
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		if isNumber(value) {
			return "number"
		}
		return fmt.Sprintf("%T", value)
	}
}

// DescribeSchema returns a JSON-friendly, reference-resolved representation.
// Recursive references are retained as $ref values to avoid infinite output.
func (doc *Document) DescribeSchema(ref string) (map[string]any, error) {
	schema, err := doc.ResolveSchema(ref)
	if err != nil {
		return nil, err
	}
	return doc.describeSchema(schema, map[string]bool{ref: true}), nil
}

func (doc *Document) describeSchema(schema *Schema, seen map[string]bool) map[string]any {
	if schema == nil {
		return nil
	}
	if schema.Ref != "" {
		if seen[schema.Ref] {
			return map[string]any{"$ref": schema.Ref, "recursive": true}
		}
		resolved, ok := doc.Schemas[schema.Ref]
		if !ok {
			return map[string]any{"$ref": schema.Ref, "unresolved": true}
		}
		next := cloneSeen(seen)
		next[schema.Ref] = true
		result := doc.describeSchema(resolved, next)
		result["$ref"] = schema.Ref
		return result
	}
	result := make(map[string]any)
	copySchemaField(result, "id", schema.ID)
	copySchemaField(result, "type", schema.Type)
	copySchemaField(result, "format", schema.Format)
	copySchemaField(result, "description", schema.Description)
	if schema.Required != nil {
		result["required"] = schema.Required
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}
	if schema.Default != nil {
		result["default"] = schema.Default
	}
	if schema.Example != nil {
		result["example"] = schema.Example
	}
	if schema.ReadOnly {
		result["readOnly"] = true
	}
	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for name, property := range schema.Properties {
			properties[name] = doc.describeSchema(property, cloneSeen(seen))
		}
		result["properties"] = properties
	}
	if schema.Items != nil {
		result["items"] = doc.describeSchema(schema.Items, cloneSeen(seen))
	}
	if schema.AdditionalProperties != nil {
		result["additionalProperties"] = doc.describeSchema(schema.AdditionalProperties, cloneSeen(seen))
	}
	return result
}

func copySchemaField(target map[string]any, name, value string) {
	if value != "" {
		target[name] = value
	}
}

func cloneSeen(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

// ExampleForSchema creates a small example request body from schema metadata.
func (doc *Document) ExampleForSchema(ref string) (any, error) {
	schema, err := doc.ResolveSchema(ref)
	if err != nil {
		return nil, err
	}
	return doc.exampleForSchema(schema, map[string]bool{ref: true}), nil
}

func (doc *Document) exampleForSchema(schema *Schema, seen map[string]bool) any {
	if schema == nil {
		return nil
	}
	if schema.Example != nil {
		return schema.Example
	}
	if schema.Default != nil {
		return schema.Default
	}
	if len(schema.Enum) > 0 {
		return schema.Enum[0]
	}
	if schema.Ref != "" {
		if seen[schema.Ref] {
			return nil
		}
		resolved := doc.Schemas[schema.Ref]
		next := cloneSeen(seen)
		next[schema.Ref] = true
		return doc.exampleForSchema(resolved, next)
	}
	switch schema.Type {
	case "object":
		result := make(map[string]any)
		requiredNames := requiredPropertyNames(schema)
		required := make(map[string]bool, len(requiredNames))
		for _, name := range requiredNames {
			required[name] = true
		}
		names := make([]string, 0, len(schema.Properties))
		for name := range schema.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			property := schema.Properties[name]
			if property.ReadOnly || (!required[name] && property.Example == nil && property.Default == nil && len(property.Enum) == 0) {
				continue
			}
			result[name] = doc.exampleForSchema(property, cloneSeen(seen))
		}
		return result
	case "array":
		return []any{doc.exampleForSchema(schema.Items, cloneSeen(seen))}
	case "string":
		return "string"
	case "boolean":
		return false
	case "integer", "number":
		return 0
	default:
		return nil
	}
}

func requiredPropertyNames(schema *Schema) []string {
	var names []string
	switch required := schema.Required.(type) {
	case []string:
		names = append(names, required...)
	case []any:
		for _, value := range required {
			if name, ok := value.(string); ok {
				names = append(names, name)
			}
		}
	}
	for name, property := range schema.Properties {
		if required, ok := property.Required.(bool); ok && required {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
