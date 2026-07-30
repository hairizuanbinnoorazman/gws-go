// Package discovery loads the command schema for supported Google APIs.
package discovery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
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
	Schemas     map[string]*Schema    `json:"schemas"`
}

// Resource groups API methods and nested resources.
type Resource struct {
	Methods   map[string]*Method   `json:"methods"`
	Resources map[string]*Resource `json:"resources"`
}

// Method describes one callable REST API method.
type Method struct {
	ID                  string                `json:"id"`
	Description         string                `json:"description"`
	HTTPMethod          string                `json:"httpMethod"`
	Path                string                `json:"path"`
	FlatPath            string                `json:"flatPath"`
	Parameters          map[string]*Parameter `json:"parameters"`
	ParameterOrder      []string              `json:"parameterOrder"`
	Request             *SchemaRef            `json:"request"`
	SupportsMediaUpload bool                  `json:"supportsMediaUpload"`
	MediaUpload         *MediaUpload          `json:"mediaUpload"`
}

// MediaUpload describes the upload protocols exposed by a discovered method.
type MediaUpload struct {
	Accept    []string             `json:"accept"`
	Protocols MediaUploadProtocols `json:"protocols"`
}

// MediaUploadProtocols contains the available Google media upload protocols.
type MediaUploadProtocols struct {
	Simple    *MediaUploadProtocol `json:"simple"`
	Resumable *MediaUploadProtocol `json:"resumable"`
}

// MediaUploadProtocol describes one upload endpoint.
type MediaUploadProtocol struct {
	Multipart bool   `json:"multipart"`
	Path      string `json:"path"`
}

// Parameter describes a path or query parameter.
type Parameter struct {
	Location         string   `json:"location"`
	Required         bool     `json:"required"`
	Repeated         bool     `json:"repeated"`
	Type             string   `json:"type"`
	Format           string   `json:"format"`
	Description      string   `json:"description"`
	Enum             []string `json:"enum"`
	EnumDescriptions []string `json:"enumDescriptions"`
	Default          any      `json:"default"`
}

// SchemaRef identifies a request body schema.
type SchemaRef struct {
	Ref string `json:"$ref"`
}

// Schema describes a JSON value in a Discovery document.
type Schema struct {
	Ref                  string             `json:"$ref,omitempty"`
	ID                   string             `json:"id,omitempty"`
	Type                 string             `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Required             any                `json:"required,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	AdditionalProperties *Schema            `json:"additionalProperties,omitempty"`
	Enum                 []any              `json:"enum,omitempty"`
	Default              any                `json:"default,omitempty"`
	Example              any                `json:"example,omitempty"`
	ReadOnly             bool               `json:"readOnly,omitempty"`
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, clierr.New("discovery_timeout", "fetch Discovery document", clierr.ExitTimeout, err)
		}
		return nil, clierr.New("network_error", "fetch Discovery document", clierr.ExitNetwork, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		fetchErr := clierr.New("discovery_error", fmt.Sprintf("fetch Discovery document: HTTP %d", resp.StatusCode), clierr.ExitAPI, nil)
		fetchErr.Status = resp.StatusCode
		fetchErr.Details = strings.TrimSpace(string(body))
		return nil, fetchErr
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
