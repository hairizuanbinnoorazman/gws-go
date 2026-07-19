package discovery

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appconfig "github.com/hairizuanbinnoorazman/gws-go/internal/config"
)

func TestLoaderFetchesThenCaches(t *testing.T) {
	t.Setenv(appconfig.DirEnv, t.TempDir())
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"name":"docs","version":"v1","rootUrl":"https://docs.googleapis.com/","resources":{}}`,
			)),
		}, nil
	})}
	loader := Loader{Client: client, BaseURL: "https://discovery.example.test"}
	for range 2 {
		doc, err := loader.Load(context.Background(), "docs", "v1")
		if err != nil || doc.Name != "docs" {
			t.Fatalf("doc=%#v err=%v", doc, err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	dir, _ := appconfig.Dir()
	if info, err := os.Stat(filepath.Join(dir, "cache", "docs_v1.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache info=%v err=%v", info, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestLoaderRejectsUnsafeIdentifier(t *testing.T) {
	t.Setenv(appconfig.DirEnv, t.TempDir())
	if _, err := (Loader{}).Load(context.Background(), "../docs", "v1"); err == nil {
		t.Fatal("expected identifier validation error")
	}
}
