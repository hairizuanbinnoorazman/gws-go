package maps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportCreatesWaitsAndDownloadsArchive(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := ""
		switch r.URL.Path {
		case "/v1/portabilityArchive:initiate":
			if r.Method != http.MethodPost {
				t.Fatalf("initiate method = %s", r.Method)
			}
			var request map[string]any
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatal(err)
			}
			resources, _ := request["resources"].([]any)
			if len(resources) != 1 || resources[0] != "myactivity.maps" || request["startTime"] != "2026-08-02T00:00:00+08:00" {
				t.Fatalf("request = %#v", request)
			}
			body = `{"archiveJobId":"job-1","accessType":"ACCESS_TYPE_ONE_TIME"}`
		case "/v1/archiveJobs/job-1/portabilityArchiveState":
			body = `{"state":"COMPLETE","urls":["https://download.example/maps-export.zip?signature=secret"]}`
		case "/maps-export.zip":
			body = "archive contents"
		default:
			return nil, fmt.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    r,
		}, nil
	})}

	outputDir := t.TempDir()
	var output strings.Builder
	err := Export(context.Background(), client, Options{
		Resources:      []string{"myactivity.maps"},
		StartTime:      "2026-08-02T00:00:00+08:00",
		EndTime:        "2026-08-03T00:00:00+08:00",
		OutputDir:      outputDir,
		Out:            &output,
		BaseURL:        "https://portability.example/v1",
		PollInterval:   time.Millisecond,
		DownloadClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(outputDir, "maps-export.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "archive contents" || !strings.Contains(output.String(), "Downloaded 1 Maps archive") {
		t.Fatalf("data=%q output=%q", data, output.String())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestExportRejectsUnsupportedResourceAndInvalidTimeFilter(t *testing.T) {
	client := &http.Client{}
	if err := Export(context.Background(), client, Options{Resources: []string{"maps.timeline"}}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported resource error, got %v", err)
	}
	if err := Export(context.Background(), client, Options{
		Resources: []string{"maps.starred_places"},
		StartTime: time.Now().Format(time.RFC3339),
	}); err == nil || !strings.Contains(err.Error(), "time filters") {
		t.Fatalf("expected time-filter error, got %v", err)
	}
}
