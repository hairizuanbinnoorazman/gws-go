package auth

import (
	"encoding/json"
	"net/http/httptest"
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
