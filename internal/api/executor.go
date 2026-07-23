// Package api builds and executes dynamically discovered REST methods.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

// Options contains inputs shared by dynamically discovered API methods.
type Options struct {
	ParamsJSON        string
	BodyJSON          string
	OutputPath        string
	UploadPath        string
	UploadContentType string
	DryRun            bool
	PageAll           bool
	PageLimit         int
	PageDelay         time.Duration
	Out               io.Writer
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
	isUpload := opts.UploadPath != ""
	if isUpload && opts.PageAll {
		return errors.New("--upload cannot be combined with --page-all")
	}
	var uploadInfo map[string]any
	if isUpload {
		if !supportsMultipartUpload(method) {
			return errors.New("this API method does not support multipart media upload")
		}
		info, statErr := os.Stat(opts.UploadPath)
		if statErr != nil {
			return fmt.Errorf("inspect upload file: %w", statErr)
		}
		if !info.Mode().IsRegular() {
			return errors.New("upload path must identify a regular file")
		}
		contentType := resolveUploadContentType(body, opts.UploadPath, opts.UploadContentType)
		if err := validateUploadContentType(contentType); err != nil {
			return err
		}
		uploadInfo = map[string]any{
			"bytes":        info.Size(),
			"content_type": contentType,
			"path":         opts.UploadPath,
		}
	}
	requestURL, err := buildURL(doc, method, params, isUpload)
	if err != nil {
		return err
	}
	if opts.DryRun {
		preview := map[string]any{
			"body": body, "dry_run": true, "method": method.HTTPMethod, "url": requestURL,
		}
		if uploadInfo != nil {
			preview["upload"] = uploadInfo
		}
		return writeJSON(opts.Out, preview)
	}
	if e.Client == nil {
		return errors.New("HTTP client is required")
	}

	pageURL := requestURL
	for page := 1; ; page++ {
		responseBody, contentType, err := e.request(ctx, method, pageURL, body, opts)
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

func (e Executor) request(ctx context.Context, method *discovery.Method, requestURL string, body any, opts Options) ([]byte, string, error) {
	var reader io.Reader
	contentType := ""
	if opts.UploadPath != "" {
		encoded, multipartType, err := buildMultipartUpload(body, opts.UploadPath, opts.UploadContentType)
		if err != nil {
			return nil, "", err
		}
		reader = bytes.NewReader(encoded)
		contentType = multipartType
	} else if body != nil {
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
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	} else if body != nil {
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

func buildMultipartUpload(metadata any, uploadPath, explicitContentType string) ([]byte, string, error) {
	file, err := os.Open(uploadPath)
	if err != nil {
		return nil, "", fmt.Errorf("open upload file: %w", err)
	}
	defer func() { _ = file.Close() }()

	metadataJSON := []byte("{}")
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return nil, "", fmt.Errorf("encode upload metadata: %w", err)
		}
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	metadataHeaders := make(textproto.MIMEHeader)
	metadataHeaders.Set("Content-Type", "application/json; charset=UTF-8")
	metadataPart, err := writer.CreatePart(metadataHeaders)
	if err != nil {
		return nil, "", fmt.Errorf("create upload metadata part: %w", err)
	}
	if _, err := metadataPart.Write(metadataJSON); err != nil {
		return nil, "", fmt.Errorf("write upload metadata part: %w", err)
	}

	mediaContentType := resolveUploadContentType(metadata, uploadPath, explicitContentType)
	if err := validateUploadContentType(mediaContentType); err != nil {
		return nil, "", err
	}
	mediaHeaders := make(textproto.MIMEHeader)
	mediaHeaders.Set("Content-Type", mediaContentType)
	mediaPart, err := writer.CreatePart(mediaHeaders)
	if err != nil {
		return nil, "", fmt.Errorf("create upload media part: %w", err)
	}
	if _, err := io.Copy(mediaPart, file); err != nil {
		return nil, "", fmt.Errorf("write upload media part: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("finish multipart upload: %w", err)
	}
	return body.Bytes(), "multipart/related; boundary=" + writer.Boundary(), nil
}

func resolveUploadContentType(metadata any, uploadPath, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if object, ok := metadata.(map[string]any); ok {
		if value, ok := object["mimeType"].(string); ok && value != "" {
			return value
		}
	}
	if detected := mime.TypeByExtension(strings.ToLower(filepath.Ext(uploadPath))); detected != "" {
		return detected
	}
	return "application/octet-stream"
}

func validateUploadContentType(value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return errors.New("upload content type must not contain CR or LF")
	}
	if _, _, err := mime.ParseMediaType(value); err != nil {
		return fmt.Errorf("invalid upload content type %q: %w", value, err)
	}
	return nil
}

func supportsMultipartUpload(method *discovery.Method) bool {
	return method.SupportsMediaUpload && method.MediaUpload != nil && method.MediaUpload.Protocols.Simple != nil && method.MediaUpload.Protocols.Simple.Multipart && method.MediaUpload.Protocols.Simple.Path != ""
}

// BuildURL renders path parameters and encodes query parameters for a method.
func BuildURL(doc *discovery.Document, method *discovery.Method, params map[string]any) (string, error) {
	return buildURL(doc, method, params, false)
}

func buildURL(doc *discovery.Document, method *discovery.Method, params map[string]any, upload bool) (string, error) {
	pathTemplate := choosePath(method)
	baseURL := doc.BaseURL
	if upload {
		if !supportsMultipartUpload(method) {
			return "", errors.New("this API method does not support multipart media upload")
		}
		pathTemplate = method.MediaUpload.Protocols.Simple.Path
		baseURL = strings.TrimRight(doc.RootURL, "/") + "/"
	}
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
	if baseURL == "" {
		baseURL = strings.TrimRight(doc.RootURL, "/") + "/" + strings.Trim(doc.ServicePath, "/") + "/"
	}
	full, err := url.Parse(strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(rendered, "/"))
	if err != nil {
		return "", err
	}
	query := full.Query()
	if upload {
		query.Set("uploadType", "multipart")
	}
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
