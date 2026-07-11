// Package errors defines flexitype's typed domain errors. Interface layers
// map Code onto their transport (HTTP status, gRPC code); usecases and
// domain code create errors through the constructors so every failure
// carries a stable machine-readable code.
package errors

import (
	"errors"
	"fmt"
)

// Code classifies a domain failure.
type Code string

const (
	// CodeValidation marks malformed or rule-violating input.
	CodeValidation Code = "VALIDATION"
	// CodeNotFound marks a missing aggregate.
	CodeNotFound Code = "NOT_FOUND"
	// CodeConflict marks uniqueness or concurrency conflicts.
	CodeConflict Code = "CONFLICT"
	// CodeArchived marks mutations against archived aggregates.
	CodeArchived Code = "ARCHIVED"
	// CodeDependency marks values rejected by an attribute dependency.
	CodeDependency Code = "DEPENDENCY_VIOLATION"
)

// Error is a typed domain error with structured details.
type Error struct {
	Code    Code
	Message string
	Details map[string]any
}

func (e *Error) Error() string {
	return e.Message
}

// NewValidation builds a CodeValidation error. Details are optional
// key/value pairs: NewValidation("bad name", "name", got).
func NewValidation(message string, kv ...any) *Error {
	return &Error{Code: CodeValidation, Message: message, Details: details(kv)}
}

// NewNotFound builds a CodeNotFound error for an entity/ID pair.
func NewNotFound(entity, id string) *Error {
	return &Error{
		Code:    CodeNotFound,
		Message: fmt.Sprintf("%s %s not found", entity, id),
		Details: map[string]any{"entity": entity, "id": id},
	}
}

// NewConflict builds a CodeConflict error.
func NewConflict(message string, kv ...any) *Error {
	return &Error{Code: CodeConflict, Message: message, Details: details(kv)}
}

// NewArchived builds a CodeArchived error for an entity/ID pair.
func NewArchived(entity, id string) *Error {
	return &Error{
		Code:    CodeArchived,
		Message: fmt.Sprintf("%s %s is archived", entity, id),
		Details: map[string]any{"entity": entity, "id": id},
	}
}

// NewDependencyViolation builds a CodeDependency error.
func NewDependencyViolation(message string, kv ...any) *Error {
	return &Error{Code: CodeDependency, Message: message, Details: details(kv)}
}

func details(kv []any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	d := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprint(kv[i])
		}
		d[key] = kv[i+1]
	}
	return d
}

// CodeOf extracts the domain code from err, or "" when err is not a domain
// error.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// DetailsOf returns the structured details of a domain error, or nil.
func DetailsOf(err error) map[string]any {
	var e *Error
	if errors.As(err, &e) {
		return e.Details
	}
	return nil
}

// IsNotFound reports whether err carries CodeNotFound.
func IsNotFound(err error) bool { return CodeOf(err) == CodeNotFound }

// IsValidation reports whether err carries CodeValidation.
func IsValidation(err error) bool { return CodeOf(err) == CodeValidation }

// IsConflict reports whether err carries CodeConflict.
func IsConflict(err error) bool { return CodeOf(err) == CodeConflict }

// IsArchived reports whether err carries CodeArchived.
func IsArchived(err error) bool { return CodeOf(err) == CodeArchived }

// IsDependencyViolation reports whether err carries CodeDependency.
func IsDependencyViolation(err error) bool { return CodeOf(err) == CodeDependency }
