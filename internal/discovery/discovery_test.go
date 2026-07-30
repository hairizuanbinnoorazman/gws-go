package discovery

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
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
				`{"name":"drive","version":"v3","rootUrl":"https://www.googleapis.com/","resources":{"files":{"methods":{"create":{"httpMethod":"POST","path":"files","supportsMediaUpload":true,"mediaUpload":{"protocols":{"simple":{"multipart":true,"path":"upload/drive/v3/files"}}}}}}}}`,
			)),
		}, nil
	})}
	loader := Loader{Client: client, BaseURL: "https://discovery.example.test"}
	for range 2 {
		doc, err := loader.Load(context.Background(), "drive", "v3")
		if err != nil || doc.Name != "drive" {
			t.Fatalf("doc=%#v err=%v", doc, err)
		}
		method := doc.Resources["files"].Methods["create"]
		if !method.SupportsMediaUpload || method.MediaUpload.Protocols.Simple.Path != "upload/drive/v3/files" {
			t.Fatalf("upload metadata was not decoded: %#v", method)
		}
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	dir, _ := appconfig.Dir()
	if info, err := os.Stat(filepath.Join(dir, "cache", "drive_v3.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache info=%v err=%v", info, err)
	}
}

func TestLoaderClassifiesNetworkErrors(t *testing.T) {
	t.Setenv(appconfig.DirEnv, t.TempDir())
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	_, err := (Loader{Client: client, BaseURL: "https://discovery.example.test"}).Load(context.Background(), "drive", "v3")
	if clierr.ExitCode(err) != clierr.ExitNetwork {
		t.Fatalf("exit=%d error=%v", clierr.ExitCode(err), err)
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
