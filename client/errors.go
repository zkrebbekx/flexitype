package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
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

	// CodeFeatureDisabled means the deployment does not run this optional
	// capability — search, the outbox, media storage, GraphQL. It is
	// permanent for that deployment, so retrying cannot help.
	CodeFeatureDisabled ErrorCode = "FEATURE_DISABLED"

	// CodeCursorConflict means another consumer committed the cursor first.
	// Re-read the cursor and retry from the position it now holds.
	CodeCursorConflict ErrorCode = "CURSOR_CONFLICT"

	// CodeCursorExpired means the position is older than the feed's
	// retention. Re-baseline: the events between the cursor and the feed's
	// oldest retained event are gone.
	CodeCursorExpired ErrorCode = "CURSOR_EXPIRED"
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
	ErrFeatureDisabled     = &APIError{Code: CodeFeatureDisabled}
	ErrCursorConflict      = &APIError{Code: CodeCursorConflict}
	ErrCursorExpired       = &APIError{Code: CodeCursorExpired}
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

	// RetryAfter is how long the server asked the caller to wait before
	// retrying, parsed from the Retry-After header. It is zero when the
	// server sent no hint. The server sets it on every throttle, and it was
	// dropped: callers saw RATE_LIMITED with no wait hint and either failed
	// outright or invented their own backoff.
	RetryAfter time.Duration
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
	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
	if err := json.Unmarshal(body, &wire); err != nil || wire.Error.Code == "" {
		return &APIError{
			Status:     resp.StatusCode,
			Code:       CodeInternal,
			Message:    fmt.Sprintf("unexpected %d response: %s", resp.StatusCode, truncate(string(body), 200)),
			RetryAfter: retryAfter,
		}
	}
	return &APIError{
		Status:     resp.StatusCode,
		Code:       ErrorCode(wire.Error.Code),
		Message:    wire.Error.Message,
		Details:    wire.Error.Details,
		RetryAfter: retryAfter,
	}
}

// parseRetryAfter reads both forms RFC 9110 allows for Retry-After: a delay
// in whole seconds, and an HTTP date. A date in the past gives zero, not a
// negative wait.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	when, err := http.ParseTime(header)
	if err != nil {
		return 0
	}
	if d := when.Sub(now); d > 0 {
		return d
	}
	return 0
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
