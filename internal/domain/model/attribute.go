package model

import (
	"fmt"
	"time"

	"github.com/oklog/ulid"
)

// AttributeError represents an error related to attributes
type AttributeError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

func (e *AttributeError) Error() string {
	return e.Message
}

// NewInvalidAttributeError creates a new invalid attribute error
func NewInvalidAttributeError(message string, details map[string]interface{}) error {
	return &AttributeError{
		Code:    "INVALID_ATTRIBUTE",
		Message: message,
		Details: details,
	}
}

// NewAttributeNotFoundError creates a new not found error
func NewAttributeNotFoundError(id ulid.ULID) error {
	return &AttributeError{
		Code:    "ATTRIBUTE_NOT_FOUND",
		Message: fmt.Sprintf("attribute with ID %s not found", id),
		Details: map[string]interface{}{
			"id": id,
		},
	}
}

// AttributeType represents the type of an attribute
type AttributeType string

const (
	TypeBool    AttributeType = "bool"
	TypeString  AttributeType = "string"
	TypeInteger AttributeType = "integer"
	TypeFloat   AttributeType = "float"
	TypeDate    AttributeType = "date"
	TypeTime    AttributeType = "time"
	TypeEnum    AttributeType = "enum"
	TypeDecimal AttributeType = "decimal"
	TypeURL     AttributeType = "url"
	TypeEmail   AttributeType = "email"
	TypeJSON    AttributeType = "json"
)

// ConstraintType represents the type of constraint
type ConstraintType string

const (
	ConstraintRequired  ConstraintType = "required"
	ConstraintMinLength ConstraintType = "min_length"
	ConstraintMaxLength ConstraintType = "max_length"
	ConstraintMinValue  ConstraintType = "min_value"
	ConstraintMaxValue  ConstraintType = "max_value"
	ConstraintPattern   ConstraintType = "pattern"
	ConstraintEnum      ConstraintType = "enum"
	ConstraintMulti     ConstraintType = "multi"
	ConstraintUnique    ConstraintType = "unique"
)

// AttributeDefinition represents a soft attribute definition
type AttributeDefinition struct {
	ID               ulid.ULID
	InternalName     string
	DisplayName      string
	Description      string
	TypeDefinitionID ulid.ULID
	Constraints      []byte
	Version          int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ArchivedAt       *time.Time
}

// Constraint represents a validation rule for an attribute
type Constraint struct {
	Type  ConstraintType `json:"type"`
	Value interface{}    `json:"value"` // The actual constraint value (e.g., min length, pattern, etc.)
}

// AttributeValue represents the actual value of an attribute
type AttributeValue struct {
	ID                   ulid.ULID
	AttributeDefinitionID ulid.ULID
	Value                interface{}
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ArchivedAt           *time.Time
}

// AttributeLink represents a dependency between attributes
type AttributeLink struct {
	ID            ulid.ULID
	SourceAttrID  ulid.ULID
	TargetAttrID  ulid.ULID
	Condition     string    // JSON path expression for the condition
	UpdatedValues []string  // Values to update in target when condition is met
	Version       int
	CreatedAt     time.Time
	UpdatedAt     time.Time
	ArchivedAt    *time.Time
}

// ListOptions represents options for listing entities
type ListOptions struct {
	IncludeArchived bool `json:"include_archived"`
	Version         *int `json:"version,omitempty"`
}

type Attribute struct {
	ID                ulid.ULID
	TypeDefinitionID  ulid.ULID
	InternalName      string
	DisplayName       string
	Description       string
	DataType          string
	IsRequired        bool
	DefaultValue      *DynamicValue
	ValidationRules   []ValidationRule
	Version           int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ArchivedAt        *time.Time
}

// Validate validates the attribute
func (a *Attribute) Validate() error {
	if a.InternalName == "" {
		return NewInvalidAttributeError("internal name is required", nil)
	}
	if a.DisplayName == "" {
		return NewInvalidAttributeError("display name is required", nil)
	}
	if a.DataType == "" {
		return NewInvalidAttributeError("data type is required", nil)
	}
	if a.Version < 1 {
		return NewInvalidAttributeError("version must be greater than 0", map[string]interface{}{
			"version": a.Version,
		})
	}
	if a.DefaultValue != nil {
		if err := a.DefaultValue.Validate(a.DataType); err != nil {
			return NewInvalidAttributeError("invalid default value", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}
	for _, rule := range a.ValidationRules {
		if err := rule.Validate(); err != nil {
			return NewInvalidAttributeError("invalid validation rule", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}
	return nil
}

// IncrementVersion increments the version number
func (a *Attribute) IncrementVersion() {
	a.Version++
}

// UpdateTimestamps updates the created_at and updated_at timestamps
func (a *Attribute) UpdateTimestamps() {
	now := time.Now()
	if a.CreatedAt.IsZero() {
		a.CreatedAt = now
	}
	a.UpdatedAt = now
}

// Archive marks the attribute as archived
func (a *Attribute) Archive() {
	now := time.Now()
	a.ArchivedAt = &now
	a.UpdatedAt = now
}

// IsArchived returns whether the attribute is archived
func (a *Attribute) IsArchived() bool {
	return a.ArchivedAt != nil
}

// ValidateValue validates a value against the attribute's data type and validation rules
func (a *Attribute) ValidateValue(value *DynamicValue) error {
	if value == nil {
		if a.IsRequired {
			return NewInvalidAttributeError("value is required", nil)
		}
		return nil
	}

	if err := value.Validate(a.DataType); err != nil {
		return NewInvalidAttributeError("invalid value type", map[string]interface{}{
			"error": err.Error(),
		})
	}

	for _, rule := range a.ValidationRules {
		if err := rule.ValidateValue(value); err != nil {
			return NewInvalidAttributeError("validation rule failed", map[string]interface{}{
				"error": err.Error(),
			})
		}
	}

	return nil
}

// AttributeFilter represents filtering options for attributes
type AttributeFilter struct {
	BaseFilter
	// Version specifies the version number to filter by
	Version int
	// OnlyLatest specifies whether to only return the latest version of each attribute
	OnlyLatest bool
	// TypeDefinitionID specifies the type definition ID to filter by
	TypeDefinitionID ulid.ULID
	// InternalName specifies the internal names to filter by
	InternalName []string
	// DataType specifies the data types to filter by
	DataType []string
	// IDs specifies the attribute IDs to filter by
	IDs []ulid.ULID
} 