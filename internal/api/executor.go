// Package api builds and executes dynamically discovered REST methods.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	ParamsJSON         string
	ParameterOverrides map[string]any
	BodyJSON           string
	OutputPath         string
	UploadPath         string
	UploadContentType  string
	ResumableUpload    bool
	UploadChunkSize    int64
	ParamsFile         string
	BodyFile           string
	Input              io.Reader
	DryRun             bool
	PageAll            bool
	PageLimit          int
	PageDelay          time.Duration
	RequestTimeout     time.Duration
	MaxRetries         int
	RetryDelay         time.Duration
	RetryUnsafe        bool
	ShowProgress       bool
	ProgressOut        io.Writer
	Format             string
	Fields             string
	Quiet              bool
	Out                io.Writer
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
	if opts.ShowProgress && opts.ProgressOut == nil {
		opts.ProgressOut = os.Stderr
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
	for name, value := range opts.ParameterOverrides {
		params[name] = value
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
		if opts.ResumableUpload && !supportsResumableUpload(method) {
			return errors.New("this API method does not support resumable media upload")
		}
		if !opts.ResumableUpload && !supportsMultipartUpload(method) {
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
	var requestURL string
	if opts.ResumableUpload {
		if !isUpload {
			return clierr.New("invalid_argument", "--resumable requires --upload", clierr.ExitInput, nil)
		}
		requestURL, err = buildResumableUploadURL(doc, method, params)
		if err != nil {
			return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
		}
		if opts.UploadChunkSize == 0 {
			opts.UploadChunkSize = 8 << 20
		}
		if opts.UploadChunkSize <= 0 || opts.UploadChunkSize%(256<<10) != 0 {
			return clierr.New("invalid_argument", "--upload-chunk-size must be a positive multiple of 256 KiB", clierr.ExitInput, nil)
		}
		uploadInfo["resumable"] = true
		uploadInfo["chunk_size"] = opts.UploadChunkSize
	} else {
		requestURL, err = buildURL(doc, method, params, isUpload)
		if err != nil {
			return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
		}
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
	if opts.ResumableUpload {
		responseBody, contentType, err := e.resumableUpload(ctx, method, requestURL, body, opts)
		if err != nil {
			return err
		}
		if opts.OutputPath != "" {
			written, checksum, writeErr := saveResponseFile(opts.OutputPath, bytes.NewReader(responseBody), opts.ProgressOut)
			if writeErr != nil {
				return clierr.New("filesystem_error", "write output file", clierr.ExitFilesystem, writeErr)
			}
			return writeJSON(opts.Out, map[string]any{
				"bytes": written, "content_type": contentType, "saved_file": opts.OutputPath, "sha256": checksum,
			})
		}
		if len(bytes.TrimSpace(responseBody)) == 0 {
			return nil
		}
		var value any
		if json.Unmarshal(responseBody, &value) != nil {
			if _, err := fmt.Fprintln(opts.Out, string(responseBody)); err != nil {
				return err
			}
			return nil
		}
		_ = contentType
		if err := renderOutput(opts.Out, value, opts); err != nil {
			return clierr.New("output_format_error", "render response", clierr.ExitInput, err)
		}
		return nil
	}

	pageURL := requestURL
	var pageValues []any
	for page := 1; ; page++ {
		responseBody, contentType, savedBytes, checksum, err := e.request(ctx, method, pageURL, body, opts)
		if err != nil {
			return err
		}
		if opts.OutputPath != "" {
			if opts.PageAll {
				return errors.New("--output cannot be combined with --page-all")
			}
			return writeJSON(opts.Out, map[string]any{
				"bytes": savedBytes, "content_type": contentType, "saved_file": opts.OutputPath, "sha256": checksum,
			})
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

func (e Executor) request(ctx context.Context, method *discovery.Method, requestURL string, body any, opts Options) ([]byte, string, int64, string, error) {
	var encodedBody []byte
	if opts.UploadPath == "" && body != nil {
		var err error
		encodedBody, err = json.Marshal(body)
		if err != nil {
			return nil, "", 0, "", err
		}
	}
	retryAllowed := requestMayRetry(method.HTTPMethod, requestURL, opts.RetryUnsafe)

	for attempt := 0; ; attempt++ {
		requestCtx := ctx
		cancel := func() {}
		if opts.RequestTimeout > 0 {
			requestCtx, cancel = context.WithTimeout(ctx, opts.RequestTimeout)
		}
		var reader io.Reader
		var bodyCloser io.Closer
		contentType := ""
		if opts.UploadPath != "" {
			uploadReader, multipartType, err := newMultipartUploadReader(body, opts.UploadPath, opts.UploadContentType, opts.ProgressOut)
			if err != nil {
				cancel()
				return nil, "", 0, "", err
			}
			reader = uploadReader
			bodyCloser = uploadReader
			contentType = multipartType
		} else if encodedBody != nil {
			reader = bytes.NewReader(encodedBody)
		}
		req, err := http.NewRequestWithContext(requestCtx, method.HTTPMethod, requestURL, reader)
		if err != nil {
			if bodyCloser != nil {
				_ = bodyCloser.Close()
			}
			cancel()
			return nil, "", 0, "", err
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		} else if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		resp, err := e.Client.Do(req)
		if err != nil {
			if bodyCloser != nil {
				_ = bodyCloser.Close()
			}
			cancel()
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
				return nil, "", 0, "", clierr.New("request_timeout", "Google API request timed out", clierr.ExitTimeout, err)
			}
			if retryAllowed && attempt < opts.MaxRetries {
				if sleepErr := e.sleep(ctx, retryBackoff(opts.RetryDelay, attempt)); sleepErr != nil {
					return nil, "", 0, "", sleepErr
				}
				continue
			}
			return nil, "", 0, "", clierr.New("network_error", "Google API request failed", clierr.ExitNetwork, err)
		}
		if bodyCloser != nil {
			_ = bodyCloser.Close()
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 && opts.OutputPath != "" {
			written, checksum, writeErr := saveResponseFile(opts.OutputPath, resp.Body, opts.ProgressOut)
			closeErr := resp.Body.Close()
			cancel()
			if writeErr != nil {
				return nil, "", 0, "", clierr.New("filesystem_error", "write output file", clierr.ExitFilesystem, writeErr)
			}
			if closeErr != nil {
				return nil, "", 0, "", clierr.New("network_error", "close Google API response", clierr.ExitNetwork, closeErr)
			}
			return nil, resp.Header.Get("Content-Type"), written, checksum, nil
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
		closeErr := resp.Body.Close()
		cancel()
		if readErr != nil {
			return nil, "", 0, "", clierr.New("network_error", "read Google API response", clierr.ExitNetwork, readErr)
		}
		if closeErr != nil {
			return nil, "", 0, "", clierr.New("network_error", "close Google API response", clierr.ExitNetwork, closeErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return responseBody, resp.Header.Get("Content-Type"), int64(len(responseBody)), "", nil
		}

		retryable := retryableStatus(resp.StatusCode)
		if retryable && retryAllowed && attempt < opts.MaxRetries {
			delay := retryBackoff(opts.RetryDelay, attempt)
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), e.now()); retryAfter > delay {
				delay = retryAfter
			}
			if err := e.sleep(ctx, delay); err != nil {
				return nil, "", 0, "", err
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
		return nil, "", 0, "", apiError
	}
}

func requestMayRetry(method, requestURL string, unsafe bool) bool {
	if unsafe {
		return true
	}
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return false
	}
	for _, name := range []string{"requestId", "request_id"} {
		if parsed.Query().Get(name) != "" {
			return true
		}
	}
	return false
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

func (e Executor) resumableUpload(ctx context.Context, method *discovery.Method, requestURL string, metadata any, opts Options) ([]byte, string, error) {
	info, err := os.Stat(opts.UploadPath)
	if err != nil {
		return nil, "", fmt.Errorf("inspect upload file: %w", err)
	}
	metadataJSON := []byte("{}")
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return nil, "", fmt.Errorf("encode upload metadata: %w", err)
		}
	}
	mediaType := resolveUploadContentType(metadata, opts.UploadPath, opts.UploadContentType)
	if err := validateUploadContentType(mediaType); err != nil {
		return nil, "", err
	}

	var sessionURL string
	for attempt := 0; ; attempt++ {
		requestCtx, cancel := requestAttemptContext(ctx, opts.RequestTimeout)
		req, requestErr := http.NewRequestWithContext(requestCtx, method.HTTPMethod, requestURL, bytes.NewReader(metadataJSON))
		if requestErr != nil {
			cancel()
			return nil, "", requestErr
		}
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		req.Header.Set("X-Upload-Content-Type", mediaType)
		req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(info.Size(), 10))
		resp, requestErr := e.Client.Do(req)
		if requestErr != nil {
			cancel()
			if isRequestTimeout(requestCtx, requestErr) {
				return nil, "", clierr.New("request_timeout", "resumable upload initialization timed out", clierr.ExitTimeout, requestErr)
			}
			if requestMayRetry(method.HTTPMethod, requestURL, opts.RetryUnsafe) && attempt < opts.MaxRetries {
				if sleepErr := e.sleep(ctx, retryBackoff(opts.RetryDelay, attempt)); sleepErr != nil {
					return nil, "", sleepErr
				}
				continue
			}
			return nil, "", clierr.New("network_error", "initialize resumable upload", clierr.ExitNetwork, requestErr)
		}
		responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		closeErr := resp.Body.Close()
		cancel()
		if readErr != nil || closeErr != nil {
			if readErr == nil {
				readErr = closeErr
			}
			return nil, "", clierr.New("network_error", "read resumable upload initialization", clierr.ExitNetwork, readErr)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			sessionURL = resp.Header.Get("Location")
			if sessionURL == "" {
				return nil, "", clierr.New("invalid_response", "resumable upload response did not include a Location header", clierr.ExitAPI, nil)
			}
			break
		}
		retryable := retryableStatus(resp.StatusCode)
		if retryable && requestMayRetry(method.HTTPMethod, requestURL, opts.RetryUnsafe) && attempt < opts.MaxRetries {
			delay := retryBackoff(opts.RetryDelay, attempt)
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), e.now()); retryAfter > delay {
				delay = retryAfter
			}
			if sleepErr := e.sleep(ctx, delay); sleepErr != nil {
				return nil, "", sleepErr
			}
			continue
		}
		return nil, "", googleAPIError(resp.StatusCode, responseBody, retryable, attempt+1)
	}

	file, err := os.Open(opts.UploadPath)
	if err != nil {
		return nil, "", fmt.Errorf("open upload file: %w", err)
	}
	defer func() { _ = file.Close() }()
	size := info.Size()
	offset := int64(0)
	first := true
	for offset < size || first {
		first = false
		chunkSize := opts.UploadChunkSize
		if remaining := size - offset; remaining < chunkSize {
			chunkSize = remaining
		}
		if size == 0 {
			chunkSize = 0
		}
		for attempt := 0; ; attempt++ {
			requestCtx, cancel := requestAttemptContext(ctx, opts.RequestTimeout)
			reader := io.NewSectionReader(file, offset, chunkSize)
			req, requestErr := http.NewRequestWithContext(requestCtx, http.MethodPut, sessionURL, reader)
			if requestErr != nil {
				cancel()
				return nil, "", requestErr
			}
			req.ContentLength = chunkSize
			req.Header.Set("Content-Type", mediaType)
			if size == 0 {
				req.Header.Set("Content-Range", "bytes */0")
			} else {
				req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+chunkSize-1, size))
			}
			resp, requestErr := e.Client.Do(req)
			if requestErr != nil {
				cancel()
				if isRequestTimeout(requestCtx, requestErr) {
					return nil, "", clierr.New("request_timeout", "resumable upload chunk timed out", clierr.ExitTimeout, requestErr)
				}
				if attempt < opts.MaxRetries {
					if sleepErr := e.sleep(ctx, retryBackoff(opts.RetryDelay, attempt)); sleepErr != nil {
						return nil, "", sleepErr
					}
					continue
				}
				return nil, "", clierr.New("network_error", "upload resumable chunk", clierr.ExitNetwork, requestErr)
			}
			responseBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
			closeErr := resp.Body.Close()
			cancel()
			if readErr != nil || closeErr != nil {
				if readErr == nil {
					readErr = closeErr
				}
				return nil, "", clierr.New("network_error", "read resumable upload response", clierr.ExitNetwork, readErr)
			}
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if opts.ProgressOut != nil {
					_, _ = fmt.Fprintf(opts.ProgressOut, "Uploaded %d bytes\n", size)
				}
				return responseBody, resp.Header.Get("Content-Type"), nil
			}
			if resp.StatusCode == 308 {
				offset = nextUploadOffset(resp.Header.Get("Range"), offset+chunkSize)
				if opts.ProgressOut != nil {
					_, _ = fmt.Fprintf(opts.ProgressOut, "Uploaded %d of %d bytes\n", offset, size)
				}
				break
			}
			retryable := retryableStatus(resp.StatusCode)
			if retryable && attempt < opts.MaxRetries {
				delay := retryBackoff(opts.RetryDelay, attempt)
				if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), e.now()); retryAfter > delay {
					delay = retryAfter
				}
				if sleepErr := e.sleep(ctx, delay); sleepErr != nil {
					return nil, "", sleepErr
				}
				continue
			}
			return nil, "", googleAPIError(resp.StatusCode, responseBody, retryable, attempt+1)
		}
	}
	return nil, "", clierr.New("invalid_response", "resumable upload ended without a final response", clierr.ExitAPI, nil)
}

func requestAttemptContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	return ctx, func() {}
}

func isRequestTimeout(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

func nextUploadOffset(header string, fallback int64) int64 {
	header = strings.TrimSpace(header)
	if !strings.HasPrefix(header, "bytes=0-") {
		return fallback
	}
	last, err := strconv.ParseInt(strings.TrimPrefix(header, "bytes=0-"), 10, 64)
	if err != nil || last < 0 {
		return fallback
	}
	return last + 1
}

func googleAPIError(status int, body []byte, retryable bool, attempts int) error {
	details := decodeErrorDetails(body)
	message := fmt.Sprintf("Google API returned HTTP %d", status)
	if detailMessage := errorDetailMessage(details); detailMessage != "" {
		message += ": " + detailMessage
	}
	apiError := clierr.New("google_api_error", message, clierr.ExitAPI, nil)
	apiError.Status = status
	apiError.Retryable = retryable
	apiError.Attempts = attempts
	apiError.Details = details
	return apiError
}

func newMultipartUploadReader(metadata any, uploadPath, explicitContentType string, progressOut io.Writer) (*io.PipeReader, string, error) {
	file, err := os.Open(uploadPath)
	if err != nil {
		return nil, "", fmt.Errorf("open upload file: %w", err)
	}

	metadataJSON := []byte("{}")
	if metadata != nil {
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			_ = file.Close()
			return nil, "", fmt.Errorf("encode upload metadata: %w", err)
		}
	}

	mediaContentType := resolveUploadContentType(metadata, uploadPath, explicitContentType)
	if err := validateUploadContentType(mediaContentType); err != nil {
		_ = file.Close()
		return nil, "", err
	}

	reader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	contentType := "multipart/related; boundary=" + writer.Boundary()
	go func() {
		defer func() { _ = file.Close() }()
		fail := func(writeErr error) {
			_ = pipeWriter.CloseWithError(writeErr)
		}
		metadataHeaders := make(textproto.MIMEHeader)
		metadataHeaders.Set("Content-Type", "application/json; charset=UTF-8")
		metadataPart, writeErr := writer.CreatePart(metadataHeaders)
		if writeErr != nil {
			fail(fmt.Errorf("create upload metadata part: %w", writeErr))
			return
		}
		if _, writeErr = metadataPart.Write(metadataJSON); writeErr != nil {
			fail(fmt.Errorf("write upload metadata part: %w", writeErr))
			return
		}
		mediaHeaders := make(textproto.MIMEHeader)
		mediaHeaders.Set("Content-Type", mediaContentType)
		mediaPart, writeErr := writer.CreatePart(mediaHeaders)
		if writeErr != nil {
			fail(fmt.Errorf("create upload media part: %w", writeErr))
			return
		}
		source := io.Reader(file)
		if progressOut != nil {
			source = io.TeeReader(file, newProgressWriter(progressOut, "Uploaded"))
		}
		if _, writeErr = io.Copy(mediaPart, source); writeErr != nil {
			fail(fmt.Errorf("write upload media part: %w", writeErr))
			return
		}
		if writeErr = writer.Close(); writeErr != nil {
			fail(fmt.Errorf("finish multipart upload: %w", writeErr))
			return
		}
		_ = pipeWriter.Close()
	}()
	return reader, contentType, nil
}

func saveResponseFile(path string, source io.Reader, progressOut io.Writer) (written int64, checksum string, resultErr error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return 0, "", err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		if resultErr != nil {
			_ = os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	target := io.Writer(io.MultiWriter(temp, hash))
	if progressOut != nil {
		target = newProgressWriterWithTarget(target, progressOut, "Downloaded")
	}
	written, err = io.Copy(target, source)
	if err != nil {
		return 0, "", err
	}
	if err := temp.Sync(); err != nil {
		return 0, "", err
	}
	if err := temp.Close(); err != nil {
		return 0, "", err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return 0, "", err
	}
	return written, fmt.Sprintf("%x", hash.Sum(nil)), nil
}

type progressWriter struct {
	target io.Writer
	out    io.Writer
	label  string
	total  int64
	next   int64
}

func newProgressWriter(out io.Writer, label string) io.Writer {
	return newProgressWriterWithTarget(io.Discard, out, label)
}

func newProgressWriterWithTarget(target, out io.Writer, label string) io.Writer {
	return &progressWriter{target: target, out: out, label: label, next: 8 << 20}
}

func (writer *progressWriter) Write(data []byte) (int, error) {
	count, err := writer.target.Write(data)
	writer.total += int64(count)
	if writer.total >= writer.next {
		_, _ = fmt.Fprintf(writer.out, "%s %d bytes\n", writer.label, writer.total)
		writer.next = writer.total + (8 << 20)
	}
	return count, err
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

func supportsResumableUpload(method *discovery.Method) bool {
	return method.SupportsMediaUpload && method.MediaUpload != nil && method.MediaUpload.Protocols.Resumable != nil && method.MediaUpload.Protocols.Resumable.Path != ""
}

func buildResumableUploadURL(doc *discovery.Document, method *discovery.Method, params map[string]any) (string, error) {
	if !supportsResumableUpload(method) {
		return "", errors.New("this API method does not support resumable media upload")
	}
	methodCopy := *method
	uploadCopy := *method.MediaUpload
	protocolsCopy := method.MediaUpload.Protocols
	resumableCopy := *protocolsCopy.Resumable
	resumableCopy.Multipart = true
	protocolsCopy.Simple = &resumableCopy
	uploadCopy.Protocols = protocolsCopy
	methodCopy.MediaUpload = &uploadCopy
	requestURL, err := buildURL(doc, &methodCopy, params, true)
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	query.Set("uploadType", "resumable")
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
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
