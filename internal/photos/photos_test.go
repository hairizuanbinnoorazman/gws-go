package photos

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDownloadPickerFlow(t *testing.T) {
	var polls atomic.Int32
	var deleted atomic.Bool
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := ""
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sessions":
			body = `{"id":"session-1","pickerUri":"https://picker.example/select","pollingConfig":{"pollInterval":"1ns","timeoutIn":"10s"}}`
		case r.Method == http.MethodGet && r.URL.Path == "/v1/sessions/session-1":
			polls.Add(1)
			body = `{"id":"session-1","mediaItemsSet":true}`
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mediaItems":
			if r.URL.Query().Get("sessionId") != "session-1" {
				t.Errorf("sessionId = %q", r.URL.Query().Get("sessionId"))
			}
			body = `{"mediaItems":[{"id":"photo-1","type":"PHOTO","mediaFile":{"filename":"day.jpg","baseUrl":"https://media.example/photo"}}]}`
		case r.Method == http.MethodGet && r.URL.Path == "/photo=d":
			body = "image bytes"
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/sessions/session-1":
			deleted.Store(true)
			status = http.StatusNoContent
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}

	outputDir := t.TempDir()
	var output bytes.Buffer
	var opened string
	err := Download(context.Background(), client, Options{
		BaseURL:   "https://picker.example/v1",
		OutputDir: outputDir,
		Out:       &output,
		OpenURL: func(uri string) error {
			opened = uri
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if opened != "https://picker.example/select" {
		t.Fatalf("opened URI = %q", opened)
	}
	if polls.Load() != 1 || !deleted.Load() {
		t.Fatalf("polls=%d deleted=%v", polls.Load(), deleted.Load())
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "day.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "image bytes" {
		t.Fatalf("downloaded data = %q", data)
	}
	if !strings.Contains(output.String(), "Downloaded 1 item(s)") {
		t.Fatalf("output = %q", output.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCreateUniqueFileAvoidsTraversalAndOverwrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "photo.jpg"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, path, err := createUniqueFile(dir, "../photo.jpg", "fallback")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if path != filepath.Join(dir, "photo-2.jpg") {
		t.Fatalf("path = %q", path)
	}
}

func TestMaxItemsValidation(t *testing.T) {
	err := Download(context.Background(), http.DefaultClient, Options{MaxItems: 2001})
	if err == nil || !strings.Contains(err.Error(), "--max-items") {
		t.Fatalf("err = %v", err)
	}
}
