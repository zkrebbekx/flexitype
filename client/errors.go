package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// ErrorCode is a stable, machine-readable failure code the API returns.
type ErrorCode string

// The error codes the service emits.
const (
	CodeValidation          ErrorCode = "VALIDATION"
	CodeNotFound            ErrorCode = "NOT_FOUND"
	CodeConflict            ErrorCode = "CONFLICT"
	CodeArchived            ErrorCode = "ARCHIVED"
	CodeDependencyViolation ErrorCode = "DEPENDENCY_VIOLATION"
	CodeUnauthenticated     ErrorCode = "UNAUTHENTICATED"
	CodeForbidden           ErrorCode = "FORBIDDEN"
	CodeRateLimited         ErrorCode = "RATE_LIMITED"
	CodeInternal            ErrorCode = "INTERNAL"
)

// Sentinels for errors.Is. Each matches an APIError with the corresponding
// code, so callers can branch without unwrapping:
//
//	if errors.Is(err, client.ErrNotFound) { ... }
var (
	ErrValidation          = &APIError{Code: CodeValidation}
	ErrNotFound            = &APIError{Code: CodeNotFound}
	ErrConflict            = &APIError{Code: CodeConflict}
	ErrArchived            = &APIError{Code: CodeArchived}
	ErrDependencyViolation = &APIError{Code: CodeDependencyViolation}
	ErrUnauthenticated     = &APIError{Code: CodeUnauthenticated}
	ErrForbidden           = &APIError{Code: CodeForbidden}
	ErrRateLimited         = &APIError{Code: CodeRateLimited}
	ErrInternal            = &APIError{Code: CodeInternal}
)

// APIError is a structured error returned by the service. Details carries the
// machine-readable context the server attached (constraint names, field names,
// positions).
type APIError struct {
	// Status is the HTTP status code (0 for a sentinel).
	Status int
	// Code is the stable error code.
	Code ErrorCode
	// Message is the human-readable message.
	Message string
	// Details is the optional machine-readable context.
	Details map[string]any
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Message == "" {
		return "flexitype: " + string(e.Code)
	}
	return fmt.Sprintf("flexitype: %s: %s", e.Code, e.Message)
}

// Is lets errors.Is match by code, so a sentinel (ErrNotFound) matches any
// APIError carrying that code regardless of message, status or details.
func (e *APIError) Is(target error) bool {
	var t *APIError
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// decodeError turns a non-2xx response into a typed *APIError.
func decodeError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var wire struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details,omitempty"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.Error.Code == "" {
		return &APIError{
			Status:  resp.StatusCode,
			Code:    CodeInternal,
			Message: fmt.Sprintf("unexpected %d response: %s", resp.StatusCode, truncate(string(body), 200)),
		}
	}
	return &APIError{
		Status:  resp.StatusCode,
		Code:    ErrorCode(wire.Error.Code),
		Message: wire.Error.Message,
		Details: wire.Error.Details,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
