package model

import (
	"fmt"
	"time"

	"github.com/oklog/ulid"
)

// BaseFilter represents common filtering options
type BaseFilter struct {
	// Limit specifies the maximum number of results to return
	Limit int
	// Offset specifies the number of results to skip
	Offset int
	// IncludeArchived specifies whether to include archived entities
	IncludeArchived bool
	// CreatedAfter specifies the minimum creation time for results
	CreatedAfter time.Time
	// CreatedBefore specifies the maximum creation time for results
	CreatedBefore time.Time
	// UpdatedAfter specifies the minimum update time for results
	UpdatedAfter time.Time
	// UpdatedBefore specifies the maximum update time for results
	UpdatedBefore time.Time
}

// DynamicValueType represents the type of dynamic value
type DynamicValueType string

const (
	DynamicValueTypeNow           DynamicValueType = "now"
	DynamicValueTypeToday         DynamicValueType = "today"
	DynamicValueTypeRelativeTime  DynamicValueType = "relative_time"
	DynamicValueTypeAttributeValue DynamicValueType = "attribute_value"
)

// DynamicValue represents a value that is calculated at runtime
type DynamicValue struct {
	Type     DynamicValueType   `json:"type"`
	Value    interface{}        `json:"value"`
	Metadata map[string]string `json:"metadata"`
}

// Calculate calculates the value of a dynamic value
func (dv *DynamicValue) Calculate() (interface{}, error) {
	switch dv.Type {
	case DynamicValueTypeNow:
		return time.Now(), nil
	case DynamicValueTypeToday:
		return time.Now().Truncate(24 * time.Hour), nil
	case DynamicValueTypeRelativeTime:
		params, ok := dv.Value.(map[string]interface{})
		if !ok {
			return nil, NewInvalidRelativeTimeParamsError()
		}

		period, ok := params["period"].(string)
		if !ok {
			return nil, NewInvalidPeriodError("")
		}

		amount, ok := params["amount"].(float64)
		if !ok {
			return nil, NewInvalidAmountError()
		}

		duration, err := parseDuration(period, int(amount))
		if err != nil {
			return nil, NewUnknownPeriodError(period)
		}

		return time.Now().Add(duration), nil
	case DynamicValueTypeAttributeValue:
		return nil, NewAttributeValueLookupNotImplementedError()
	default:
		return nil, NewInvalidDynamicValueTypeError(dv.Type)
	}
}

// parseDuration parses a duration from a period and amount
func parseDuration(period string, amount int) (time.Duration, error) {
	switch period {
	case "seconds":
		return time.Duration(amount) * time.Second, nil
	case "minutes":
		return time.Duration(amount) * time.Minute, nil
	case "hours":
		return time.Duration(amount) * time.Hour, nil
	case "days":
		return time.Duration(amount) * 24 * time.Hour, nil
	case "weeks":
		return time.Duration(amount) * 7 * 24 * time.Hour, nil
	case "months":
		return time.Duration(amount) * 30 * 24 * time.Hour, nil
	case "years":
		return time.Duration(amount) * 365 * 24 * time.Hour, nil
	default:
		return 0, NewUnknownPeriodError(period)
	}
}

// ConditionType represents the type of condition
type ConditionType string

const (
	ConditionTypeEquals      ConditionType = "equals"
	ConditionTypeIn         ConditionType = "in"
	ConditionTypeRange      ConditionType = "range"
	ConditionTypePattern    ConditionType = "pattern"
	ConditionTypeDynamicRange ConditionType = "dynamic_range"
)

// DependencyCondition represents a condition that must be met for a dependency to be valid
type DependencyCondition struct {
	Type         ConditionType  `json:"type"`
	Value        interface{}    `json:"value"`
	DynamicValue *DynamicValue `json:"dynamic_value,omitempty"`
	Metadata     map[string]string `json:"metadata"`
}

// Evaluate evaluates whether a condition is met
func (c *DependencyCondition) Evaluate(value interface{}) bool {
	switch c.Type {
	case ConditionTypeEquals:
		return c.evaluateEquals(value)
	case ConditionTypeIn:
		return c.evaluateIn(value)
	case ConditionTypeRange:
		return c.evaluateRange(value)
	case ConditionTypePattern:
		return c.evaluatePattern(value)
	case ConditionTypeDynamicRange:
		return c.evaluateDynamicRange(value)
	default:
		return false
	}
}

// evaluateEquals evaluates an equals condition
func (c *DependencyCondition) evaluateEquals(value interface{}) bool {
	return c.Value == value
}

// evaluateIn evaluates an in condition
func (c *DependencyCondition) evaluateIn(value interface{}) bool {
	values, ok := c.Value.([]interface{})
	if !ok {
		return false
	}

	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// evaluateRange evaluates a range condition
func (c *DependencyCondition) evaluateRange(value interface{}) bool {
	rangeValue, ok := c.Value.(map[string]interface{})
	if !ok {
		return false
	}

	min, hasMin := rangeValue["min"]
	max, hasMax := rangeValue["max"]

	switch v := value.(type) {
	case int:
		if hasMin && v < int(min.(float64)) {
			return false
		}
		if hasMax && v > int(max.(float64)) {
			return false
		}
	case float64:
		if hasMin && v < min.(float64) {
			return false
		}
		if hasMax && v > max.(float64) {
			return false
		}
	case time.Time:
		if hasMin && v.Before(min.(time.Time)) {
			return false
		}
		if hasMax && v.After(max.(time.Time)) {
			return false
		}
	default:
		return false
	}

	return true
}

// evaluatePattern evaluates a pattern condition
func (c *DependencyCondition) evaluatePattern(value interface{}) bool {
	pattern, ok := c.Value.(string)
	if !ok {
		return false
	}

	strValue, ok := value.(string)
	if !ok {
		return false
	}

	// TODO: Implement pattern matching
	return true
}

// evaluateDynamicRange evaluates a dynamic range condition
func (c *DependencyCondition) evaluateDynamicRange(value interface{}) bool {
	if c.DynamicValue == nil {
		return false
	}

	dynamicValue, err := c.DynamicValue.Calculate()
	if err != nil {
		return false
	}

	operator, ok := c.Metadata["operator"]
	if !ok {
		return false
	}

	switch operator {
	case "greater_than":
		return compareValues(value, dynamicValue) > 0
	case "less_than":
		return compareValues(value, dynamicValue) < 0
	case "greater_than_or_equal":
		return compareValues(value, dynamicValue) >= 0
	case "less_than_or_equal":
		return compareValues(value, dynamicValue) <= 0
	default:
		return false
	}
}

// compareValues compares two values
func compareValues(a, b interface{}) int {
	switch a := a.(type) {
	case int:
		if b, ok := b.(int); ok {
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		}
	case float64:
		if b, ok := b.(float64); ok {
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		}
	case time.Time:
		if b, ok := b.(time.Time); ok {
			if a.Before(b) {
				return -1
			}
			if a.After(b) {
				return 1
			}
			return 0
		}
	}
	return 0
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

// AttributeDefinitionFilter represents filtering options for attribute definitions
type AttributeDefinitionFilter struct {
	BaseFilter
	// Version specifies the version number to filter by
	Version int
	// OnlyLatest specifies whether to only return the latest version of each attribute definition
	OnlyLatest bool
	// InternalName specifies the internal names to filter by
	InternalName []string
	// TypeDefinitionID specifies the type definition IDs to filter by
	TypeDefinitionID []ulid.ULID
	// IDs specifies the attribute definition IDs to filter by
	IDs []ulid.ULID
}

// AttributeValueFilter represents filtering options for attribute values
type AttributeValueFilter struct {
	BaseFilter
	// AttributeDefinitionID specifies the attribute definition IDs to filter by
	AttributeDefinitionID []ulid.ULID
	// IDs specifies the attribute value IDs to filter by
	IDs []ulid.ULID
}

// AttributeValueDependency represents a dependency between attribute values
type AttributeValueDependency struct {
	ID                ulid.ULID              `json:"id"`
	SourceAttributeID ulid.ULID              `json:"source_attribute_id"`
	SourceConditions  []DependencyCondition  `json:"source_conditions"`
	TargetAttributeID ulid.ULID              `json:"target_attribute_id"`
	TargetValues      []string               `json:"target_values"`
	TargetConstraints []DependencyCondition  `json:"target_constraints"`
	Description       string                 `json:"description"`
	ValidationRules   map[string]interface{} `json:"validation_rules"`
	IsRequired        bool                   `json:"is_required"`
	DefaultValue      interface{}            `json:"default_value"`
	Version           int                    `json:"version"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
	ArchivedAt        *time.Time             `json:"archived_at"`
}

// Validate validates the dependency
func (d *AttributeValueDependency) Validate() error {
	if err := d.validateConditions(); err != nil {
		return err
	}

	if err := d.validateTargetValues(); err != nil {
		return err
	}

	if err := d.validateConstraints(); err != nil {
		return err
	}

	return nil
}

// validateConditions validates the source conditions
func (d *AttributeValueDependency) validateConditions() error {
	for _, condition := range d.SourceConditions {
		switch condition.Type {
		case ConditionTypeEquals, ConditionTypeIn, ConditionTypeRange, ConditionTypePattern, ConditionTypeDynamicRange:
			// Valid condition types
		default:
			return NewInvalidConditionTypeError(condition.Type)
		}

		if condition.DynamicValue != nil {
			switch condition.DynamicValue.Type {
			case DynamicValueTypeNow, DynamicValueTypeToday, DynamicValueTypeRelativeTime, DynamicValueTypeAttributeValue:
				// Valid dynamic value types
			default:
				return NewInvalidDynamicValueTypeError(condition.DynamicValue.Type)
			}
		}
	}
	return nil
}

// validateTargetValues validates the target values
func (d *AttributeValueDependency) validateTargetValues() error {
	if len(d.TargetValues) == 0 {
		return NewEmptyTargetValuesError()
	}
	return nil
}

// validateConstraints validates the target constraints
func (d *AttributeValueDependency) validateConstraints() error {
	for _, constraint := range d.TargetConstraints {
		switch constraint.Type {
		case ConditionTypeDynamicRange:
			// Valid constraint type
		default:
			return NewInvalidConstraintTypeError(constraint.Type)
		}

		if constraint.DynamicValue == nil {
			return NewMissingDynamicValueError()
		}

		switch constraint.DynamicValue.Type {
		case DynamicValueTypeNow, DynamicValueTypeToday, DynamicValueTypeRelativeTime, DynamicValueTypeAttributeValue:
			// Valid dynamic value types
		default:
			return NewInvalidDynamicValueTypeError(constraint.DynamicValue.Type)
		}
	}
	return nil
}

// EvaluateConditions evaluates whether all source conditions are met
func (d *AttributeValueDependency) EvaluateConditions(value interface{}) bool {
	for _, condition := range d.SourceConditions {
		if !condition.Evaluate(value) {
			return false
		}
	}
	return true
}

// AttributeValueDependencyFilter represents filtering options for attribute value dependencies
type AttributeValueDependencyFilter struct {
	BaseFilter
	// Version specifies the version number to filter by
	Version int
	// OnlyLatest specifies whether to only return the latest version of each dependency
	OnlyLatest bool
	// SourceAttributeID specifies the source attribute IDs to filter by
	SourceAttributeID []ulid.ULID
	// TargetAttributeID specifies the target attribute IDs to filter by
	TargetAttributeID []ulid.ULID
	// SourceValue specifies the source values to filter by
	SourceValue []string
	// IDs specifies the dependency IDs to filter by
	IDs []ulid.ULID
}

// TypeDefinition represents a type definition
type TypeDefinition struct {
	ID           ulid.ULID
	InternalName string
	DisplayName  string
	Version      int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	ArchivedAt   *time.Time
}

// AttributeDefinition represents an attribute definition in the system
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

// AttributeValue represents a value for an attribute
type AttributeValue struct {
	ID                   ulid.ULID
	AttributeDefinitionID ulid.ULID
	Value                interface{}
	CreatedAt            time.Time
	UpdatedAt            time.Time
	ArchivedAt           *time.Time
}

// ListOptions represents options for listing entities
type ListOptions struct {
	IncludeArchived bool
	Version         *int
} 