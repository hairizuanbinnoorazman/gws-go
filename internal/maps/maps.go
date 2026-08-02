// Package maps exports the personal Google Maps data made available through
// Google's Data Portability API.
package maps

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
	"path/filepath"
	"strings"
	"time"
)

const defaultBaseURL = "https://dataportability.googleapis.com/v1"

// Resources lists every Maps-related Data Portability resource group.
var Resources = map[string]string{
	"maps.aliased_places":         "labeled places",
	"maps.commute_routes":         "pinned commute trips and routes",
	"maps.commute_settings":       "commute settings",
	"maps.ev_profile":             "electric vehicle profile",
	"maps.factual_contributions":  "corrections made to map information",
	"maps.offering_contributions": "updates and contributions made to places",
	"maps.photos_videos":          "photos and videos posted to Maps",
	"maps.questions_answers":      "questions and answers posted to Maps",
	"maps.reviews":                "reviews and posts",
	"maps.starred_places":         "Starred places",
	"myactivity.maps":             "Maps activity, such as searches and directions",
	"mymaps.maps":                 "maps created with My Maps",
}

// Options controls creation and download of a Maps portability archive.
type Options struct {
	Resources    []string
	StartTime    string
	EndTime      string
	OutputDir    string
	Out          io.Writer
	BaseURL      string
	PollInterval time.Duration
	// DownloadClient fetches Google's signed archive URLs. It intentionally
	// defaults to an unauthenticated client so OAuth credentials are never sent
	// to the storage host.
	DownloadClient *http.Client
}

type initiateResponse struct {
	ArchiveJobID string `json:"archiveJobId"`
	AccessType   string `json:"accessType"`
}

type archiveState struct {
	State      string   `json:"state"`
	URLs       []string `json:"urls"`
	Name       string   `json:"name"`
	StartTime  string   `json:"startTime"`
	ExportTime string   `json:"exportTime"`
}

// Export starts an archive, waits for it to complete, and downloads each
// archive object returned by Google.
func Export(ctx context.Context, client *http.Client, opts Options) error {
	if client == nil {
		return errors.New("HTTP client is required")
	}
	if len(opts.Resources) == 0 {
		opts.Resources = []string{"myactivity.maps"}
	}
	for _, resource := range opts.Resources {
		if _, ok := Resources[resource]; !ok {
			return fmt.Errorf("unsupported Maps resource %q", resource)
		}
	}
	if (opts.StartTime != "" || opts.EndTime != "") &&
		(len(opts.Resources) != 1 || opts.Resources[0] != "myactivity.maps") {
		return errors.New("time filters are supported only when exporting myactivity.maps")
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "google-maps"
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 2 * time.Second
	}
	if opts.DownloadClient == nil {
		opts.DownloadClient = http.DefaultClient
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	request := map[string]any{"resources": opts.Resources}
	if opts.StartTime != "" {
		request["startTime"] = opts.StartTime
	}
	if opts.EndTime != "" {
		request["endTime"] = opts.EndTime
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	var initiated initiateResponse
	if err := doJSON(ctx, client, http.MethodPost, baseURL+"/portabilityArchive:initiate", bytes.NewReader(body), &initiated); err != nil {
		return err
	}
	if initiated.ArchiveJobID == "" {
		return errors.New("the Data Portability API returned an empty archive job ID")
	}
	if _, err := fmt.Fprintf(opts.Out, "Maps export job %s started (%s).\n", initiated.ArchiveJobID, initiated.AccessType); err != nil {
		return err
	}

	stateURL := baseURL + "/archiveJobs/" + url.PathEscape(initiated.ArchiveJobID) + "/portabilityArchiveState"
	var current archiveState
	for {
		if err := doJSON(ctx, client, http.MethodGet, stateURL, nil, &current); err != nil {
			return err
		}
		switch current.State {
		case "COMPLETE":
			if len(current.URLs) == 0 {
				return errors.New("the completed Maps export did not contain any download URLs")
			}
			if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
				return fmt.Errorf("create output directory: %w", err)
			}
			for index, downloadURL := range current.URLs {
				path, err := download(ctx, opts.DownloadClient, opts.OutputDir, downloadURL, index+1)
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(opts.Out, "Downloaded %s\n", path); err != nil {
					return err
				}
			}
			_, err := fmt.Fprintf(opts.Out, "Downloaded %d Maps archive file(s) to %s\n", len(current.URLs), opts.OutputDir)
			return err
		case "FAILED", "CANCELLED":
			return fmt.Errorf("maps export job ended with state %s", current.State)
		case "IN_PROGRESS", "STATE_UNSPECIFIED", "":
		default:
			return fmt.Errorf("maps export job returned unknown state %q", current.State)
		}
		timer := time.NewTimer(opts.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for Maps export: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func download(ctx context.Context, client *http.Client, outputDir, downloadURL string, number int) (string, error) {
	parsed, err := url.Parse(downloadURL)
	if err != nil {
		return "", fmt.Errorf("parse Maps archive URL: %w", err)
	}
	name := filepath.Base(parsed.Path)
	if name == "" || name == "." || name == "/" {
		name = fmt.Sprintf("google-maps-%d.zip", number)
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	path := filepath.Join(outputDir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create Maps archive file: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download Maps archive: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("download Maps archive: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", fmt.Errorf("save Maps archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("save Maps archive: %w", err)
	}
	keep = true
	return path, nil
}

func doJSON(ctx context.Context, client *http.Client, method, requestURL string, body io.Reader, target any) error {
	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("the Data Portability API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("the Data Portability API returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode Data Portability API response: %w", err)
	}
	return nil
}
