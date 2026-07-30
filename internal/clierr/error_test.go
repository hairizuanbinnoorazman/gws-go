package clierr

import (
	"errors"
	"strings"
	"testing"
)

func TestRenderJSON(t *testing.T) {
	err := New("google_api_error", "quota exhausted", ExitAPI, errors.New("cause"))
	err.Status = 429
	err.Retryable = true
	err.Attempts = 5
	var output strings.Builder
	if renderErr := Render(&output, err, "json"); renderErr != nil {
		t.Fatal(renderErr)
	}
	got := output.String()
	for _, expected := range []string{
		`"code":"google_api_error"`,
		`"http_status":429`,
		`"retryable":true`,
		`"attempts":5`,
		`"exit_code":4`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("output %q does not contain %q", got, expected)
		}
	}
}
