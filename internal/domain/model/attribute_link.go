package model

import (
	"fmt"
	"time"

	"github.com/oklog/ulid"
)

// AttributeLinkError represents an error related to attribute links
type AttributeLinkError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (e *AttributeLinkError) Error() string {
	return e.Message
}

// NewInvalidAttributeLinkError creates a new invalid attribute link error
func NewInvalidAttributeLinkError(message string, details map[string]interface{}) error {
	return &AttributeLinkError{
		Code:    "INVALID_ATTRIBUTE_LINK",
		Message: message,
		Details: details,
	}
}

// NewAttributeLinkNotFoundError creates a new not found error
func NewAttributeLinkNotFoundError(id ulid.ULID) error {
	return &AttributeLinkError{
		Code:    "ATTRIBUTE_LINK_NOT_FOUND",
		Message: fmt.Sprintf("attribute link with ID %s not found", id),
		Details: map[string]interface{}{
			"id": id,
		},
	}
}

// AttributeLink represents a link between two attribute definitions
type AttributeLink struct {
	ID                      ulid.ULID
	SourceAttributeID       ulid.ULID
	TargetAttributeID       ulid.ULID
	LinkType                string
	Description             string
	Version                 int
	CreatedAt               time.Time
	UpdatedAt               time.Time
	ArchivedAt              *time.Time
}

// AttributeLinkFilter represents filtering options for attribute links
type AttributeLinkFilter struct {
	BaseFilter
	// Version specifies the version number to filter by
	Version int
	// OnlyLatest specifies whether to only return the latest version of each link
	OnlyLatest bool
	// SourceAttributeID specifies the source attribute IDs to filter by
	SourceAttributeID []ulid.ULID
	// TargetAttributeID specifies the target attribute IDs to filter by
	TargetAttributeID []ulid.ULID
	// IDs specifies the link IDs to filter by
	IDs []ulid.ULID
}

// Validate validates the attribute link
func (l *AttributeLink) Validate() error {
	if l.SourceAttributeID == l.TargetAttributeID {
		return NewInvalidAttributeLinkError("source and target attributes cannot be the same", map[string]interface{}{
			"source_attribute_id": l.SourceAttributeID,
			"target_attribute_id": l.TargetAttributeID,
		})
	}
	if l.LinkType == "" {
		return NewInvalidAttributeLinkError("link type is required", nil)
	}
	if l.Version < 1 {
		return NewInvalidAttributeLinkError("version must be greater than 0", map[string]interface{}{
			"version": l.Version,
		})
	}
	return nil
}

// IncrementVersion increments the version number
func (l *AttributeLink) IncrementVersion() {
	l.Version++
}

// UpdateTimestamps updates the created_at and updated_at timestamps
func (l *AttributeLink) UpdateTimestamps() {
	now := time.Now()
	if l.CreatedAt.IsZero() {
		l.CreatedAt = now
	}
	l.UpdatedAt = now
}

// Archive marks the link as archived
func (l *AttributeLink) Archive() {
	now := time.Now()
	l.ArchivedAt = &now
	l.UpdatedAt = now
} 