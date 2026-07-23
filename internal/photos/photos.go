// Package photos implements the interactive Google Photos Picker flow.
package photos

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
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://photospicker.googleapis.com/v1"

// Options controls an interactive picker and download operation.
type Options struct {
	OutputDir string
	MaxItems  int
	Out       io.Writer
	OpenURL   func(string) error
	BaseURL   string
}

type session struct {
	ID            string        `json:"id"`
	PickerURI     string        `json:"pickerUri"`
	MediaItemsSet bool          `json:"mediaItemsSet"`
	PollingConfig pollingConfig `json:"pollingConfig"`
}

type pollingConfig struct {
	PollInterval string `json:"pollInterval"`
	TimeoutIn    string `json:"timeoutIn"`
}

type mediaPage struct {
	MediaItems    []mediaItem `json:"mediaItems"`
	NextPageToken string      `json:"nextPageToken"`
}

type mediaItem struct {
	ID         string    `json:"id"`
	CreateTime string    `json:"createTime"`
	Type       string    `json:"type"`
	MediaFile  mediaFile `json:"mediaFile"`
}

type mediaFile struct {
	BaseURL  string `json:"baseUrl"`
	MIMEType string `json:"mimeType"`
	Filename string `json:"filename"`
}

// Download opens a Picker session, waits for the user to finish selecting, and
// downloads every selected photo or video.
func Download(ctx context.Context, client *http.Client, opts Options) error {
	if client == nil {
		return errors.New("HTTP client is required")
	}
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.OutputDir == "" {
		opts.OutputDir = "google-photos"
	}
	if opts.MaxItems < 0 || opts.MaxItems > 2000 {
		return errors.New("--max-items must be between 0 and 2000")
	}
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	current, err := createSession(ctx, client, baseURL, opts.MaxItems)
	if err != nil {
		return err
	}
	if current.ID == "" || current.PickerURI == "" {
		return errors.New("the Google Photos Picker returned an incomplete session")
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = deleteSession(cleanupCtx, client, baseURL, current.ID)
	}()

	if _, err := fmt.Fprintf(opts.Out, "Select photos or videos in Google Photos:\n\n%s\n\nWaiting for your selection...\n", current.PickerURI); err != nil {
		return err
	}
	if opts.OpenURL != nil {
		if err := opts.OpenURL(current.PickerURI); err != nil {
			if _, writeErr := fmt.Fprintf(opts.Out, "Could not open a browser automatically: %v\n", err); writeErr != nil {
				return writeErr
			}
		}
	}

	current, err = waitForSelection(ctx, client, baseURL, current)
	if err != nil {
		return err
	}
	items, err := listMedia(ctx, client, baseURL, current.ID)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		_, err = fmt.Fprintln(opts.Out, "No media items were selected.")
		return err
	}
	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	for _, item := range items {
		path, err := downloadItem(ctx, client, opts.OutputDir, item)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(opts.Out, "Downloaded %s\n", path); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(opts.Out, "Downloaded %d item(s) to %s\n", len(items), opts.OutputDir)
	return err
}

func createSession(ctx context.Context, client *http.Client, baseURL string, maxItems int) (session, error) {
	body := []byte("{}")
	if maxItems > 0 {
		var err error
		body, err = json.Marshal(map[string]any{
			"pickingConfig": map[string]string{"maxItemCount": strconv.Itoa(maxItems)},
		})
		if err != nil {
			return session{}, err
		}
	}
	var result session
	err := doJSON(ctx, client, http.MethodPost, baseURL+"/sessions", bytes.NewReader(body), &result)
	return result, err
}

func waitForSelection(ctx context.Context, client *http.Client, baseURL string, current session) (session, error) {
	for !current.MediaItemsSet {
		interval := parseDuration(current.PollingConfig.PollInterval, 2*time.Second)
		timeout := parseDuration(current.PollingConfig.TimeoutIn, time.Minute)
		if timeout <= 0 {
			return session{}, errors.New("the Google Photos Picker session timed out")
		}
		if interval > timeout {
			interval = timeout
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return session{}, fmt.Errorf("wait for Google Photos selection: %w", ctx.Err())
		case <-timer.C:
		}
		var next session
		if err := doJSON(ctx, client, http.MethodGet, baseURL+"/sessions/"+url.PathEscape(current.ID), nil, &next); err != nil {
			return session{}, err
		}
		current = next
	}
	return current, nil
}

func listMedia(ctx context.Context, client *http.Client, baseURL, sessionID string) ([]mediaItem, error) {
	var items []mediaItem
	pageToken := ""
	for {
		query := url.Values{"sessionId": {sessionID}, "pageSize": {"100"}}
		if pageToken != "" {
			query.Set("pageToken", pageToken)
		}
		var page mediaPage
		if err := doJSON(ctx, client, http.MethodGet, baseURL+"/mediaItems?"+query.Encode(), nil, &page); err != nil {
			return nil, err
		}
		items = append(items, page.MediaItems...)
		if page.NextPageToken == "" {
			return items, nil
		}
		pageToken = page.NextPageToken
	}
}

func deleteSession(ctx context.Context, client *http.Client, baseURL, sessionID string) error {
	return doJSON(ctx, client, http.MethodDelete, baseURL+"/sessions/"+url.PathEscape(sessionID), nil, nil)
}

func downloadItem(ctx context.Context, client *http.Client, outputDir string, item mediaItem) (string, error) {
	if item.MediaFile.BaseURL == "" {
		return "", fmt.Errorf("media item %q has no download URL", item.ID)
	}
	suffix := "=d"
	if item.Type == "VIDEO" {
		suffix = "=dv"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.MediaFile.BaseURL+suffix, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %q: %w", item.MediaFile.Filename, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", fmt.Errorf("download %q: HTTP %d: %s", item.MediaFile.Filename, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	file, path, err := createUniqueFile(outputDir, item.MediaFile.Filename, item.ID)
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := io.Copy(file, resp.Body); err != nil {
		return "", fmt.Errorf("save %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("save %q: %w", path, err)
	}
	keep = true
	return path, nil
}

func createUniqueFile(dir, suppliedName, fallback string) (*os.File, string, error) {
	name := filepath.Base(suppliedName)
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = fallback
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 || r == '/' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for number := 1; ; number++ {
		candidate := name
		if number > 1 {
			candidate = fmt.Sprintf("%s-%d%s", stem, number, extension)
		}
		path := filepath.Join(dir, candidate)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return file, path, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("create output file: %w", err)
		}
	}
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
		return fmt.Errorf("the Google Photos Picker request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("the Google Photos Picker returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if target == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode Google Photos Picker response: %w", err)
	}
	return nil
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return duration
}
