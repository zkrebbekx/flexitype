package sdk

import (
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
)

// Helper functions for working with the SDK

// DataType is a public alias for the core.DataType enum
type DataType = core.DataType

// Common data types for easier SDK usage
const (
	StringType  = core.StringType
	IntType     = core.IntType
	FloatType   = core.FloatType
	BooleanType = core.BooleanType
	DateType    = core.DateType
	ObjectType  = core.ObjectType
	ArrayType   = core.ArrayType
)

// NewAttribute creates a new attribute definition with a more friendly API
func NewAttribute(id, name, description string, dataType DataType, required bool) *core.AttributeDefinition {
	return core.NewAttributeDefinition(
		id,
		name,
		description,
		dataType,
		required,
	)
}

// Float64Ptr returns a pointer to a float64
func Float64Ptr(v float64) *float64 {
	return &v
}

// Common validation rules with a more friendly API

// RequiredRule creates a rule that validates a value is not nil or empty
type RequiredRule struct {
	validation.RequiredRule
}

// MinLengthRule validates string length is at least minLength
type MinLengthRule struct {
	validation.MinLengthRule
}

// MaxLengthRule validates string length is at most maxLength
type MaxLengthRule struct {
	validation.MaxLengthRule
}

// PatternRule validates strings against a regex pattern
type PatternRule struct {
	validation.PatternRule
}

// EnumRule validates values are in a predefined set
type EnumRule struct {
	validation.EnumRule
}

// RangeRule validates numeric values are within a range
type RangeRule struct {
	validation.RangeRule
}

// NewMinLengthRule creates a new min length validation rule
func NewMinLengthRule(minLength int) *MinLengthRule {
	return &MinLengthRule{
		MinLengthRule: validation.MinLengthRule{
			MinLength: minLength,
		},
	}
}

// NewMaxLengthRule creates a new max length validation rule
func NewMaxLengthRule(maxLength int) *MaxLengthRule {
	return &MaxLengthRule{
		MaxLengthRule: validation.MaxLengthRule{
			MaxLength: maxLength,
		},
	}
}

// NewPatternRule creates a new pattern validation rule
func NewPatternRule(pattern string) (*PatternRule, error) {
	rule, err := validation.NewPatternRule(pattern)
	if err != nil {
		return nil, err
	}

	return &PatternRule{
		PatternRule: *rule,
	}, nil
}

// NewEnumRule creates a new enum validation rule
func NewEnumRule(allowedValues []interface{}) *EnumRule {
	return &EnumRule{
		EnumRule: validation.EnumRule{
			AllowedValues: allowedValues,
		},
	}
}

// NewRangeRule creates a new range validation rule
func NewRangeRule(min, max *float64) *RangeRule {
	return &RangeRule{
		RangeRule: validation.RangeRule{
			Min: min,
			Max: max,
		},
	}
}

// CascadeBehavior is a type alias for the core.CascadeBehavior enum
type CascadeBehavior = core.CascadeBehavior

// Common cascade behaviors for easier SDK usage
const (
	CascadeInherit  = core.CascadeInherit
	CascadeOverride = core.CascadeOverride
	CascadeDisabled = core.CascadeDisabled
)

// NewCascade creates a new cascade definition with specified parameters
func NewCascade(enabled bool, behavior CascadeBehavior, logic string, weight int) core.Cascade {
	return core.Cascade{
		Enabled:  enabled,
		Behavior: behavior,
		Logic:    logic,
		Weight:   weight,
	}
}
