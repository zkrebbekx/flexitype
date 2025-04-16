package validation

import (
	"fmt"
)

// DynamicRuleFactory creates rules based on configuration
type DynamicRuleFactory struct {
	// State goes here
}

// NewDynamicRuleFactory creates a new dynamic rule factory
func NewDynamicRuleFactory() *DynamicRuleFactory {
	return &DynamicRuleFactory{}
}

// CreateRequiredRule creates a rule that makes a field required
func (f *DynamicRuleFactory) CreateRequiredRule() Rule {
	return &RequiredRule{}
}

// CreateEnumRule creates a rule that validates against a set of allowed values
func (f *DynamicRuleFactory) CreateEnumRule(allowedValues []interface{}) Rule {
	return &EnumRule{
		AllowedValues: allowedValues,
	}
}

// CreateRangeRule creates a rule for numeric range validation
func (f *DynamicRuleFactory) CreateRangeRule(min, max *float64) Rule {
	return &RangeRule{
		Min: min,
		Max: max,
	}
}

// CreateMinLengthRule creates a rule for minimum string length validation
func (f *DynamicRuleFactory) CreateMinLengthRule(minLength int) Rule {
	return &MinLengthRule{
		MinLength: minLength,
	}
}

// CreateMaxLengthRule creates a rule for maximum string length validation
func (f *DynamicRuleFactory) CreateMaxLengthRule(maxLength int) Rule {
	return &MaxLengthRule{
		MaxLength: maxLength,
	}
}

// CreatePatternRule creates a rule for string pattern validation
func (f *DynamicRuleFactory) CreatePatternRule(pattern string) (Rule, error) {
	return NewPatternRule(pattern)
}

// ApplyRuleAction creates or updates a rule based on an action and parameters
func (f *DynamicRuleFactory) ApplyRuleAction(action string, currentRules []Rule, params map[string]interface{}) ([]Rule, error) {
	// Start with the current rules
	resultRules := make([]Rule, 0, len(currentRules))
	
	// First, handle actions that completely replace rules of a certain type
	switch action {
	case "make_required":
		// Remove any RequiredRule and add a new one
		for _, rule := range currentRules {
			if _, isRequired := rule.(*RequiredRule); !isRequired {
				resultRules = append(resultRules, rule)
			}
		}
		resultRules = append(resultRules, f.CreateRequiredRule())
		return resultRules, nil
		
	case "make_optional":
		// Remove any RequiredRule
		for _, rule := range currentRules {
			if _, isRequired := rule.(*RequiredRule); !isRequired {
				resultRules = append(resultRules, rule)
			}
		}
		return resultRules, nil
		
	case "set_enum_values":
		// Get values from params
		valuesParam, ok := params["values"]
		if !ok {
			return nil, fmt.Errorf("missing 'values' parameter for set_enum_values action")
		}
		
		values, ok := valuesParam.([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid type for 'values' parameter: expected []interface{}")
		}
		
		// Remove any EnumRule and add a new one
		for _, rule := range currentRules {
			if _, isEnum := rule.(*EnumRule); !isEnum {
				resultRules = append(resultRules, rule)
			}
		}
		resultRules = append(resultRules, f.CreateEnumRule(values))
		return resultRules, nil
		
	case "add_enum_values":
		// Get values from params
		valuesParam, ok := params["values"]
		if !ok {
			return nil, fmt.Errorf("missing 'values' parameter for add_enum_values action")
		}
		
		valuesToAdd, ok := valuesParam.([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid type for 'values' parameter: expected []interface{}")
		}
		
		// Find any existing EnumRule, add values to it, or create a new one
		var existingEnum *EnumRule
		for _, rule := range currentRules {
			if enum, isEnum := rule.(*EnumRule); isEnum {
				existingEnum = enum
				break
			}
		}
		
		if existingEnum != nil {
			// Add values to existing enum
			newValues := make([]interface{}, len(existingEnum.AllowedValues))
			copy(newValues, existingEnum.AllowedValues)
			
			// Add new values, avoiding duplicates
			for _, newVal := range valuesToAdd {
				found := false
				for _, existingVal := range newValues {
					if fmt.Sprintf("%v", newVal) == fmt.Sprintf("%v", existingVal) {
						found = true
						break
					}
				}
				if !found {
					newValues = append(newValues, newVal)
				}
			}
			
			// Copy other rules and add updated enum rule
			for _, rule := range currentRules {
				if _, isEnum := rule.(*EnumRule); !isEnum {
					resultRules = append(resultRules, rule)
				}
			}
			resultRules = append(resultRules, f.CreateEnumRule(newValues))
		} else {
			// Just keep existing rules and add a new enum rule
			resultRules = append(resultRules, currentRules...)
			resultRules = append(resultRules, f.CreateEnumRule(valuesToAdd))
		}
		return resultRules, nil
		
	case "remove_enum_values":
		// Get values from params
		valuesParam, ok := params["values"]
		if !ok {
			return nil, fmt.Errorf("missing 'values' parameter for remove_enum_values action")
		}
		
		valuesToRemove, ok := valuesParam.([]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid type for 'values' parameter: expected []interface{}")
		}
		
		// Find any existing EnumRule and remove values from it
		var existingEnum *EnumRule
		for _, rule := range currentRules {
			if enum, isEnum := rule.(*EnumRule); isEnum {
				existingEnum = enum
				break
			}
		}
		
		if existingEnum != nil {
			// Create a map for quick lookup of values to remove
			removeMap := make(map[string]bool)
			for _, val := range valuesToRemove {
				removeMap[fmt.Sprintf("%v", val)] = true
			}
			
			// Filter out values that should be removed
			newValues := make([]interface{}, 0, len(existingEnum.AllowedValues))
			for _, val := range existingEnum.AllowedValues {
				if !removeMap[fmt.Sprintf("%v", val)] {
					newValues = append(newValues, val)
				}
			}
			
			// Copy other rules and add updated enum rule
			for _, rule := range currentRules {
				if _, isEnum := rule.(*EnumRule); !isEnum {
					resultRules = append(resultRules, rule)
				}
			}
			
			// Only add the enum rule if there are values left
			if len(newValues) > 0 {
				resultRules = append(resultRules, f.CreateEnumRule(newValues))
			}
		} else {
			// No enum rule to modify, just return existing rules
			return currentRules, nil
		}
		return resultRules, nil
		
	case "set_min_value":
		// Get value from params
		valueParam, ok := params["value"]
		if !ok {
			return nil, fmt.Errorf("missing 'value' parameter for set_min_value action")
		}
		
		value, ok := valueParam.(float64)
		if !ok {
			// Try to convert from int
			if intVal, isInt := valueParam.(int); isInt {
				value = float64(intVal)
			} else {
				return nil, fmt.Errorf("invalid type for 'value' parameter: expected numeric value")
			}
		}
		
		// Find any existing RangeRule and update min value
		var existingRange *RangeRule
		for _, rule := range currentRules {
			if rangeRule, isRange := rule.(*RangeRule); isRange {
				existingRange = rangeRule
				break
			}
		}
		
		if existingRange != nil {
			// Update the min value in the existing rule
			min := value
			max := existingRange.Max
			
			// Copy other rules and add updated range rule
			for _, rule := range currentRules {
				if _, isRange := rule.(*RangeRule); !isRange {
					resultRules = append(resultRules, rule)
				}
			}
			resultRules = append(resultRules, f.CreateRangeRule(&min, max))
		} else {
			// Just keep existing rules and add a new range rule
			resultRules = append(resultRules, currentRules...)
			min := value
			resultRules = append(resultRules, f.CreateRangeRule(&min, nil))
		}
		return resultRules, nil
		
	case "set_max_value":
		// Get value from params
		valueParam, ok := params["value"]
		if !ok {
			return nil, fmt.Errorf("missing 'value' parameter for set_max_value action")
		}
		
		value, ok := valueParam.(float64)
		if !ok {
			// Try to convert from int
			if intVal, isInt := valueParam.(int); isInt {
				value = float64(intVal)
			} else {
				return nil, fmt.Errorf("invalid type for 'value' parameter: expected numeric value")
			}
		}
		
		// Find any existing RangeRule and update max value
		var existingRange *RangeRule
		for _, rule := range currentRules {
			if rangeRule, isRange := rule.(*RangeRule); isRange {
				existingRange = rangeRule
				break
			}
		}
		
		if existingRange != nil {
			// Update the max value in the existing rule
			min := existingRange.Min
			max := value
			
			// Copy other rules and add updated range rule
			for _, rule := range currentRules {
				if _, isRange := rule.(*RangeRule); !isRange {
					resultRules = append(resultRules, rule)
				}
			}
			resultRules = append(resultRules, f.CreateRangeRule(min, &max))
		} else {
			// Just keep existing rules and add a new range rule
			resultRules = append(resultRules, currentRules...)
			max := value
			resultRules = append(resultRules, f.CreateRangeRule(nil, &max))
		}
		return resultRules, nil
		
	case "set_min_length":
		// Get value from params
		valueParam, ok := params["value"]
		if !ok {
			return nil, fmt.Errorf("missing 'value' parameter for set_min_length action")
		}
		
		var intValue int
		if floatVal, isFloat := valueParam.(float64); isFloat {
			intValue = int(floatVal)
		} else if intVal, isInt := valueParam.(int); isInt {
			intValue = intVal
		} else {
			return nil, fmt.Errorf("invalid type for 'value' parameter: expected numeric value")
		}
		
		// Remove any existing MinLengthRule and add a new one
		for _, rule := range currentRules {
			if _, isMinLength := rule.(*MinLengthRule); !isMinLength {
				resultRules = append(resultRules, rule)
			}
		}
		resultRules = append(resultRules, f.CreateMinLengthRule(intValue))
		return resultRules, nil
		
	case "set_max_length":
		// Get value from params
		valueParam, ok := params["value"]
		if !ok {
			return nil, fmt.Errorf("missing 'value' parameter for set_max_length action")
		}
		
		var intValue int
		if floatVal, isFloat := valueParam.(float64); isFloat {
			intValue = int(floatVal)
		} else if intVal, isInt := valueParam.(int); isInt {
			intValue = intVal
		} else {
			return nil, fmt.Errorf("invalid type for 'value' parameter: expected numeric value")
		}
		
		// Remove any existing MaxLengthRule and add a new one
		for _, rule := range currentRules {
			if _, isMaxLength := rule.(*MaxLengthRule); !isMaxLength {
				resultRules = append(resultRules, rule)
			}
		}
		resultRules = append(resultRules, f.CreateMaxLengthRule(intValue))
		return resultRules, nil
		
	case "set_pattern":
		// Get value from params
		patternParam, ok := params["pattern"]
		if !ok {
			return nil, fmt.Errorf("missing 'pattern' parameter for set_pattern action")
		}
		
		pattern, ok := patternParam.(string)
		if !ok {
			return nil, fmt.Errorf("invalid type for 'pattern' parameter: expected string")
		}
		
		// Create the pattern rule
		patternRule, err := f.CreatePatternRule(pattern)
		if err != nil {
			return nil, fmt.Errorf("failed to create pattern rule: %w", err)
		}
		
		// Remove any existing PatternRule and add the new one
		for _, rule := range currentRules {
			if _, isPattern := rule.(*PatternRule); !isPattern {
				resultRules = append(resultRules, rule)
			}
		}
		resultRules = append(resultRules, patternRule)
		return resultRules, nil
		
	default:
		return nil, fmt.Errorf("unsupported rule action: %s", action)
	}
}