package validation

import (
	"fmt"
	"reflect"
	"regexp"
)

// Rule is the interface that all validation rules must implement
type Rule interface {
	Validate(value interface{}) error
}

// RequiredRule validates that a value is not nil or empty
type RequiredRule struct{}

func (r *RequiredRule) Validate(value interface{}) error {
	if value == nil {
		return fmt.Errorf("value is required but was nil")
	}

	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String:
		if v.String() == "" {
			return fmt.Errorf("value is required but was empty string")
		}
	case reflect.Slice, reflect.Map, reflect.Array:
		if v.Len() == 0 {
			return fmt.Errorf("value is required but was empty collection")
		}
	}

	return nil
}

// MinLengthRule validates that a string has at least minLength characters
type MinLengthRule struct {
	MinLength int
}

func (r *MinLengthRule) Validate(value interface{}) error {
	if value == nil {
		return nil // Let RequiredRule handle nil check
	}

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.String {
		return fmt.Errorf("expected string but got %T", value)
	}

	if len(v.String()) < r.MinLength {
		return fmt.Errorf("string length %d is less than minimum %d", len(v.String()), r.MinLength)
	}

	return nil
}

// MaxLengthRule validates that a string has at most maxLength characters
type MaxLengthRule struct {
	MaxLength int
}

func (r *MaxLengthRule) Validate(value interface{}) error {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.String {
		return fmt.Errorf("expected string but got %T", value)
	}

	if len(v.String()) > r.MaxLength {
		return fmt.Errorf("string length %d is greater than maximum %d", len(v.String()), r.MaxLength)
	}

	return nil
}

// PatternRule validates that a string matches a regular expression pattern
type PatternRule struct {
	Pattern string
	regex   *regexp.Regexp
}

func NewPatternRule(pattern string) (*PatternRule, error) {
	regex, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	return &PatternRule{
		Pattern: pattern,
		regex:   regex,
	}, nil
}

func (r *PatternRule) Validate(value interface{}) error {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	if v.Kind() != reflect.String {
		return fmt.Errorf("expected string but got %T", value)
	}

	if !r.regex.MatchString(v.String()) {
		return fmt.Errorf("string does not match pattern '%s'", r.Pattern)
	}

	return nil
}

// EnumRule validates that a value is one of a predefined set of values
type EnumRule struct {
	AllowedValues []interface{}
}

func (r *EnumRule) Validate(value interface{}) error {
	if value == nil {
		return nil
	}

	for _, allowed := range r.AllowedValues {
		if reflect.DeepEqual(value, allowed) {
			return nil
		}
	}

	return fmt.Errorf("value is not one of the allowed values: %v", r.AllowedValues)
}

// RangeRule validates that a numeric value is within a specified range
type RangeRule struct {
	Min *float64
	Max *float64
}

func (r *RangeRule) Validate(value interface{}) error {
	if value == nil {
		return nil
	}

	v := reflect.ValueOf(value)
	var floatValue float64

	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		floatValue = float64(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		floatValue = float64(v.Uint())
	case reflect.Float32, reflect.Float64:
		floatValue = v.Float()
	default:
		return fmt.Errorf("expected numeric type but got %T", value)
	}

	if r.Min != nil && floatValue < *r.Min {
		return fmt.Errorf("value %v is less than minimum %v", floatValue, *r.Min)
	}

	if r.Max != nil && floatValue > *r.Max {
		return fmt.Errorf("value %v is greater than maximum %v", floatValue, *r.Max)
	}

	return nil
}

// CustomRule allows for custom validation logic
type CustomRule struct {
	ValidationFunc func(interface{}) error
	Description    string
}

func (r *CustomRule) Validate(value interface{}) error {
	return r.ValidationFunc(value)
}

// GenericRule is a placeholder rule that always succeeds
type GenericRule struct{}

func (r *GenericRule) Validate(value interface{}) error {
	return nil // Always validates successfully
}
