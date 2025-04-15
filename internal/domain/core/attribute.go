package core

import (
	"sort"
	"time"

	"github.com/zac300/flexitype/internal/domain/validation"
)

// DataType represents the type of data an attribute can hold
type DataType string

const (
	StringType  DataType = "string"
	IntType     DataType = "int"
	FloatType   DataType = "float"
	BooleanType DataType = "boolean"
	DateType    DataType = "date"
	ObjectType  DataType = "object"
	ArrayType   DataType = "array"
)

// CascadeBehavior defines how a cascade behaves when inherited
type CascadeBehavior string

const (
	CascadeInherit  CascadeBehavior = "inherit"  // Inherit cascade as-is
	CascadeOverride CascadeBehavior = "override" // Override with child's own value
	CascadeDisabled CascadeBehavior = "disabled" // Disable the cascade in this child
)

// Cascade represents an attribute that can be inherited by child types
type Cascade struct {
	ID       string          // Unique identifier for the cascade
	Enabled  bool            // If true, this attribute is a cascade that cascades to child types
	Behavior CascadeBehavior // How the cascade behaves when inherited by children
	Logic    string          // Optional expression for dynamic logic/constraints
	Weight   int             // Execution weight for the cascade (higher weight = higher priority)
}

// AttributeDefinition represents a dynamic attribute that can be assigned to a type
type AttributeDefinition struct {
	ID              string
	Name            string
	Description     string
	DataType        DataType
	Required        bool
	DefaultValue    interface{}
	ValidationRules []validation.Rule
	MultiValued     bool      // If true, this attribute can have multiple values
	Cascades        []Cascade // Cascade properties for inheritance
	Disabled        bool      // If true, this attribute is not settable/enforceable
	CreatedAt       time.Time // When the attribute was created
	UpdatedAt       time.Time // When the attribute was last updated
}

// NewAttributeDefinition creates a new attribute definition
func NewAttributeDefinition(id, name, description string, dataType DataType, required bool) *AttributeDefinition {
	now := time.Now()
	return &AttributeDefinition{
		ID:              id,
		Name:            name,
		Description:     description,
		DataType:        dataType,
		Required:        required,
		ValidationRules: make([]validation.Rule, 0),
		MultiValued:     false,
		Cascades:        make([]Cascade, 0),
		Disabled:        false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

// SetDefaultValue sets the default value for this attribute
func (a *AttributeDefinition) SetDefaultValue(value interface{}) {
	a.DefaultValue = value
	a.UpdatedAt = time.Now()
}

// AddValidationRule adds a validation rule to this attribute
func (a *AttributeDefinition) AddValidationRule(rule validation.Rule) {
	a.ValidationRules = append(a.ValidationRules, rule)
	a.UpdatedAt = time.Now()
}

// AddCascade adds a cascade with specified parameters to this attribute
func (a *AttributeDefinition) AddCascade(id string, enabled bool, behavior CascadeBehavior, logic string, weight int) {
	cascade := Cascade{
		ID:       id,
		Enabled:  enabled,
		Behavior: behavior,
		Logic:    logic,
		Weight:   weight,
	}
	a.Cascades = append(a.Cascades, cascade)
	a.UpdatedAt = time.Now()
}

// GetCascades returns all enabled cascades sorted by weight (highest weight first)
func (a *AttributeDefinition) GetCascades() []Cascade {
	// Filter enabled cascades
	enabledCascades := make([]Cascade, 0)
	for _, cascade := range a.Cascades {
		if cascade.Enabled {
			enabledCascades = append(enabledCascades, cascade)
		}
	}

	// Sort by weight (highest first)
	sort.Slice(enabledCascades, func(i, j int) bool {
		return enabledCascades[i].Weight > enabledCascades[j].Weight
	})

	return enabledCascades
}

// RemoveCascade removes a cascade with the specified ID
func (a *AttributeDefinition) RemoveCascade(id string) {
	for i, cascade := range a.Cascades {
		if cascade.ID == id {
			a.Cascades = append(a.Cascades[:i], a.Cascades[i+1:]...)
			a.UpdatedAt = time.Now()
			return
		}
	}
}

// HasEnabledCascades returns true if the attribute has at least one enabled cascade
func (a *AttributeDefinition) HasEnabledCascades() bool {
	for _, cascade := range a.Cascades {
		if cascade.Enabled {
			return true
		}
	}
	return false
}

// SetMultiValued sets whether this attribute can have multiple values
func (a *AttributeDefinition) SetMultiValued(multiValued bool) {
	a.MultiValued = multiValued
	a.UpdatedAt = time.Now()
}

// SetDisabled sets whether this attribute is disabled (not settable/enforceable)
func (a *AttributeDefinition) SetDisabled(disabled bool) {
	a.Disabled = disabled
	a.UpdatedAt = time.Now()
}

// IsActive returns true if the attribute is not disabled and should be used
func (a *AttributeDefinition) IsActive() bool {
	return !a.Disabled
}

// Validate validates a value against this attribute's rules
func (a *AttributeDefinition) Validate(value interface{}) []error {
	errors := make([]error, 0)

	// Apply all validation rules
	for _, rule := range a.ValidationRules {
		if err := rule.Validate(value); err != nil {
			errors = append(errors, err)
		}
	}

	return errors
}
