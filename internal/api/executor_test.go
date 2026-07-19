package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}
