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
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

// Options contains inputs shared by dynamically discovered API methods.
type Options struct {
	ParamsJSON        string
	BodyJSON          string
	OutputPath        string
	UploadPath        string
	UploadContentType string
	ParamsFile        string
	BodyFile          string
	Input             io.Reader
	DryRun            bool
	PageAll           bool
	PageLimit         int
	PageDelay         time.Duration
	RequestTimeout    time.Duration
	MaxRetries        int
	RetryDelay        time.Duration
	Format            string
	Fields            string
	Quiet             bool
	Out               io.Writer
}

// Executor sends requests to a Google Workspace REST API.
type Executor struct {
	Client *http.Client
	Now    func() time.Time
	Sleep  func(context.Context, time.Duration) error
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
	if opts.MaxRetries < 0 {
		return clierr.New("invalid_argument", "--max-retries must not be negative", clierr.ExitInput, nil)
	}
	if opts.RequestTimeout < 0 {
		return clierr.New("invalid_argument", "--timeout must not be negative", clierr.ExitInput, nil)
	}
	if opts.RetryDelay < 0 {
		return clierr.New("invalid_argument", "--retry-delay must not be negative", clierr.ExitInput, nil)
	}
	paramsJSON, bodyJSON, err := resolveJSONInputs(opts)
	if err != nil {
		return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
	}
	params, err := parseObject(paramsJSON, "--params")
	if err != nil {
		return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
	}
	body, err := parseBody(bodyJSON)
	if err != nil {
		return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
	}
	if err := doc.ValidateRequest(method.Request, body); err != nil {
		return clierr.New("request_validation_failed", err.Error(), clierr.ExitInput, nil)
	}
	isUpload := opts.UploadPath != ""
	if isUpload && opts.PageAll {
		return clierr.New("invalid_argument", "--upload cannot be combined with --page-all", clierr.ExitInput, nil)
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
		return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
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
	var pageValues []any
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
		if !isJSON {
			if opts.Format != "" && opts.Format != "json" {
				return clierr.New("output_format_error", "non-JSON responses support only --format json or --output", clierr.ExitInput, nil)
			}
			if _, err := fmt.Fprintln(opts.Out, string(responseBody)); err != nil {
				return err
			}
			return nil
		}
		pageValues = append(pageValues, value)
		if !opts.PageAll || page >= opts.PageLimit {
			break
		}
		object, ok := value.(map[string]any)
		if !ok {
			break
		}
		next, _ := object["nextPageToken"].(string)
		if next == "" {
			break
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
	value := pageValues[0]
	if opts.PageAll {
		value = mergePages(pageValues)
	}
	if err := renderOutput(opts.Out, value, opts); err != nil {
		return clierr.New("output_format_error", "render response", clierr.ExitInput, err)
	}
	return nil
}

func resolveJSONInputs(opts Options) (string, string, error) {
	if opts.Input == nil {
		opts.Input = os.Stdin
	}
	stdinUses := 0
	for _, value := range []string{opts.ParamsJSON, opts.BodyJSON, opts.ParamsFile, opts.BodyFile} {
		if value == "-" {
			stdinUses++
		}
	}
	if stdinUses > 1 {
		return "", "", errors.New("stdin may be used by only one JSON input")
	}
	params, err := resolveJSONInput(opts.ParamsJSON, opts.ParamsFile, "--params", "--params-file", opts.Input)
	if err != nil {
		return "", "", err
	}
	body, err := resolveJSONInput(opts.BodyJSON, opts.BodyFile, "--json", "--json-file", opts.Input)
	if err != nil {
		return "", "", err
	}
	return params, body, nil
}

func resolveJSONInput(inline, path, inlineFlag, fileFlag string, input io.Reader) (string, error) {
	if inline != "" && path != "" {
		return "", fmt.Errorf("%s and %s cannot be combined", inlineFlag, fileFlag)
	}
	if inline == "-" {
		data, err := io.ReadAll(io.LimitReader(input, 16<<20))
		if err != nil {
			return "", fmt.Errorf("read %s from stdin: %w", inlineFlag, err)
		}
		return string(data), nil
	}
	if path == "" {
		return inline, nil
	}
	if path == "-" {
		data, err := io.ReadAll(io.LimitReader(input, 16<<20))
		if err != nil {
			return "", fmt.Errorf("read %s from stdin: %w", fileFlag, err)
		}
		return string(data), nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", fileFlag, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must identify a regular file", fileFlag)
	}
	if info.Size() > 16<<20 {
		return "", fmt.Errorf("%s exceeds the 16 MiB limit", fileFlag)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fileFlag, err)
	}
	return string(data), nil
}

func (e Executor) request(ctx context.Context, method *discovery.Method, requestURL string, body any, opts Options) ([]byte, string, error) {
	var encodedBody []byte
	contentType := ""
	if opts.UploadPath != "" {
		encoded, multipartType, err := buildMultipartUpload(body, opts.UploadPath, opts.UploadContentType)
		if err != nil {
			return nil, "", err
		}
		encodedBody = encoded
		contentType = multipartType
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, "", err
		}
		encodedBody = encoded
	}

	for attempt := 0; ; attempt++ {
		requestCtx := ctx
		cancel := func() {}
		if opts.RequestTimeout > 0 {
			requestCtx, cancel = context.WithTimeout(ctx, opts.RequestTimeout)
		}
		var reader io.Reader
		if encodedBody != nil {
			reader = bytes.NewReader(encodedBody)
		}
		req, err := http.NewRequestWithContext(requestCtx, method.HTTPMethod, requestURL, reader)
		if err != nil {
			cancel()
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
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
				return nil, "", clierr.New("request_timeout", "Google API request timed out", clierr.ExitTimeout, err)
			}
			return nil, "", clierr.New("network_error", "Google API request failed", clierr.ExitNetwork, err)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		closeErr := resp.Body.Close()
		cancel()
		if readErr != nil {
			return nil, "", clierr.New("network_error", "read Google API response", clierr.ExitNetwork, readErr)
		}
		if closeErr != nil {
			return nil, "", clierr.New("network_error", "close Google API response", clierr.ExitNetwork, closeErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return responseBody, resp.Header.Get("Content-Type"), nil
		}

		retryable := retryableStatus(resp.StatusCode)
		if retryable && attempt < opts.MaxRetries {
			delay := retryBackoff(opts.RetryDelay, attempt)
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), e.now()); retryAfter > delay {
				delay = retryAfter
			}
			if err := e.sleep(ctx, delay); err != nil {
				return nil, "", err
			}
			continue
		}
		details := decodeErrorDetails(responseBody)
		message := fmt.Sprintf("Google API returned HTTP %d", resp.StatusCode)
		if detailMessage := errorDetailMessage(details); detailMessage != "" {
			message += ": " + detailMessage
		}
		apiError := clierr.New("google_api_error", message, clierr.ExitAPI, nil)
		apiError.Status = resp.StatusCode
		apiError.Retryable = retryable
		apiError.Attempts = attempt + 1
		apiError.Details = details
		return nil, "", apiError
	}
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func retryBackoff(base time.Duration, retry int) time.Duration {
	if base <= 0 {
		return 0
	}
	const maximum = 30 * time.Second
	for range retry {
		if base >= maximum/2 {
			return maximum
		}
		base *= 2
	}
	if base > maximum {
		return maximum
	}
	return base
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}

func decodeErrorDetails(body []byte) any {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	var value any
	if json.Unmarshal(body, &value) == nil {
		return value
	}
	const maximum = 4096
	if len(body) > maximum {
		body = body[:maximum]
	}
	return strings.TrimSpace(string(body))
}

func errorDetailMessage(details any) string {
	object, ok := details.(map[string]any)
	if !ok {
		if text, textOK := details.(string); textOK {
			return text
		}
		return ""
	}
	if nested, nestedOK := object["error"].(map[string]any); nestedOK {
		if message, messageOK := nested["message"].(string); messageOK {
			return message
		}
	}
	message, _ := object["message"].(string)
	return message
}

func (e Executor) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e Executor) sleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	if e.Sleep != nil {
		return e.Sleep(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
