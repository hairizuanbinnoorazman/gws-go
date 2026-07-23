// Package auth implements installed-app OAuth and token persistence.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	appconfig "github.com/hairizuanbinnoorazman/gws-go/internal/config"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// TokenEnv supplies a pre-obtained access token when set.
const TokenEnv = "GWS_GO_TOKEN"

// DefaultScopes grants access to the supported Workspace APIs. Gmail access is
// intentionally read-only.
var DefaultScopes = []string{
	"https://www.googleapis.com/auth/documents",
	"https://www.googleapis.com/auth/calendar",
	"https://www.googleapis.com/auth/presentations",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/drive",
}

// ClientFile models the OAuth client JSON downloaded from Google Cloud.
type ClientFile struct {
	Installed *ClientConfig `json:"installed"`
	Web       *ClientConfig `json:"web"`
}

// ClientConfig contains an installed application's OAuth endpoints and ID.
type ClientConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	AuthURI      string   `json:"auth_uri"`
	TokenURI     string   `json:"token_uri"`
	RedirectURIs []string `json:"redirect_uris"`
}

// LoginOptions controls the installed-app OAuth flow.
type LoginOptions struct {
	ClientSecretFile string
	NoBrowser        bool
	Timeout          time.Duration
	Scopes           []string
	Out              io.Writer
}

// Login obtains and persists an offline OAuth refresh token.
func Login(ctx context.Context, opts LoginOptions) error {
	if opts.Out == nil {
		opts.Out = os.Stdout
	}
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	if len(opts.Scopes) == 0 {
		opts.Scopes = DefaultScopes
	}
	client, err := loadAndSaveClient(opts.ClientSecretFile)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start OAuth callback server: %w", err)
	}
	defer func() { _ = listener.Close() }()
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port)
	oauthConfig := oauthConfig(client, redirectURL, opts.Scopes)

	state, err := randomURLSafe(32)
	if err != nil {
		return err
	}
	verifier, err := randomURLSafe(48)
	if err != nil {
		return err
	}
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	authURL := oauthConfig.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce,
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	if _, err := fmt.Fprintf(opts.Out, "Open this URL to authorize gws-go:\n\n%s\n\n", authURL); err != nil {
		return err
	}
	if !opts.NoBrowser {
		if err := openBrowser(authURL); err != nil {
			if _, writeErr := fmt.Fprintf(opts.Out, "Could not open a browser automatically: %v\n", err); writeErr != nil {
				return writeErr
			}
		}
	}

	result := make(chan callbackResult, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second}
	server.Handler = callbackHandler(state, result)
	go func() {
		if serveErr := server.Serve(listener); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			result <- callbackResult{err: serveErr}
		}
	}()

	waitCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	var code string
	select {
	case <-waitCtx.Done():
		return fmt.Errorf("OAuth login timed out: %w", waitCtx.Err())
	case callback := <-result:
		if callback.err != nil {
			return callback.err
		}
		code = callback.code
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)

	token, err := oauthConfig.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return fmt.Errorf("exchange authorization code: %w", err)
	}
	if token.RefreshToken == "" {
		return errors.New("google did not return a refresh token; revoke the app grant and retry auth login")
	}
	if err := SaveToken(token); err != nil {
		return err
	}
	_, err = fmt.Fprintln(opts.Out, "Authentication successful. Offline refresh token saved.")
	return err
}

type callbackResult struct {
	code string
	err  error
}

func callbackHandler(expectedState string, result chan<- callbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
			http.Error(w, "Authorization was denied. You may close this window.", http.StatusBadRequest)
			result <- callbackResult{err: fmt.Errorf("oauth authorization failed: %s", oauthErr)}
			return
		}
		if r.URL.Query().Get("state") != expectedState {
			http.Error(w, "Invalid OAuth state.", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("invalid OAuth state in callback")}
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "Missing authorization code.", http.StatusBadRequest)
			result <- callbackResult{err: errors.New("OAuth callback did not contain a code")}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<h1>Authentication successful</h1><p>You may close this window.</p>")
		result <- callbackResult{code: code}
	})
}

// HTTPClient returns an authenticated client, refreshing saved tokens as needed.
func HTTPClient(ctx context.Context) (*http.Client, error) {
	if token := os.Getenv(TokenEnv); token != "" {
		return oauth2.NewClient(ctx, oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})), nil
	}
	client, err := loadClient()
	if err != nil {
		return nil, fmt.Errorf("not authenticated; run 'gws-go auth login': %w", err)
	}
	token, err := LoadToken()
	if err != nil {
		return nil, fmt.Errorf("not authenticated; run 'gws-go auth login': %w", err)
	}
	config := oauthConfig(client, "http://127.0.0.1", DefaultScopes)
	current, err := config.TokenSource(ctx, token).Token()
	if err != nil {
		return nil, fmt.Errorf("refresh OAuth token: %w", err)
	}
	if err := SaveToken(current); err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(current)), nil
}

// Status reports which local authentication source is available.
func Status() (string, error) {
	if os.Getenv(TokenEnv) != "" {
		return "authenticated via " + TokenEnv, nil
	}
	token, err := LoadToken()
	if err != nil {
		return "not authenticated", err
	}
	if token.RefreshToken != "" {
		return "authenticated (offline refresh token available)", nil
	}
	return "authenticated (access token only)", nil
}

// Logout removes the locally saved OAuth token.
func Logout() error {
	path, err := tokenPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// SaveToken atomically persists an OAuth token with owner-only permissions.
func SaveToken(token *oauth2.Token) error {
	dir, err := appconfig.EnsureDir()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "token.json"), append(data, '\n'), 0o600)
}

// LoadToken reads the locally persisted OAuth token.
func LoadToken() (*oauth2.Token, error) {
	path, err := tokenPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var token oauth2.Token
	if err := json.Unmarshal(data, &token); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &token, nil
}

func loadAndSaveClient(source string) (*ClientConfig, error) {
	if source == "" {
		return loadClient()
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read OAuth client file: %w", err)
	}
	client, err := parseClient(data)
	if err != nil {
		return nil, err
	}
	dir, err := appconfig.EnsureDir()
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(filepath.Join(dir, "client_secret.json"), data, 0o600); err != nil {
		return nil, err
	}
	return client, nil
}

func loadClient() (*ClientConfig, error) {
	dir, err := appconfig.Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "client_secret.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return parseClient(data)
}

func parseClient(data []byte) (*ClientConfig, error) {
	var file ClientFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("parse OAuth client JSON: %w", err)
	}
	client := file.Installed
	if client == nil {
		client = file.Web
	}
	if client == nil || client.ClientID == "" || client.ClientSecret == "" {
		return nil, errors.New("OAuth client JSON must contain installed.client_id and installed.client_secret")
	}
	return client, nil
}

func oauthConfig(client *ClientConfig, redirectURL string, scopes []string) *oauth2.Config {
	endpoint := google.Endpoint
	if client.AuthURI != "" {
		endpoint.AuthURL = client.AuthURI
	}
	if client.TokenURI != "" {
		endpoint.TokenURL = client.TokenURI
	}
	return &oauth2.Config{ClientID: client.ClientID, ClientSecret: client.ClientSecret, Endpoint: endpoint, RedirectURL: redirectURL, Scopes: scopes}
}

func randomURLSafe(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate OAuth random value: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func openBrowser(url string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{url}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		name, args = "xdg-open", []string{url}
	}
	return exec.Command(name, args...).Start()
}

func tokenPath() (string, error) {
	dir, err := appconfig.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token.json"), nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".gws-go-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ParseScopes parses comma-separated scopes or returns the defaults.
func ParseScopes(value string) []string {
	if strings.TrimSpace(value) == "" {
		return append([]string(nil), DefaultScopes...)
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if scope := strings.TrimSpace(part); scope != "" {
			result = append(result, scope)
		}
	}
	return result
}
