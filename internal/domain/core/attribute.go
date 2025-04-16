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
	CascadeInherit      CascadeBehavior = "inherit"       // Inherit cascade as-is
	CascadeOverride     CascadeBehavior = "override"      // Override with child's own value
	CascadeDisabled     CascadeBehavior = "disabled"      // Disable the cascade in this child
	CascadeValidation   CascadeBehavior = "validation"    // Cascade modifies validation rules
	CascadeRequirement  CascadeBehavior = "requirement"   // Cascade changes whether field is required
	CascadeEnumValues   CascadeBehavior = "enum_values"   // Cascade modifies enum allowed values
	CascadeDefaultValue CascadeBehavior = "default_value" // Cascade modifies default value
)

// CascadeValidationAction specifies what validation action to perform
type CascadeValidationAction string

const (
	ActionMakeRequired     CascadeValidationAction = "make_required"      // Make the field required
	ActionMakeOptional     CascadeValidationAction = "make_optional"      // Make the field optional
	ActionSetEnumValues    CascadeValidationAction = "set_enum_values"    // Set/replace enum allowed values
	ActionAddEnumValues    CascadeValidationAction = "add_enum_values"    // Add values to an enum
	ActionRemoveEnumValues CascadeValidationAction = "remove_enum_values" // Remove values from an enum
	ActionSetMinValue      CascadeValidationAction = "set_min_value"      // Set min value for numeric fields
	ActionSetMaxValue      CascadeValidationAction = "set_max_value"      // Set max value for numeric fields
	ActionSetMinLength     CascadeValidationAction = "set_min_length"     // Set min length for string fields
	ActionSetMaxLength     CascadeValidationAction = "set_max_length"     // Set max length for string fields
	ActionSetPattern       CascadeValidationAction = "set_pattern"        // Set regex pattern for string fields
	ActionSetDefaultValue  CascadeValidationAction = "set_default_value"  // Set default value
)

// CascadeValidationConfig contains configuration for validation modifications
type CascadeValidationConfig struct {
	Action       CascadeValidationAction // Action to perform
	TargetField  string                  // Target field to apply the validation to (empty for self)
	Values       []interface{}           // Values to use (enum values, min/max, etc.)
	StringValue  string                  // String value (for pattern, etc.)
	NumericValue float64                 // Numeric value (for min/max, etc.)
}

// Cascade represents an attribute that can be inherited by child types
type Cascade struct {
	ID               string                   // Unique identifier for the cascade
	Enabled          bool                     // If true, this attribute is a cascade that cascades to child types
	Behavior         CascadeBehavior          // How the cascade behaves when inherited by children
	Logic            string                   // Optional expression for dynamic logic/constraints
	Weight           int                      // Execution weight for the cascade (higher weight = higher priority)
	ValidationConfig *CascadeValidationConfig // Configuration for validation cascades (used when Behavior is CascadeValidation*)
	expression       ExpressionEvaluator      // Optional custom expression evaluator
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

// AddValidationCascade adds a cascade that modifies validation rules based on conditions
func (a *AttributeDefinition) AddValidationCascade(id string, enabled bool, behavior CascadeBehavior,
	logic string, weight int, validationConfig *CascadeValidationConfig) {

	cascade := Cascade{
		ID:               id,
		Enabled:          enabled,
		Behavior:         behavior,
		Logic:            logic,
		Weight:           weight,
		ValidationConfig: validationConfig,
		expression:       NewExpression(logic),
	}
	a.Cascades = append(a.Cascades, cascade)
	a.UpdatedAt = time.Now()
}

// AddValidationCascadeWithCustomExpr adds a cascade with a custom expression evaluator
func (a *AttributeDefinition) AddValidationCascadeWithCustomExpr(id string, enabled bool,
	behavior CascadeBehavior, expr ExpressionEvaluator, weight int, validationConfig *CascadeValidationConfig) {

	cascade := Cascade{
		ID:               id,
		Enabled:          enabled,
		Behavior:         behavior,
		Logic:            "Custom expression",
		Weight:           weight,
		ValidationConfig: validationConfig,
		expression:       expr,
	}
	a.Cascades = append(a.Cascades, cascade)
	a.UpdatedAt = time.Now()
}

// AddRequirementCascade is a shorthand for adding a cascade that makes a field required or optional
func (a *AttributeDefinition) AddRequirementCascade(id string, enabled bool, logic string, weight int,
	targetField string, makeRequired bool) {

	action := ActionMakeOptional
	if makeRequired {
		action = ActionMakeRequired
	}

	cfg := &CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
	}

	a.AddValidationCascade(id, enabled, CascadeRequirement, logic, weight, cfg)
}

// AddEnumValuesCascade is a shorthand for adding a cascade that modifies enum values
func (a *AttributeDefinition) AddEnumValuesCascade(id string, enabled bool, logic string, weight int,
	targetField string, action CascadeValidationAction, values []interface{}) {

	cfg := &CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
		Values:      values,
	}

	a.AddValidationCascade(id, enabled, CascadeEnumValues, logic, weight, cfg)
}

// AddNumericConstraintCascade adds a cascade that sets min/max values for numeric fields
func (a *AttributeDefinition) AddNumericConstraintCascade(id string, enabled bool, logic string, weight int,
	targetField string, action CascadeValidationAction, value float64) {

	cfg := &CascadeValidationConfig{
		Action:       action,
		TargetField:  targetField,
		NumericValue: value,
	}

	a.AddValidationCascade(id, enabled, CascadeValidation, logic, weight, cfg)
}

// AddStringConstraintCascade adds a cascade that sets min/max length or pattern for string fields
func (a *AttributeDefinition) AddStringConstraintCascade(id string, enabled bool, logic string, weight int,
	targetField string, action CascadeValidationAction, value interface{}) {

	cfg := &CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
	}

	// Handle different actions
	switch action {
	case ActionSetMinLength, ActionSetMaxLength:
		if numValue, ok := value.(int); ok {
			cfg.NumericValue = float64(numValue)
		}
	case ActionSetPattern:
		if strValue, ok := value.(string); ok {
			cfg.StringValue = strValue
		}
	}

	a.AddValidationCascade(id, enabled, CascadeValidation, logic, weight, cfg)
}

// AddDefaultValueCascade adds a cascade that sets a default value based on conditions
func (a *AttributeDefinition) AddDefaultValueCascade(id string, enabled bool, logic string, weight int,
	targetField string, defaultValue interface{}) {

	cfg := &CascadeValidationConfig{
		Action:      ActionSetDefaultValue,
		TargetField: targetField,
		Values:      []interface{}{defaultValue},
	}

	a.AddValidationCascade(id, enabled, CascadeDefaultValue, logic, weight, cfg)
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
