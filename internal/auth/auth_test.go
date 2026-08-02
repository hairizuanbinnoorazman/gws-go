package auth

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestCallbackHandler(t *testing.T) {
	results := make(chan callbackResult, 1)
	handler := callbackHandler("expected", results)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/?state=expected&code=abc", nil))
	if recorder.Code != 200 {
		t.Fatalf("status = %d", recorder.Code)
	}
	if result := <-results; result.err != nil || result.code != "abc" {
		t.Fatalf("unexpected callback result: %#v", result)
	}
}

func TestCallbackHandlerRejectsState(t *testing.T) {
	results := make(chan callbackResult, 1)
	recorder := httptest.NewRecorder()
	callbackHandler("expected", results).ServeHTTP(recorder, httptest.NewRequest("GET", "/?state=wrong&code=abc", nil))
	if recorder.Code != 400 || (<-results).err == nil {
		t.Fatal("expected invalid state error")
	}
}

func TestParseClient(t *testing.T) {
	data, err := json.Marshal(ClientFile{Installed: &ClientConfig{ClientID: "id", ClientSecret: "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	client, err := parseClient(data)
	if err != nil || client.ClientID != "id" {
		t.Fatalf("client=%#v err=%v", client, err)
	}
	if _, err := parseClient([]byte(`{"installed":{}}`)); err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("expected client id error, got %v", err)
	}
}

func TestParseScopes(t *testing.T) {
	got := ParseScopes(" scope-a,scope-b ,, ")
	if len(got) != 2 || got[0] != "scope-a" || got[1] != "scope-b" {
		t.Fatalf("unexpected scopes: %#v", got)
	}
}

func TestDefaultScopesIncludeReadOnlyGmail(t *testing.T) {
	const gmailReadOnly = "https://www.googleapis.com/auth/gmail.readonly"
	if !slices.Contains(DefaultScopes, gmailReadOnly) {
		t.Fatalf("default scopes do not contain %q: %#v", gmailReadOnly, DefaultScopes)
	}
	for _, scope := range DefaultScopes {
		if strings.HasPrefix(scope, "https://www.googleapis.com/auth/gmail") && scope != gmailReadOnly {
			t.Fatalf("default scopes grant non-read-only Gmail access: %q", scope)
		}
	}
}

func TestDefaultScopesIncludeDrive(t *testing.T) {
	const drive = "https://www.googleapis.com/auth/drive"
	if !slices.Contains(DefaultScopes, drive) {
		t.Fatalf("default scopes do not contain %q: %#v", drive, DefaultScopes)
	}
}

func TestDefaultScopesIncludeSheets(t *testing.T) {
	const sheets = "https://www.googleapis.com/auth/spreadsheets"
	if !slices.Contains(DefaultScopes, sheets) {
		t.Fatalf("default scopes do not contain %q: %#v", sheets, DefaultScopes)
	}
}

func TestGmailWriteScopeIsOptIn(t *testing.T) {
	const (
		readOnly = "https://www.googleapis.com/auth/gmail.readonly"
		modify   = "https://www.googleapis.com/auth/gmail.modify"
		send     = "https://www.googleapis.com/auth/gmail.send"
	)
	standard, err := ScopesForPreset("standard")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(standard, readOnly) || slices.Contains(standard, modify) || slices.Contains(standard, send) {
		t.Fatalf("standard scopes = %#v", standard)
	}
	write, err := ScopesForPreset("gmail-write")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(write, readOnly) || !slices.Contains(write, modify) || !slices.Contains(write, send) {
		t.Fatalf("gmail-write scopes = %#v", write)
	}
}

func TestMapsScopePresetIncludesOnlyPortabilityScopes(t *testing.T) {
	scopes, err := ScopesForPreset("maps")
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range MapsPortabilityScopes {
		if !slices.Contains(scopes, scope) {
			t.Fatalf("maps scopes do not contain %q: %#v", scope, scopes)
		}
	}
	if slices.Contains(scopes, "https://www.googleapis.com/auth/documents") {
		t.Fatalf("maps preset mixes Data Portability and Workspace scopes: %#v", scopes)
	}
}

func TestDefaultScopesIncludePhotosPicker(t *testing.T) {
	const picker = "https://www.googleapis.com/auth/photospicker.mediaitems.readonly"
	if !slices.Contains(DefaultScopes, picker) {
		t.Fatalf("default scopes do not contain %q: %#v", picker, DefaultScopes)
	}
}
