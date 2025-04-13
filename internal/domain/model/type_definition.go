package model

import (
	"fmt"
	"time"

	"github.com/oklog/ulid"
)

// TypeDefinitionError represents an error related to type definitions
type TypeDefinitionError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (e *TypeDefinitionError) Error() string {
	return e.Message
}

// NewInvalidTypeDefinitionError creates a new invalid type definition error
func NewInvalidTypeDefinitionError(message string, details map[string]interface{}) error {
	return &TypeDefinitionError{
		Code:    "INVALID_TYPE_DEFINITION",
		Message: message,
		Details: details,
	}
}

// NewTypeDefinitionNotFoundError creates a new not found error
func NewTypeDefinitionNotFoundError(id ulid.ULID) error {
	return &TypeDefinitionError{
		Code:    "TYPE_DEFINITION_NOT_FOUND",
		Message: fmt.Sprintf("type definition with ID %s not found", id),
		Details: map[string]interface{}{
			"id": id,
		},
	}
}

type TypeDefinition struct {
	ID           ulid.ULID
	InternalName string
	DisplayName  string
	Version      int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
}

// Validate validates the type definition
func (t *TypeDefinition) Validate() error {
	if t.InternalName == "" {
		return NewInvalidTypeDefinitionError("internal name is required", nil)
	}
	if t.DisplayName == "" {
		return NewInvalidTypeDefinitionError("display name is required", nil)
	}
	if t.Version < 1 {
		return NewInvalidTypeDefinitionError("version must be greater than 0", map[string]interface{}{
			"version": t.Version,
		})
	}
	return nil
}

// IncrementVersion increments the version number
func (t *TypeDefinition) IncrementVersion() {
	t.Version++
}

// UpdateTimestamps updates the created_at and updated_at timestamps
func (t *TypeDefinition) UpdateTimestamps() {
	now := time.Now()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
}

// Archive marks the type definition as archived
func (t *TypeDefinition) Archive() {
	now := time.Now()
	t.ArchivedAt = &now
	t.UpdatedAt = now
}

// IsArchived returns whether the type definition is archived
func (t *TypeDefinition) IsArchived() bool {
	return t.ArchivedAt != nil
}

// TypeDefinitionFilter represents filtering options for type definitions
type TypeDefinitionFilter struct {
	BaseFilter
	// Version specifies the version number to filter by
	Version int
	// OnlyLatest specifies whether to only return the latest version of each type definition
	OnlyLatest bool
	// InternalName specifies the internal names to filter by
	InternalName []string
	// IDs specifies the type definition IDs to filter by
	IDs []ulid.ULID
} 