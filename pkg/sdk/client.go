package sdk

import (
	"context"
	"fmt"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// DataType string constants for gRPC API usage
const (
	// Data type string constants for gRPC API
	DataTypeString  = "string"
	DataTypeInt     = "int"
	DataTypeFloat   = "float"
	DataTypeBoolean = "boolean"
	DataTypeDate    = "date"
	DataTypeObject  = "object"
	DataTypeArray   = "array"
)

// All constants have been moved to helper.go

// Client provides a high-level API for working with FlexiType
type Client struct {
	typeRepo     ports.TypeRepository
	instanceRepo ports.InstanceRepository
}

// NewClient creates a new FlexiType client
func NewClient(typeRepo ports.TypeRepository, instanceRepo ports.InstanceRepository) *Client {
	return &Client{
		typeRepo:     typeRepo,
		instanceRepo: instanceRepo,
	}
}

// SaveType creates a new type definition
func (c *Client) SaveType(ctx context.Context, name, description string) (*core.TypeDefinition, error) {
	typeDef := core.NewTypeDefinition(name, description)
	err := c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, err
	}

	return typeDef, nil
}

// GetType retrieves a type definition by ID
func (c *Client) GetType(ctx context.Context, name string) (*core.TypeDefinition, error) {
	return c.typeRepo.GetByName(ctx, name)
}

// AddAttribute adds an attribute to a type definition
func (c *Client) AddAttribute(ctx context.Context, typeName string, attr *core.AttributeDefinition) error {
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return err
	}

	typeDef.AddAttribute(attr)

	// Validate cascades to ensure they reference existing attributes and have no circular dependencies
	if errors := typeDef.ValidateCascades(); len(errors) > 0 {
		// Combine all validation errors into a single message
		errorMsgs := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMsgs = append(errorMsgs, err.Error())
		}
		return fmt.Errorf("cascade validation failed: %s", errorMsgs)
	}

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	return c.typeRepo.Save(ctx, typeDef)
}

// SaveInstance creates a new instance of a type
func (c *Client) SaveInstance(ctx context.Context, id string, typeDef *core.TypeDefinition, attributes map[string]interface{}) (*core.Instance, error) {
	instance := core.NewInstance(id, typeDef)

	// Set all provided attributes
	for name, value := range attributes {
		err := instance.SetAttribute(name, value)
		if err != nil {
			return nil, err
		}
	}

	// Validate the instance
	errs := instance.Validate()
	if len(errs) > 0 {
		return nil, errs[0] // Return first error for simplicity
	}

	// Save the instance
	err := c.instanceRepo.Save(ctx, instance)
	if err != nil {
		return nil, err
	}

	return instance, nil
}

// GetInstance retrieves an instance by ID
func (c *Client) GetInstance(ctx context.Context, id string) (*core.Instance, error) {
	return c.instanceRepo.GetByID(ctx, id)
}

// QueryInstances queries instances by type and attribute filters
func (c *Client) QueryInstances(ctx context.Context, typeName string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	return c.instanceRepo.Query(ctx, typeName, attributeFilters)
}

// SetAttributeDisabledState enables or disables an attribute on a type definition
func (c *Client) SetAttributeDisabledState(ctx context.Context, typeName, attributeName string, disabled bool) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeName, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeName, typeName)
	}

	// If the disabled state is already what we want, no need to update
	if foundAttr.Disabled == disabled {
		return typeDef, nil
	}

	// Update the disabled state
	foundAttr.SetDisabled(disabled)

	// If we're disabling an attribute, we need to validate that it's not referenced by any cascades
	if disabled {
		// Validate cascades to ensure they don't reference this newly disabled attribute
		if errors := typeDef.ValidateCascades(); len(errors) > 0 {
			// Revert the change since validation failed
			foundAttr.SetDisabled(!disabled)

			// Combine validation errors
			errorMsgs := make([]string, 0, len(errors))
			for _, err := range errors {
				errorMsgs = append(errorMsgs, err.Error())
			}
			return nil, fmt.Errorf("cannot disable attribute: %s", errorMsgs)
		}
	}

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// AddCascadeToAttribute adds a cascade with specified parameters to an attribute
func (c *Client) AddCascadeToAttribute(ctx context.Context, typeName, attributeName string, enabled bool, behavior core.CascadeBehavior, logic string, weight int) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeName, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeName, typeName)
	}

	// Add the cascade using logic as the ID for simplicity
	foundAttr.AddCascade(logic, enabled, behavior, logic, weight)

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// AddValidationCascade adds a cascade that modifies validation rules based on conditions
func (c *Client) AddValidationCascade(ctx context.Context, typeName, attributeName, cascadeID string, enabled bool,
	behavior core.CascadeBehavior, logic string, weight int, config *core.CascadeValidationConfig) (*core.TypeDefinition, error) {

	// Get the type
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeName, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeName, typeName)
	}

	// Add the validation cascade
	foundAttr.AddValidationCascade(cascadeID, enabled, behavior, logic, weight, config)

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// AddRequirementCascade adds a cascade that makes a field required/optional based on conditions
func (c *Client) AddRequirementCascade(ctx context.Context, typeName, attributeName, cascadeID string,
	enabled bool, logic string, weight int, targetField string, makeRequired bool) (*core.TypeDefinition, error) {

	action := core.ActionMakeOptional
	if makeRequired {
		action = core.ActionMakeRequired
	}

	cfg := &core.CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
	}

	return c.AddValidationCascade(ctx, typeName, attributeName, cascadeID, enabled,
		core.CascadeRequirement, logic, weight, cfg)
}

// AddEnumValuesCascade adds a cascade that modifies enum values based on conditions
func (c *Client) AddEnumValuesCascade(ctx context.Context, typeName, attributeName, cascadeID string,
	enabled bool, logic string, weight int, targetField string, action core.CascadeValidationAction,
	values []interface{}) (*core.TypeDefinition, error) {

	cfg := &core.CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
		Values:      values,
	}

	return c.AddValidationCascade(ctx, typeName, attributeName, cascadeID, enabled,
		core.CascadeEnumValues, logic, weight, cfg)
}

// AddNumericConstraintCascade adds a cascade that sets min/max values for numeric fields
func (c *Client) AddNumericConstraintCascade(ctx context.Context, typeName, attributeName, cascadeID string,
	enabled bool, logic string, weight int, targetField string, action core.CascadeValidationAction,
	value float64) (*core.TypeDefinition, error) {

	cfg := &core.CascadeValidationConfig{
		Action:       action,
		TargetField:  targetField,
		NumericValue: value,
	}

	return c.AddValidationCascade(ctx, typeName, attributeName, cascadeID, enabled,
		core.CascadeValidation, logic, weight, cfg)
}

// AddStringConstraintCascade adds a cascade that sets string constraints (min/max length, pattern)
func (c *Client) AddStringConstraintCascade(ctx context.Context, typeName, attributeName, cascadeID string,
	enabled bool, logic string, weight int, targetField string, action core.CascadeValidationAction,
	value interface{}) (*core.TypeDefinition, error) {

	cfg := &core.CascadeValidationConfig{
		Action:      action,
		TargetField: targetField,
	}

	// Handle different actions
	switch action {
	case core.ActionSetMinLength, core.ActionSetMaxLength:
		if numValue, ok := value.(int); ok {
			cfg.NumericValue = float64(numValue)
		} else if floatValue, ok := value.(float64); ok {
			cfg.NumericValue = floatValue
		} else {
			return nil, fmt.Errorf("expected numeric value for min/max length but got %T", value)
		}
	case core.ActionSetPattern:
		if strValue, ok := value.(string); ok {
			cfg.StringValue = strValue
		} else {
			return nil, fmt.Errorf("expected string value for pattern but got %T", value)
		}
	default:
		return nil, fmt.Errorf("unsupported string constraint action: %s", action)
	}

	return c.AddValidationCascade(ctx, typeName, attributeName, cascadeID, enabled,
		core.CascadeValidation, logic, weight, cfg)
}

// AddDefaultValueCascade adds a cascade that sets a default value based on conditions
func (c *Client) AddDefaultValueCascade(ctx context.Context, typeName, attributeName, cascadeID string,
	enabled bool, logic string, weight int, targetField string,
	defaultValue interface{}) (*core.TypeDefinition, error) {

	cfg := &core.CascadeValidationConfig{
		Action:      core.ActionSetDefaultValue,
		TargetField: targetField,
		Values:      []interface{}{defaultValue},
	}

	return c.AddValidationCascade(ctx, typeName, attributeName, cascadeID, enabled,
		core.CascadeDefaultValue, logic, weight, cfg)
}

// RemoveCascadeFromAttribute removes a cascade with the specified logic from an attribute
func (c *Client) RemoveCascadeFromAttribute(ctx context.Context, typeName, attributeName, logic string) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeName, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeName, typeName)
	}

	// Remove the cascade
	foundAttr.RemoveCascade(logic)

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// UpdateInstance updates an existing instance with new attributes
func (c *Client) UpdateInstance(ctx context.Context, id string, attributes map[string]interface{}) (*core.Instance, error) {
	// Get the existing instance
	instance, err := c.instanceRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Get the latest type definition
	latestTypeDef, err := c.typeRepo.GetByName(ctx, instance.TypeDefinition.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get latest type definition: %w", err)
	}

	// Update the instance to use the latest type definition
	instance.TypeDefinition = latestTypeDef

	// Check if type version has changed
	if instance.TypeVersion != latestTypeDef.Version {
		// Migrate the instance to the latest type version
		migrationErrors := instance.MigrateToLatestVersion()
		if len(migrationErrors) > 0 {
			return nil, fmt.Errorf("migration to type version %d failed: %v",
				latestTypeDef.Version, migrationErrors)
		}
	}

	// Update the instance's attributes
	for name, value := range attributes {
		err := instance.SetAttribute(name, value)
		if err != nil {
			return nil, fmt.Errorf("failed to set attribute '%s': %w", name, err)
		}
	}

	// Validate the instance
	errors := instance.Validate()
	if len(errors) > 0 {
		return nil, fmt.Errorf("validation failed: %v", errors)
	}

	// Save the updated instance
	err = c.instanceRepo.Save(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("failed to save instance: %w", err)
	}

	return instance, nil
}

// DeleteAttribute removes an attribute from a type definition
func (c *Client) DeleteAttribute(ctx context.Context, typeName string, attributeName string) error {
	// Get the type
	typeDef, err := c.typeRepo.GetByName(ctx, typeName)
	if err != nil {
		return fmt.Errorf("type with ID '%s' not found: %w", typeName, err)
	}

	// Find the attribute to be deleted
	var attributeToDelete *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			attributeToDelete = attr
			break
		}
	}

	if attributeToDelete == nil {
		return fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeName, typeName)
	}

	// Before removing, check if any other attributes reference this one in their cascades
	// Temporarily disable the attribute (to simulate removal for validation)
	attributeToDelete.SetDisabled(true)

	// Validate cascades to ensure they don't reference this soon-to-be-deleted attribute
	if errors := typeDef.ValidateCascades(); len(errors) > 0 {
		// Revert the temporary change
		attributeToDelete.SetDisabled(false)

		// Combine all validation errors into a single message
		errorMsgs := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMsgs = append(errorMsgs, err.Error())
		}
		return fmt.Errorf("cannot delete attribute: %s", errorMsgs)
	}

	// Now actually remove the attribute
	newAttributes := make([]*core.AttributeDefinition, 0, len(typeDef.Attributes))
	for _, attr := range typeDef.Attributes {
		if attr.Name != attributeName {
			newAttributes = append(newAttributes, attr)
		}
	}

	typeDef.Attributes = newAttributes

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	return c.typeRepo.Save(ctx, typeDef)
}
