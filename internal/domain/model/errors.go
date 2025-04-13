package model

import "fmt"

// DomainError represents a domain-specific error
type DomainError struct {
	Code    string
	Message string
	Details map[string]interface{}
}

// Error implements the error interface
func (e *DomainError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// NewDomainError creates a new domain error
func NewDomainError(code, message string, details map[string]interface{}) *DomainError {
	return &DomainError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// Error codes
const (
	// DynamicValue errors
	ErrInvalidDynamicValueType     = "INVALID_DYNAMIC_VALUE_TYPE"
	ErrInvalidRelativeTimeParams   = "INVALID_RELATIVE_TIME_PARAMS"
	ErrInvalidPeriod              = "INVALID_PERIOD"
	ErrInvalidAmount              = "INVALID_AMOUNT"
	ErrUnknownPeriod              = "UNKNOWN_PERIOD"
	ErrAttributeValueLookupNotImpl = "ATTRIBUTE_VALUE_LOOKUP_NOT_IMPLEMENTED"

	// Condition errors
	ErrInvalidConditionType     = "INVALID_CONDITION_TYPE"
	ErrInvalidDynamicValue      = "INVALID_DYNAMIC_VALUE"
	ErrInvalidOperator          = "INVALID_OPERATOR"
	ErrInvalidPattern          = "INVALID_PATTERN"
	ErrInvalidRange            = "INVALID_RANGE"
	ErrInvalidValueType        = "INVALID_VALUE_TYPE"

	// Dependency errors
	ErrEmptyTargetValues       = "EMPTY_TARGET_VALUES"
	ErrInvalidConstraintType   = "INVALID_CONSTRAINT_TYPE"
	ErrMissingDynamicValue     = "MISSING_DYNAMIC_VALUE"
	ErrInvalidSourceAttribute  = "INVALID_SOURCE_ATTRIBUTE"
	ErrInvalidTargetAttribute  = "INVALID_TARGET_ATTRIBUTE"
	ErrDifferentTypeDefinition = "DIFFERENT_TYPE_DEFINITION"
	ErrDifferentVersion        = "DIFFERENT_VERSION"
)

// DynamicValue errors
func NewInvalidDynamicValueTypeError(valueType DynamicValueType) *DomainError {
	return NewDomainError(
		ErrInvalidDynamicValueType,
		fmt.Sprintf("invalid dynamic value type: %s", valueType),
		map[string]interface{}{"type": valueType},
	)
}

func NewInvalidRelativeTimeParamsError() *DomainError {
	return NewDomainError(
		ErrInvalidRelativeTimeParams,
		"invalid relative time parameters",
		nil,
	)
}

func NewInvalidPeriodError(period string) *DomainError {
	return NewDomainError(
		ErrInvalidPeriod,
		fmt.Sprintf("invalid period: %s", period),
		map[string]interface{}{"period": period},
	)
}

func NewInvalidAmountError() *DomainError {
	return NewDomainError(
		ErrInvalidAmount,
		"invalid amount",
		nil,
	)
}

func NewUnknownPeriodError(period string) *DomainError {
	return NewDomainError(
		ErrUnknownPeriod,
		fmt.Sprintf("unknown period: %s", period),
		map[string]interface{}{"period": period},
	)
}

func NewAttributeValueLookupNotImplementedError() *DomainError {
	return NewDomainError(
		ErrAttributeValueLookupNotImpl,
		"attribute value lookup not implemented",
		nil,
	)
}

// Condition errors
func NewInvalidConditionTypeError(conditionType ConditionType) *DomainError {
	return NewDomainError(
		ErrInvalidConditionType,
		fmt.Sprintf("invalid condition type: %s", conditionType),
		map[string]interface{}{"type": conditionType},
	)
}

func NewInvalidDynamicValueError() *DomainError {
	return NewDomainError(
		ErrInvalidDynamicValue,
		"dynamic value is invalid",
		nil,
	)
}

func NewInvalidOperatorError(operator string) *DomainError {
	return NewDomainError(
		ErrInvalidOperator,
		fmt.Sprintf("invalid operator: %s", operator),
		map[string]interface{}{"operator": operator},
	)
}

func NewInvalidPatternError() *DomainError {
	return NewDomainError(
		ErrInvalidPattern,
		"invalid pattern",
		nil,
	)
}

func NewInvalidRangeError() *DomainError {
	return NewDomainError(
		ErrInvalidRange,
		"invalid range",
		nil,
	)
}

func NewInvalidValueTypeError(value interface{}) *DomainError {
	return NewDomainError(
		ErrInvalidValueType,
		fmt.Sprintf("invalid value type: %T", value),
		map[string]interface{}{"value": value},
	)
}

// Dependency errors
func NewEmptyTargetValuesError() *DomainError {
	return NewDomainError(
		ErrEmptyTargetValues,
		"target values cannot be empty",
		nil,
	)
}

func NewInvalidConstraintTypeError(constraintType ConditionType) *DomainError {
	return NewDomainError(
		ErrInvalidConstraintType,
		fmt.Sprintf("invalid constraint type: %s", constraintType),
		map[string]interface{}{"type": constraintType},
	)
}

func NewMissingDynamicValueError() *DomainError {
	return NewDomainError(
		ErrMissingDynamicValue,
		"dynamic constraints must have a dynamic value",
		nil,
	)
}

func NewInvalidSourceAttributeError(attributeID ulid.ULID) *DomainError {
	return NewDomainError(
		ErrInvalidSourceAttribute,
		fmt.Sprintf("source attribute not found: %s", attributeID),
		map[string]interface{}{"attribute_id": attributeID},
	)
}

func NewInvalidTargetAttributeError(attributeID ulid.ULID) *DomainError {
	return NewDomainError(
		ErrInvalidTargetAttribute,
		fmt.Sprintf("target attribute not found: %s", attributeID),
		map[string]interface{}{"attribute_id": attributeID},
	)
}

func NewDifferentTypeDefinitionError(sourceID, targetID ulid.ULID) *DomainError {
	return NewDomainError(
		ErrDifferentTypeDefinition,
		"source and target attributes must belong to the same type definition",
		map[string]interface{}{
			"source_attribute_id": sourceID,
			"target_attribute_id": targetID,
		},
	)
}

func NewDifferentVersionError(sourceVersion, targetVersion int) *DomainError {
	return NewDomainError(
		ErrDifferentVersion,
		"source and target attributes must have the same version",
		map[string]interface{}{
			"source_version": sourceVersion,
			"target_version": targetVersion,
		},
	)
} 