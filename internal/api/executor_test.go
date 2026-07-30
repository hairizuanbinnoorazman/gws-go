package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestBuildURL(t *testing.T) {
	doc := &discovery.Document{BaseURL: "https://slides.googleapis.com/", Parameters: map[string]*discovery.Parameter{
		"fields": {},
	}}
	method := &discovery.Method{
		Path:     "v1/presentations/{presentationId}",
		FlatPath: "v1/presentations/{presentationsId}",
		Parameters: map[string]*discovery.Parameter{
			"presentationId": {Location: "path", Required: true},
			"tag":            {Location: "query", Repeated: true},
		},
	}
	got, err := BuildURL(doc, method, map[string]any{
		"presentationId": "deck/with space",
		"tag":            []any{"one", "two"},
		"fields":         "title,slides",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.EscapedPath() != "/v1/presentations/deck%2Fwith%20space" {
		t.Fatalf("unexpected path: %s", parsed.EscapedPath())
	}
	if got := parsed.Query()["tag"]; len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("unexpected repeated query: %#v", got)
	}
}

func TestBuildURLRejectsMissingAndUnknownParameters(t *testing.T) {
	doc := &discovery.Document{BaseURL: "https://example.test/"}
	method := &discovery.Method{Path: "items/{id}", Parameters: map[string]*discovery.Parameter{
		"id": {Location: "path", Required: true},
	}}
	if _, err := BuildURL(doc, method, map[string]any{}); err == nil || !strings.Contains(err.Error(), "id") {
		t.Fatalf("expected missing id error, got %v", err)
	}
	if _, err := BuildURL(doc, method, map[string]any{"id": "ok", "bogus": true}); err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("expected unknown parameter error, got %v", err)
	}
}

func TestBuildURLRejectsUnsafeReservedPath(t *testing.T) {
	doc := &discovery.Document{BaseURL: "https://example.test/"}
	method := &discovery.Method{Path: "v1/{+name}", Parameters: map[string]*discovery.Parameter{
		"name": {Location: "path", Required: true},
	}}
	if _, err := BuildURL(doc, method, map[string]any{"name": "documents/../secret"}); err == nil {
		t.Fatal("expected unsafe reserved path to be rejected")
	}
}

func TestBuildUploadURL(t *testing.T) {
	doc := &discovery.Document{
		RootURL:    "https://www.googleapis.com/",
		BaseURL:    "https://www.googleapis.com/drive/v3/",
		Parameters: map[string]*discovery.Parameter{"fields": {}},
	}
	method := multipartUploadMethod()
	got, err := buildURL(doc, method, map[string]any{"fields": "id,name"}, true)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/upload/drive/v3/files" {
		t.Fatalf("unexpected upload path: %s", parsed.Path)
	}
	if parsed.Query().Get("uploadType") != "multipart" || parsed.Query().Get("fields") != "id,name" {
		t.Fatalf("unexpected upload query: %s", parsed.RawQuery)
	}
}

func TestExecutorUploadsMultipartMedia(t *testing.T) {
	uploadPath := t.TempDir() + "/report.txt"
	if err := os.WriteFile(uploadPath, []byte("hello drive"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/upload/drive/v3/files" || r.URL.Query().Get("uploadType") != "multipart" {
			t.Fatalf("unexpected upload URL: %s", r.URL)
		}
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "multipart/related" {
			t.Fatalf("content type=%q params=%#v err=%v", mediaType, params, err)
		}
		reader := multipart.NewReader(r.Body, params["boundary"])
		metadataPart, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		var metadata map[string]any
		if err := json.NewDecoder(metadataPart).Decode(&metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["name"] != "report.txt" {
			t.Fatalf("unexpected metadata: %#v", metadata)
		}
		mediaPart, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		contents, err := io.ReadAll(mediaPart)
		if err != nil {
			t.Fatal(err)
		}
		if mediaPart.Header.Get("Content-Type") != "text/plain" || string(contents) != "hello drive" {
			t.Fatalf("media content-type=%q contents=%q", mediaPart.Header.Get("Content-Type"), contents)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"file-id","name":"report.txt"}`)),
		}, nil
	})}
	doc := &discovery.Document{RootURL: "https://www.googleapis.com/", BaseURL: "https://www.googleapis.com/drive/v3/"}
	var out strings.Builder
	err := (Executor{Client: client}).Execute(context.Background(), doc, multipartUploadMethod(), Options{
		BodyJSON:          `{"name":"report.txt"}`,
		UploadPath:        uploadPath,
		UploadContentType: "text/plain",
		Out:               &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"file-id"`) {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestExecutorDryRunPreviewsUpload(t *testing.T) {
	uploadPath := t.TempDir() + "/report.txt"
	if err := os.WriteFile(uploadPath, []byte("preview"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	err := (Executor{}).Execute(context.Background(), &discovery.Document{
		RootURL: "https://www.googleapis.com/",
		BaseURL: "https://www.googleapis.com/drive/v3/",
	}, multipartUploadMethod(), Options{
		DryRun:     true,
		UploadPath: uploadPath,
		Out:        &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `uploadType=multipart`) || !strings.Contains(out.String(), `"content_type": "text/plain`) {
		t.Fatalf("unexpected dry-run output: %s", out.String())
	}
}

func TestExecutorRejectsUnsafeUploadContentType(t *testing.T) {
	uploadPath := t.TempDir() + "/report.txt"
	if err := os.WriteFile(uploadPath, []byte("unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := (Executor{}).Execute(context.Background(), &discovery.Document{
		RootURL: "https://www.googleapis.com/",
	}, multipartUploadMethod(), Options{
		DryRun:            true,
		UploadPath:        uploadPath,
		UploadContentType: "text/plain\r\nX-Injected: yes",
	})
	if err == nil || !strings.Contains(err.Error(), "CR or LF") {
		t.Fatalf("expected content-type validation error, got %v", err)
	}
}

func TestExecutorValidatesRequestSchemaBeforeSending(t *testing.T) {
	requested := false
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requested = true
		return nil, errors.New("must not be called")
	})}
	doc := &discovery.Document{
		BaseURL: "https://example.test/",
		Schemas: map[string]*discovery.Schema{
			"CreateItem": {
				Type:     "object",
				Required: []string{"name"},
				Properties: map[string]*discovery.Schema{
					"name": {Type: "string"},
				},
			},
		},
	}
	err := (Executor{Client: client}).Execute(context.Background(), doc, &discovery.Method{
		HTTPMethod: http.MethodPost,
		Path:       "items",
		Request:    &discovery.SchemaRef{Ref: "CreateItem"},
	}, Options{BodyJSON: `{"name":42}`, Out: io.Discard})
	var structured *clierr.Error
	if !errors.As(err, &structured) || structured.Code != "request_validation_failed" {
		t.Fatalf("error = %T %v", err, err)
	}
	if requested {
		t.Fatal("request was sent despite validation failure")
	}
}

func TestExecutorReadsJSONInputsFromStdinAndFiles(t *testing.T) {
	directory := t.TempDir()
	paramsPath := directory + "/params.json"
	if err := os.WriteFile(paramsPath, []byte(`{"itemId":"item-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	doc := &discovery.Document{
		BaseURL: "https://example.test/",
		Schemas: map[string]*discovery.Schema{
			"Item": {
				Type: "object",
				Properties: map[string]*discovery.Schema{
					"name": {Type: "string"},
				},
			},
		},
	}
	method := &discovery.Method{
		HTTPMethod: http.MethodPatch,
		Path:       "items/{itemId}",
		Parameters: map[string]*discovery.Parameter{
			"itemId": {Location: "path", Required: true},
		},
		Request: &discovery.SchemaRef{Ref: "Item"},
	}
	var output strings.Builder
	err := (Executor{}).Execute(context.Background(), doc, method, Options{
		ParamsFile: paramsPath,
		BodyJSON:   "-",
		Input:      strings.NewReader(`{"name":"Updated"}`),
		DryRun:     true,
		Out:        &output,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `/items/item-1`) || !strings.Contains(output.String(), `"Updated"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestExecutorRejectsMultipleStdinInputs(t *testing.T) {
	err := (Executor{}).Execute(context.Background(), &discovery.Document{}, &discovery.Method{}, Options{
		ParamsJSON: "-",
		BodyFile:   "-",
		Input:      strings.NewReader(`{}`),
		DryRun:     true,
	})
	if err == nil || !strings.Contains(err.Error(), "only one JSON input") {
		t.Fatalf("error = %v", err)
	}
}

func multipartUploadMethod() *discovery.Method {
	return &discovery.Method{
		HTTPMethod:          http.MethodPost,
		Path:                "files",
		SupportsMediaUpload: true,
		MediaUpload: &discovery.MediaUpload{Protocols: discovery.MediaUploadProtocols{
			Simple: &discovery.MediaUploadProtocol{Multipart: true, Path: "upload/drive/v3/files"},
		}},
	}
}

func TestExecutorPaginates(t *testing.T) {
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		body := `{"items":[1],"nextPageToken":"next"}`
		if r.URL.Query().Get("pageToken") == "next" {
			body = `{"items":[2]}`
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	doc := &discovery.Document{BaseURL: "https://calendar.example.test/"}
	method := &discovery.Method{HTTPMethod: http.MethodGet, Path: "items"}
	var out strings.Builder
	err := (Executor{Client: client}).Execute(context.Background(), doc, method, Options{
		PageAll: true, PageLimit: 3, Out: &out,
	})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || !strings.Contains(out.String(), `"items"`) {
		t.Fatalf("requests=%d output=%q", requests, out.String())
	}
}

func TestExecutorRetriesWithExponentialBackoffAndRetryAfter(t *testing.T) {
	requests := 0
	var delays []time.Duration
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requests++
		status := http.StatusServiceUnavailable
		headers := make(http.Header)
		if requests == 2 {
			headers.Set("Retry-After", "3")
		}
		body := `{"error":{"message":"try again"}}`
		if requests == 3 {
			status = http.StatusOK
			body = `{"ok":true}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     headers,
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	executor := Executor{
		Client: client,
		Sleep: func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
	}
	err := executor.Execute(context.Background(),
		&discovery.Document{BaseURL: "https://example.test/"},
		&discovery.Method{HTTPMethod: http.MethodGet, Path: "items"},
		Options{MaxRetries: 2, RetryDelay: time.Second, Out: io.Discard},
	)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
	if len(delays) != 2 || delays[0] != time.Second || delays[1] != 3*time.Second {
		t.Fatalf("delays = %#v", delays)
	}
}

func TestExecutorReturnsStructuredAPIErrorAfterRetries(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"quota exhausted"}}`)),
		}, nil
	})}
	err := (Executor{Client: client}).Execute(context.Background(),
		&discovery.Document{BaseURL: "https://example.test/"},
		&discovery.Method{HTTPMethod: http.MethodGet, Path: "items"},
		Options{MaxRetries: 1, Out: io.Discard},
	)
	var structured *clierr.Error
	if !errors.As(err, &structured) {
		t.Fatalf("expected structured error, got %T: %v", err, err)
	}
	if structured.Status != http.StatusTooManyRequests || structured.Attempts != 2 || !structured.Retryable {
		t.Fatalf("unexpected structured error: %#v", structured)
	}
	if clierr.ExitCode(err) != clierr.ExitAPI || !strings.Contains(err.Error(), "quota exhausted") {
		t.Fatalf("exit=%d error=%v", clierr.ExitCode(err), err)
	}
}

func TestExecutorRequestTimeout(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	err := (Executor{Client: client}).Execute(context.Background(),
		&discovery.Document{BaseURL: "https://example.test/"},
		&discovery.Method{HTTPMethod: http.MethodGet, Path: "items"},
		Options{RequestTimeout: time.Millisecond, Out: io.Discard},
	)
	if clierr.ExitCode(err) != clierr.ExitTimeout {
		t.Fatalf("exit=%d error=%v", clierr.ExitCode(err), err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
