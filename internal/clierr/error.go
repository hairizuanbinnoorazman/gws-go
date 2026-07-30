// Package clierr defines errors that can be rendered consistently by the CLI.
package clierr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Process exit codes. Keep these stable so scripts can depend on them.
const (
	ExitGeneral    = 1
	ExitInput      = 2
	ExitAuth       = 3
	ExitAPI        = 4
	ExitNetwork    = 5
	ExitTimeout    = 6
	ExitFilesystem = 7
)

// Error is a machine-readable CLI failure.
type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Status      int    `json:"http_status,omitempty"`
	Retryable   bool   `json:"retryable,omitempty"`
	Attempts    int    `json:"attempts,omitempty"`
	ProcessCode int    `json:"exit_code"`
	Details     any    `json:"details,omitempty"`
	cause       error
}

// New constructs a structured error.
func New(code, message string, processCode int, cause error) *Error {
	return &Error{Code: code, Message: message, ProcessCode: processCode, cause: cause}
}

func (e *Error) Error() string {
	if e.cause == nil {
		return e.Message
	}
	return fmt.Sprintf("%s: %v", e.Message, e.cause)
}

// Unwrap exposes the underlying error to errors.Is and errors.As.
func (e *Error) Unwrap() error { return e.cause }

// ExitCode returns the stable process exit code for err.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var structured *Error
	if errors.As(err, &structured) && structured.ProcessCode > 0 {
		return structured.ProcessCode
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ExitTimeout
	}
	return ExitGeneral
}

// Render writes an error in either human-readable text or JSON.
func Render(w io.Writer, err error, format string) error {
	if format != "json" {
		_, writeErr := fmt.Fprintln(w, "Error:", err)
		return writeErr
	}
	payload := errorPayload(err)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(payload)
}

func errorPayload(err error) *Error {
	var structured *Error
	if errors.As(err, &structured) {
		payload := *structured
		if payload.Details == nil && payload.cause != nil {
			payload.Details = payload.cause.Error()
		}
		payload.cause = nil
		return &payload
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return New("timeout", err.Error(), ExitTimeout, nil)
	}
	return New("internal_error", err.Error(), ExitGeneral, nil)
}
