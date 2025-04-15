package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Instance represents an instance of a type with attribute values
type Instance struct {
	ID             string
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
		TypeDefinition: typeDef,
		TypeVersion:    typeDef.Version,
		Attributes:     make(map[string]interface{}),
		CreatedAt:      now,
		UpdatedAt:      now,
	}
}

// SetAttribute sets an attribute value on the instance
func (i *Instance) SetAttribute(name string, value interface{}) error {
	// Find attribute definition
	attrDef := i.findAttributeDefinition(name)
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
	attrDef := i.findAttributeDefinition(name)
	if attrDef != nil && attrDef.DefaultValue != nil {
		return attrDef.DefaultValue, nil
	}

	return nil, fmt.Errorf("attribute '%s' not found", name)
}

// Validate validates all required attributes are present and valid
func (i *Instance) Validate() []error {
	errors := make([]error, 0)

	// Get all attribute definitions including inherited ones
	allAttrs := i.TypeDefinition.GetAllAttributes()

	// First pass: check all required attributes that are not disabled
	for _, attrDef := range allAttrs {
		// Skip disabled attributes
		if attrDef.Disabled {
			continue
		}

		if attrDef.Required {
			// Check if attribute is set
			value, exists := i.Attributes[attrDef.Name]
			if !exists && attrDef.DefaultValue == nil {
				errors = append(errors, fmt.Errorf("required attribute '%s' is missing", attrDef.Name))
				continue
			}

			// If value exists, validate it
			if exists {
				validationErrors := attrDef.Validate(value)
				errors = append(errors, validationErrors...)
			}
		}
	}

	// Second pass: evaluate cascade logic expressions for non-disabled attributes
	for _, attrDef := range allAttrs {
		// Skip disabled attributes
		if attrDef.Disabled {
			continue
		}

		// Get all enabled cascades for this attribute, sorted by weight (highest first)
		enabledCascades := attrDef.GetCascades()

		// Process each cascade in weight order
		for _, cascade := range enabledCascades {
			if cascade.Logic != "" {
				expr := NewExpression(cascade.Logic)
				result, err := expr.Evaluate(i)

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
									targetAttrDef := i.findAttributeDefinition(attrName)
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
	}

	return errors
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
func (i *Instance) MigrateToLatestVersion() []error {
	// Check if instance is already at the latest version
	if i.TypeVersion == i.TypeDefinition.Version {
		return nil // Already at the latest version
	}

	// Store the original version for error reporting
	oldVersion := i.TypeVersion

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

// findAttributeDefinition finds the attribute definition for a given name
func (i *Instance) findAttributeDefinition(name string) *AttributeDefinition {
	for _, attr := range i.TypeDefinition.GetAllAttributes() {
		if attr.Name == name {
			return attr
		}
	}

	return nil
}
