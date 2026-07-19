// Package api builds and executes dynamically discovered REST methods.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

// Options contains inputs shared by dynamically discovered API methods.
type Options struct {
	ParamsJSON string
	BodyJSON   string
	OutputPath string
	DryRun     bool
	PageAll    bool
	PageLimit  int
	PageDelay  time.Duration
	Out        io.Writer
}

// Executor sends requests to a Google Workspace REST API.
type Executor struct {
	Client *http.Client
}

var templatePattern = regexp.MustCompile(`\{(\+?)([^}]+)\}`)

// Execute builds, sends, and renders one discovered API method call.
func (e Executor) Execute(ctx context.Context, doc *discovery.Document, method *discovery.Method, opts Options) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.PageLimit <= 0 {
		opts.PageLimit = 10
	}
	params, err := parseObject(opts.ParamsJSON, "--params")
	if err != nil {
		return err
	}
	body, err := parseBody(opts.BodyJSON)
	if err != nil {
		return err
	}
	requestURL, err := BuildURL(doc, method, params)
	if err != nil {
		return err
	}
	if opts.DryRun {
		return writeJSON(opts.Out, map[string]any{
			"body": body, "dry_run": true, "method": method.HTTPMethod, "url": requestURL,
		})
	}
	if e.Client == nil {
		return errors.New("HTTP client is required")
	}

	pageURL := requestURL
	for page := 1; ; page++ {
		responseBody, contentType, err := e.request(ctx, method, pageURL, body)
		if err != nil {
			return err
		}
		if opts.OutputPath != "" {
			if opts.PageAll {
				return errors.New("--output cannot be combined with --page-all")
			}
			if err := os.WriteFile(opts.OutputPath, responseBody, 0o600); err != nil {
				return fmt.Errorf("write output file: %w", err)
			}
			return writeJSON(opts.Out, map[string]any{"bytes": len(responseBody), "content_type": contentType, "saved_file": opts.OutputPath})
		}
		if len(bytes.TrimSpace(responseBody)) == 0 {
			return nil
		}
		var value any
		isJSON := json.Unmarshal(responseBody, &value) == nil
		if isJSON {
			if err := writeJSON(opts.Out, value); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(opts.Out, string(responseBody)); err != nil {
				return err
			}
		}
		if !opts.PageAll || !isJSON || page >= opts.PageLimit {
			return nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		next, _ := object["nextPageToken"].(string)
		if next == "" {
			return nil
		}
		parsed, err := url.Parse(requestURL)
		if err != nil {
			return err
		}
		query := parsed.Query()
		query.Set("pageToken", next)
		parsed.RawQuery = query.Encode()
		pageURL = parsed.String()
		if opts.PageDelay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(opts.PageDelay):
			}
		}
	}
}

func (e Executor) request(ctx context.Context, method *discovery.Method, requestURL string, body any) ([]byte, string, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method.HTTPMethod, requestURL, reader)
	if err != nil {
		return nil, "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := e.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("google API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("google API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return responseBody, resp.Header.Get("Content-Type"), nil
}

// BuildURL renders path parameters and encodes query parameters for a method.
func BuildURL(doc *discovery.Document, method *discovery.Method, params map[string]any) (string, error) {
	pathTemplate := choosePath(method)
	pathNames := make(map[string]bool)
	for _, match := range templatePattern.FindAllStringSubmatch(pathTemplate, -1) {
		pathNames[match[2]] = true
	}
	for name, definition := range method.Parameters {
		if definition.Required {
			if _, ok := params[name]; !ok {
				return "", fmt.Errorf("required parameter %q is missing; provide it in --params", name)
			}
		}
	}

	var renderErr error
	rendered := templatePattern.ReplaceAllStringFunc(pathTemplate, func(token string) string {
		match := templatePattern.FindStringSubmatch(token)
		value, ok := params[match[2]]
		if !ok {
			renderErr = fmt.Errorf("required path parameter %q is missing; provide it in --params", match[2])
			return token
		}
		text := scalarString(value)
		if match[1] == "+" {
			encoded, err := encodeReservedPath(text)
			if err != nil {
				renderErr = err
				return token
			}
			return encoded
		}
		return url.PathEscape(text)
	})
	if renderErr != nil {
		return "", renderErr
	}
	baseURL := doc.BaseURL
	if baseURL == "" {
		baseURL = strings.TrimRight(doc.RootURL, "/") + "/" + strings.Trim(doc.ServicePath, "/") + "/"
	}
	full, err := url.Parse(strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(rendered, "/"))
	if err != nil {
		return "", err
	}
	query := full.Query()
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if pathNames[name] {
			continue
		}
		definition, known := method.Parameters[name]
		if !known {
			definition, known = doc.Parameters[name]
		}
		if !known {
			return "", fmt.Errorf("unknown parameter %q", name)
		}
		value := params[name]
		if definition.Repeated {
			items, ok := value.([]any)
			if !ok {
				return "", fmt.Errorf("repeated parameter %q must be a JSON array", name)
			}
			for _, item := range items {
				query.Add(name, scalarString(item))
			}
			continue
		}
		if _, array := value.([]any); array {
			return "", fmt.Errorf("parameter %q does not accept a JSON array", name)
		}
		query.Set(name, scalarString(value))
	}
	full.RawQuery = query.Encode()
	return full.String(), nil
}

func choosePath(method *discovery.Method) string {
	if method.FlatPath == "" {
		return method.Path
	}
	for name, parameter := range method.Parameters {
		if parameter.Location != "path" {
			continue
		}
		if !strings.Contains(method.FlatPath, "{"+name+"}") && !strings.Contains(method.FlatPath, "{+"+name+"}") {
			return method.Path
		}
	}
	return method.FlatPath
}

func encodeReservedPath(value string) (string, error) {
	parts := strings.Split(value, "/")
	for index, part := range parts {
		if part == "." || part == ".." || strings.IndexFunc(part, unicode.IsControl) >= 0 || strings.ContainsAny(part, "?#") {
			return "", errors.New("path parameter contains an unsafe path segment")
		}
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/"), nil
}

func parseObject(raw, flag string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid %s JSON: %w", flag, err)
	}
	if value == nil {
		return nil, fmt.Errorf("%s must be a JSON object", flag)
	}
	return value, nil
}

func parseBody(raw string) (any, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid --json body: %w", err)
	}
	return value, nil
}

func scalarString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}
