package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zac300/flexitype/internal/domain/validation"
)

// Instance represents an instance of a type with attribute values
type Instance struct {
	ID             string // External ID used by consumers to identify the instance
	Version        int    // Internal version number for the instance
	TypeDefinition *TypeDefinition
	TypeVersion    int // Stores the version of the type definition this instance was created with
	Attributes     map[string]interface{}
	CreatedAt      time.Time  // When the instance was created
	UpdatedAt      time.Time  // When the instance was last updated
	ArchivedAt     *time.Time // Nullable timestamp when the instance was archived
}

// NewInstance creates a new instance of a type
func NewInstance(id string, typeDef *TypeDefinition) *Instance {
	now := time.Now()
	return &Instance{
		ID:             id,
		Version:        1, // Initial version is 1
		TypeDefinition: typeDef,
		TypeVersion:    typeDef.Version,
		Attributes:     make(map[string]interface{}),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// NewInstanceVersion creates a new version of an existing instance
func NewInstanceVersion(existingInstance *Instance, newVersion int) *Instance {
	now := time.Now()
	return &Instance{
		ID:             existingInstance.ID, // Maintain the same ID
		Version:        newVersion,          // Set the new version number
		TypeDefinition: existingInstance.TypeDefinition,
		TypeVersion:    existingInstance.TypeVersion,
		Attributes:     make(map[string]interface{}),
		CreatedAt:      existingInstance.CreatedAt, // Preserve original creation time
		UpdatedAt:      now,
	}
}

// SetAttribute sets an attribute value on the instance
func (i *Instance) SetAttribute(name string, value interface{}) error {
	// Find attribute definition
	attrDef := i.FindAttributeDefinition(name)
	if attrDef == nil {
		return fmt.Errorf("attribute '%s' is not defined for this type", name)
	}

	// Check if the attribute is disabled
	if attrDef.Disabled {
		return fmt.Errorf("attribute '%s' is disabled and cannot be set", name)
	}

	// Handle multi-valued attributes
	if attrDef.MultiValued {
		// For multi-valued attributes, we store a slice
		currentValue, exists := i.Attributes[name]

		// Check if the provided value is a slice
		valueSlice, isSlice := value.([]interface{})
		if isSlice {
			// If provided a slice directly, validate each item
			for _, item := range valueSlice {
				errors := attrDef.Validate(item)
				if len(errors) > 0 {
					return fmt.Errorf("validation failed for an item in multi-valued attribute '%s': %v", name, errors)
				}
			}

			i.Attributes[name] = value
			i.UpdatedAt = time.Now()
			return nil
		} else {
			// Single value provided for multi-valued attribute
			// Validate the value
			errors := attrDef.Validate(value)
			if len(errors) > 0 {
				return fmt.Errorf("validation failed for attribute '%s': %v", name, errors)
			}

			// If attribute doesn't exist yet, create new slice
			if !exists {
				i.Attributes[name] = []interface{}{value}
				i.UpdatedAt = time.Now()
				return nil
			}

			// Otherwise append to existing slice
			currentSlice, ok := currentValue.([]interface{})
			if !ok {
				return fmt.Errorf("attribute '%s' is multi-valued but current value is not a slice", name)
			}

			currentSlice = append(currentSlice, value)
			i.Attributes[name] = currentSlice
			i.UpdatedAt = time.Now()
			return nil
		}
	} else {
		// For single-valued attributes
		errors := attrDef.Validate(value)
		if len(errors) > 0 {
			return fmt.Errorf("validation failed for attribute '%s': %v", name, errors)
		}

		i.Attributes[name] = value
		i.UpdatedAt = time.Now()
		return nil
	}
}

// GetAttribute gets an attribute value from the instance
func (i *Instance) GetAttribute(name string) (interface{}, error) {
	// Check if we have the attribute
	value, exists := i.Attributes[name]
	if exists {
		return value, nil
	}

	// Check if it's defined but we're using default value
	attrDef := i.FindAttributeDefinition(name)
	if attrDef != nil && attrDef.DefaultValue != nil {
		return attrDef.DefaultValue, nil
	}

	return nil, fmt.Errorf("attribute '%s' not found", name)
}

// Validate validates all required attributes are present and valid
func (i *Instance) Validate() []error {
	errors := make([]error, 0)

	// Create a dynamic rule factory for validation modifications
	ruleFactory := validation.NewDynamicRuleFactory()

	// Track dynamically modified validation rules
	dynamicRules := make(map[string][]validation.Rule)

	// First pass: process validation-related cascades to modify validation rules
	// Get all attribute definitions including inherited ones
	allAttrs := i.TypeDefinition.GetAllAttributes()

	// Process validation cascades first
	for _, attrDef := range allAttrs {
		// Skip disabled attributes
		if attrDef.Disabled {
			continue
		}

		// Get all enabled cascades for this attribute, sorted by weight (highest first)
		enabledCascades := attrDef.GetCascades()

		// Process each cascade that modifies validation rules
		for _, cascade := range enabledCascades {
			if isValidationCascade(cascade.Behavior) && cascade.ValidationConfig != nil {
				// Evaluate the cascade condition
				var result bool
				var err error

				// Use custom expression if available, otherwise parse string logic
				if cascade.expression != nil {
					result, err = cascade.expression.Evaluate(i)
				} else if cascade.Logic != "" {
					expr := NewExpression(cascade.Logic)
					result, err = expr.Evaluate(i)
				} else {
					// No condition means always apply
					result = true
				}

				if err != nil {
					errors = append(errors, fmt.Errorf("error evaluating validation cascade logic (weight: %d) for attribute '%s': %w",
						cascade.Weight, attrDef.Name, err))
					continue
				}

				if result {
					// Condition is true, apply validation rule changes
					targetField := cascade.ValidationConfig.TargetField
					if targetField == "" {
						targetField = attrDef.Name // If no target specified, apply to self
					}

					// Find the target attribute
					targetAttrDef := i.FindAttributeDefinition(targetField)
					if targetAttrDef == nil || targetAttrDef.Disabled {
						errors = append(errors, fmt.Errorf("validation cascade target attribute '%s' not found or disabled", targetField))
						continue
					}

					// Get current rules for the target attribute
					// First check if we have already modified rules for this field
					currentRules, exists := dynamicRules[targetField]
					if !exists {
						// Use original rules from attribute definition
						currentRules = make([]validation.Rule, len(targetAttrDef.ValidationRules))
						copy(currentRules, targetAttrDef.ValidationRules)
						dynamicRules[targetField] = currentRules
					}

					// Apply the validation rule modifications
					switch cascade.ValidationConfig.Action {
					case ActionMakeRequired:
						// Mark the attribute as required
						params := map[string]interface{}{"value": true}
						newRules, err := ruleFactory.ApplyRuleAction("make_required", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error applying required validation: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionMakeOptional:
						// Mark the attribute as optional
						params := map[string]interface{}{"value": false}
						newRules, err := ruleFactory.ApplyRuleAction("make_optional", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error applying optional validation: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetEnumValues:
						// Replace enum values
						params := map[string]interface{}{"values": cascade.ValidationConfig.Values}
						newRules, err := ruleFactory.ApplyRuleAction("set_enum_values", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error applying enum values: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionAddEnumValues:
						// Add values to enum
						params := map[string]interface{}{"values": cascade.ValidationConfig.Values}
						newRules, err := ruleFactory.ApplyRuleAction("add_enum_values", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error adding enum values: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionRemoveEnumValues:
						// Remove values from enum
						params := map[string]interface{}{"values": cascade.ValidationConfig.Values}
						newRules, err := ruleFactory.ApplyRuleAction("remove_enum_values", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error removing enum values: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetMinValue:
						// Set min value for numeric fields
						params := map[string]interface{}{"value": cascade.ValidationConfig.NumericValue}
						newRules, err := ruleFactory.ApplyRuleAction("set_min_value", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error setting min value: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetMaxValue:
						// Set max value for numeric fields
						params := map[string]interface{}{"value": cascade.ValidationConfig.NumericValue}
						newRules, err := ruleFactory.ApplyRuleAction("set_max_value", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error setting max value: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetMinLength:
						// Set min length for string fields
						params := map[string]interface{}{"value": cascade.ValidationConfig.NumericValue}
						newRules, err := ruleFactory.ApplyRuleAction("set_min_length", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error setting min length: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetMaxLength:
						// Set max length for string fields
						params := map[string]interface{}{"value": cascade.ValidationConfig.NumericValue}
						newRules, err := ruleFactory.ApplyRuleAction("set_max_length", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error setting max length: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetPattern:
						// Set pattern for string fields
						params := map[string]interface{}{"pattern": cascade.ValidationConfig.StringValue}
						newRules, err := ruleFactory.ApplyRuleAction("set_pattern", currentRules, params)
						if err != nil {
							errors = append(errors, fmt.Errorf("error setting pattern: %w", err))
							continue
						}
						dynamicRules[targetField] = newRules

					case ActionSetDefaultValue:
						// Set default value
						if len(cascade.ValidationConfig.Values) > 0 {
							targetAttrDef.SetDefaultValue(cascade.ValidationConfig.Values[0])
						}
					}
				}
			}
		}
	}

	// Second pass: check required attributes and validate fields
	for _, attrDef := range allAttrs {
		// Skip disabled attributes
		if attrDef.Disabled {
			continue
		}

		// Check if validation rules were dynamically modified
		var rulesForValidation []validation.Rule
		dynamicRuleSet, hasDynamicRules := dynamicRules[attrDef.Name]

		if hasDynamicRules {
			rulesForValidation = dynamicRuleSet

			// Check if this attribute is dynamically required
			isRequired := false
			for _, rule := range dynamicRuleSet {
				if _, ok := rule.(*validation.RequiredRule); ok {
					isRequired = true
					break
				}
			}

			// If dynamically required, check that it's present
			if isRequired {
				value, exists := i.Attributes[attrDef.Name]
				if !exists && attrDef.DefaultValue == nil {
					errors = append(errors, fmt.Errorf("dynamically required attribute '%s' is missing", attrDef.Name))
					continue
				}

				// Validate if value exists
				if exists {
					for _, rule := range rulesForValidation {
						if err := rule.Validate(value); err != nil {
							errors = append(errors, fmt.Errorf("validation failed for attribute '%s': %w", attrDef.Name, err))
						}
					}
				}
			} else if attrDef.Required {
				// Still required by original definition
				value, exists := i.Attributes[attrDef.Name]
				if !exists && attrDef.DefaultValue == nil {
					errors = append(errors, fmt.Errorf("required attribute '%s' is missing", attrDef.Name))
					continue
				}

				// Validate if value exists
				if exists {
					for _, rule := range rulesForValidation {
						if err := rule.Validate(value); err != nil {
							errors = append(errors, fmt.Errorf("validation failed for attribute '%s': %w", attrDef.Name, err))
						}
					}
				}
			} else {
				// Optional field, validate only if it exists
				if value, exists := i.Attributes[attrDef.Name]; exists {
					for _, rule := range rulesForValidation {
						if err := rule.Validate(value); err != nil {
							errors = append(errors, fmt.Errorf("validation failed for attribute '%s': %w", attrDef.Name, err))
						}
					}
				}
			}
		} else {
			// No dynamic rules, use original validation
			if attrDef.Required {
				// Check if attribute is set
				value, exists := i.Attributes[attrDef.ID]
				if !exists && attrDef.DefaultValue == nil {
					errors = append(errors, fmt.Errorf("required attribute '%s' is missing", attrDef.Name))
					continue
				}

				// If value exists, validate it
				if exists {
					validationErrors := attrDef.Validate(value)
					errors = append(errors, validationErrors...)
				}
			} else {
				// Optional field, validate only if it exists
				if value, exists := i.Attributes[attrDef.Name]; exists {
					validationErrors := attrDef.Validate(value)
					errors = append(errors, validationErrors...)
				}
			}
		}
	}

	// Third pass: process cascades that set attribute values
	for _, attrDef := range allAttrs {
		// Skip disabled attributes
		if attrDef.Disabled {
			continue
		}

		// Get all enabled cascades for this attribute, sorted by weight (highest first)
		enabledCascades := attrDef.GetCascades()

		// Process each cascade in weight order
		for _, cascade := range enabledCascades {
			// Skip validation cascades as they were handled already
			if isValidationCascade(cascade.Behavior) {
				continue
			}

			// Evaluate the condition
			var result bool
			var err error

			// Use custom expression if available, otherwise parse string logic
			if cascade.expression != nil {
				result, err = cascade.expression.Evaluate(i)
			} else if cascade.Logic != "" {
				expr := NewExpression(cascade.Logic)
				result, err = expr.Evaluate(i)
			} else {
				// No condition means don't apply
				continue
			}

			if err != nil {
				errors = append(errors, fmt.Errorf("error evaluating cascade logic (weight: %d) for attribute '%s': %w",
					cascade.Weight, attrDef.Name, err))
			} else if result {
				// The cascade logic evaluated to true, which may need to set or modify attributes
				// For example, if the logic is "amount > 100", and it's true, we might need to
				// set another attribute like "signatureRequired = true"

				// Extract the consequence part from the logic (if it has one)
				if strings.Contains(cascade.Logic, "=>") {
					parts := strings.Split(cascade.Logic, "=>")
					if len(parts) == 2 {
						consequence := strings.TrimSpace(parts[1])
						// Parse the consequence (e.g., "signatureRequired = true")
						if strings.Contains(consequence, "=") {
							kv := strings.Split(consequence, "=")
							if len(kv) == 2 {
								attrName := strings.TrimSpace(kv[0])
								attrValue := parseExpressionValue(strings.TrimSpace(kv[1]))

								// Check if the target attribute is disabled
								targetAttrDef := i.FindAttributeDefinition(attrName)
								if targetAttrDef != nil && targetAttrDef.Disabled {
									// Skip setting disabled attributes
									continue
								}

								// Set the attribute value
								err := i.SetAttribute(attrName, attrValue)
								if err != nil {
									errors = append(errors, fmt.Errorf("error setting attribute '%s' from cascade logic (weight: %d): %w",
										attrName, cascade.Weight, err))
								}
							}
						}
					}
				}
			}
		}
	}

	return errors
}

// isValidationCascade checks if a cascade behavior is related to validation
func isValidationCascade(behavior CascadeBehavior) bool {
	return behavior == CascadeValidation ||
		behavior == CascadeRequirement ||
		behavior == CascadeEnumValues ||
		behavior == CascadeDefaultValue
}

// parseExpressionValue parses a value from a cascade logic expression
func parseExpressionValue(value string) interface{} {
	// Try to parse as number
	if f, err := strconv.ParseFloat(value, 64); err == nil {
		return f
	}

	// Check for boolean
	if value == "true" {
		return true
	}
	if value == "false" {
		return false
	}

	// Check for string literal
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return value[1 : len(value)-1]
	}

	// Default to treating as string
	return value
}

// MigrateToLatestVersion updates the instance to use the latest type definition version
// and validates the instance against the new type version
// It creates a new version by copying attributes from the previous version, removing disabled ones
func (i *Instance) MigrateToLatestVersion() []error {
	// Check if instance is already at the latest version
	if i.TypeVersion == i.TypeDefinition.Version {
		return nil // Already at the latest version
	}

	// Store the original version for error reporting
	oldVersion := i.TypeVersion

	// Get all active attributes in the new type version
	allAttributes := i.TypeDefinition.GetAllAttributes()
	activeAttributeNames := make(map[string]bool)
	for _, attr := range allAttributes {
		if !attr.Disabled {
			activeAttributeNames[attr.Name] = true
		}
	}

	// Create a new attributes map, only including values for attributes that are still active
	newAttributes := make(map[string]interface{})
	for attrName, value := range i.Attributes {
		if activeAttributeNames[attrName] {
			// Only copy attributes that are still active in the new type version
			newAttributes[attrName] = value
		}
	}

	// Replace the attributes with the filtered set
	i.Attributes = newAttributes

	// Update the instance type version to match the current type definition
	i.TypeVersion = i.TypeDefinition.Version
	i.UpdatedAt = time.Now()

	// Validate the instance against the new type version
	errors := i.Validate()
	if len(errors) > 0 {
		// Add context about version migration to each error
		for idx, err := range errors {
			errors[idx] = fmt.Errorf("migration from version %d to %d: %w",
				oldVersion, i.TypeDefinition.Version, err)
		}
	}

	return errors
}

// FindAttributeDefinition finds the attribute definition for a given name
func (i *Instance) FindAttributeDefinition(idOrName string) *AttributeDefinition {
	for _, attr := range i.TypeDefinition.GetAllAttributes() {
		if attr.ID == idOrName || attr.Name == idOrName {
			return attr
		}
	}

	return nil
}
