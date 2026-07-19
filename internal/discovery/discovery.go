// Package discovery loads the command schema for supported Google APIs.
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"time"

	appconfig "github.com/hairizuanbinnoorazman/gws-go/internal/config"
)

const cacheTTL = 24 * time.Hour

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Document is the relevant subset of a Google REST Discovery document.
type Document struct {
	Name        string                `json:"name"`
	Version     string                `json:"version"`
	Description string                `json:"description"`
	RootURL     string                `json:"rootUrl"`
	ServicePath string                `json:"servicePath"`
	BaseURL     string                `json:"baseUrl"`
	Resources   map[string]*Resource  `json:"resources"`
	Parameters  map[string]*Parameter `json:"parameters"`
}

// Resource groups API methods and nested resources.
type Resource struct {
	Methods   map[string]*Method   `json:"methods"`
	Resources map[string]*Resource `json:"resources"`
}

// Method describes one callable REST API method.
type Method struct {
	ID             string                `json:"id"`
	Description    string                `json:"description"`
	HTTPMethod     string                `json:"httpMethod"`
	Path           string                `json:"path"`
	FlatPath       string                `json:"flatPath"`
	Parameters     map[string]*Parameter `json:"parameters"`
	ParameterOrder []string              `json:"parameterOrder"`
	Request        *SchemaRef            `json:"request"`
}

// Parameter describes a path or query parameter.
type Parameter struct {
	Location string `json:"location"`
	Required bool   `json:"required"`
	Repeated bool   `json:"repeated"`
}

// SchemaRef identifies a request body schema.
type SchemaRef struct {
	Ref string `json:"$ref"`
}

// Loader fetches and caches Discovery documents.
type Loader struct {
	Client  *http.Client
	BaseURL string
	Now     func() time.Time
}

// Load returns a fresh cached document or fetches it from Google.
func (l Loader) Load(ctx context.Context, service, version string) (*Document, error) {
	if !identifierPattern.MatchString(service) || !identifierPattern.MatchString(version) {
		return nil, errorsNewIdentifier()
	}
	dir, err := appconfig.EnsureDir()
	if err != nil {
		return nil, err
	}
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	cachePath := filepath.Join(cacheDir, service+"_"+version+".json")
	now := time.Now()
	if l.Now != nil {
		now = l.Now()
	}
	if info, statErr := os.Stat(cachePath); statErr == nil && now.Sub(info.ModTime()) < cacheTTL {
		if data, readErr := os.ReadFile(cachePath); readErr == nil {
			var doc Document
			if jsonErr := json.Unmarshal(data, &doc); jsonErr == nil {
				return &doc, nil
			}
		}
	}
	baseURL := l.BaseURL
	if baseURL == "" {
		baseURL = "https://www.googleapis.com/discovery/v1/apis"
	}
	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/%s/%s/rest", baseURL, service, version), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch Discovery document: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch Discovery document: HTTP %d: %s", resp.StatusCode, body)
	}
	var doc Document
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("parse Discovery document: %w", err)
	}
	if err := os.WriteFile(cachePath, body, 0o600); err != nil {
		return nil, fmt.Errorf("cache Discovery document: %w", err)
	}
	return &doc, nil
}

func errorsNewIdentifier() error {
	return fmt.Errorf("service and version may contain only letters, digits, underscore, and hyphen")
}
