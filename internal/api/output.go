package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"gopkg.in/yaml.v3"
)

var collectionKeys = []string{"items", "files", "messages", "events", "values", "responses"}

func renderOutput(out io.Writer, value any, opts Options) error {
	format := opts.Format
	if format == "" {
		format = "json"
	}
	switch format {
	case "json", "jsonl", "table", "yaml", "csv":
	default:
		return fmt.Errorf("unsupported format %q; use json, jsonl, table, yaml, or csv", format)
	}
	fields := parseFields(opts.Fields)
	if opts.Quiet {
		return renderQuiet(out, value, fields)
	}
	if len(fields) > 0 {
		value = selectFields(value, fields)
	}
	switch format {
	case "json":
		return writeJSON(out, value)
	case "jsonl":
		encoder := json.NewEncoder(out)
		encoder.SetEscapeHTML(false)
		for _, item := range rowsFromValue(value) {
			if err := encoder.Encode(item); err != nil {
				return err
			}
		}
		return nil
	case "yaml":
		data, err := yaml.Marshal(value)
		if err != nil {
			return err
		}
		_, err = out.Write(data)
		return err
	case "table":
		return renderTable(out, rowsFromValue(value))
	case "csv":
		return renderCSV(out, rowsFromValue(value))
	default:
		panic("validated output format")
	}
}

func mergePages(pages []any) any {
	if len(pages) == 0 {
		return nil
	}
	first, ok := pages[0].(map[string]any)
	if !ok {
		return pages
	}
	merged := make(map[string]any, len(first))
	for key, value := range first {
		merged[key] = value
	}
	delete(merged, "nextPageToken")
	for _, rawPage := range pages[1:] {
		page, objectOK := rawPage.(map[string]any)
		if !objectOK {
			continue
		}
		for key, value := range page {
			if key == "nextPageToken" {
				continue
			}
			items, array := value.([]any)
			existing, existingArray := merged[key].([]any)
			if array && existingArray {
				merged[key] = append(existing, items...)
				continue
			}
			merged[key] = value
		}
	}
	return merged
}

func parseFields(raw string) []string {
	var result []string
	for _, field := range strings.Split(raw, ",") {
		field = strings.TrimSpace(field)
		if field != "" {
			result = append(result, field)
		}
	}
	return result
}

func selectFields(value any, fields []string) any {
	if array, ok := value.([]any); ok {
		result := make([]any, 0, len(array))
		for _, item := range array {
			result = append(result, selectFields(item, fields))
		}
		return result
	}
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	for _, key := range collectionKeys {
		if array, exists := object[key].([]any); exists {
			result := make([]any, 0, len(array))
			for _, item := range array {
				result = append(result, selectFields(item, fields))
			}
			return result
		}
	}
	result := make(map[string]any)
	for _, field := range fields {
		if selected, exists := lookupPath(object, strings.Split(field, ".")); exists {
			assignPath(result, strings.Split(field, "."), selected)
		}
	}
	return result
}

func lookupPath(value any, parts []string) (any, bool) {
	current := value
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func assignPath(target map[string]any, parts []string, value any) {
	if len(parts) == 1 {
		target[parts[0]] = value
		return
	}
	child, _ := target[parts[0]].(map[string]any)
	if child == nil {
		child = make(map[string]any)
		target[parts[0]] = child
	}
	assignPath(child, parts[1:], value)
}

func rowsFromValue(value any) []any {
	if array, ok := value.([]any); ok {
		return array
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range collectionKeys {
			if array, exists := object[key].([]any); exists {
				return array
			}
		}
	}
	return []any{value}
}

func renderQuiet(out io.Writer, value any, fields []string) error {
	rows := rowsFromValue(value)
	for _, row := range rows {
		var values []any
		if len(fields) > 0 {
			for _, field := range fields {
				if selected, ok := lookupPath(row, strings.Split(field, ".")); ok {
					values = append(values, selected)
				}
			}
		} else if object, ok := row.(map[string]any); ok {
			if id, exists := object["id"]; exists {
				values = append(values, id)
			}
		} else {
			values = append(values, row)
		}
		if len(values) == 0 {
			continue
		}
		parts := make([]string, len(values))
		for index, item := range values {
			parts[index] = scalarOutput(item)
		}
		if _, err := fmt.Fprintln(out, strings.Join(parts, "\t")); err != nil {
			return err
		}
	}
	return nil
}

func renderTable(out io.Writer, rows []any) error {
	headers, records := tabularRows(rows)
	if len(headers) == 0 {
		return errors.New("table output requires an object or array of objects")
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for _, record := range records {
		if _, err := fmt.Fprintln(writer, strings.Join(record, "\t")); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func renderCSV(out io.Writer, rows []any) error {
	headers, records := tabularRows(rows)
	if len(headers) == 0 {
		return errors.New("CSV output requires an object or array of objects")
	}
	writer := csv.NewWriter(out)
	if err := writer.Write(headers); err != nil {
		return err
	}
	for _, record := range records {
		if err := writer.Write(record); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func tabularRows(rows []any) ([]string, [][]string) {
	flattened := make([]map[string]string, 0, len(rows))
	headerSet := make(map[string]bool)
	for _, row := range rows {
		object, ok := row.(map[string]any)
		if !ok {
			return nil, nil
		}
		record := make(map[string]string)
		flattenObject("", object, record)
		for name := range record {
			headerSet[name] = true
		}
		flattened = append(flattened, record)
	}
	headers := make([]string, 0, len(headerSet))
	for name := range headerSet {
		headers = append(headers, name)
	}
	sort.Strings(headers)
	records := make([][]string, 0, len(flattened))
	for _, record := range flattened {
		row := make([]string, len(headers))
		for index, name := range headers {
			row[index] = record[name]
		}
		records = append(records, row)
	}
	return headers, records
}

func flattenObject(prefix string, object map[string]any, target map[string]string) {
	for name, value := range object {
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if child, ok := value.(map[string]any); ok {
			flattenObject(path, child, target)
			continue
		}
		target[path] = scalarOutput(value)
	}
}

func scalarOutput(value any) string {
	switch value := value.(type) {
	case nil:
		return ""
	case string:
		return value
	case json.Number:
		return value.String()
	case bool, float64, float32, int, int64, uint64:
		return fmt.Sprint(value)
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
}
