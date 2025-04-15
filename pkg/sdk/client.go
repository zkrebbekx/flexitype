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

// CreateType creates a new type definition
func (c *Client) CreateType(ctx context.Context, id, name, description string) (*core.TypeDefinition, error) {
	typeDef := core.NewTypeDefinition(id, name, description)
	err := c.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, err
	}

	return typeDef, nil
}

// GetType retrieves a type definition by ID
func (c *Client) GetType(ctx context.Context, id string) (*core.TypeDefinition, error) {
	return c.typeRepo.GetByID(ctx, id)
}

// AddAttribute adds an attribute to a type definition
func (c *Client) AddAttribute(ctx context.Context, typeID string, attr *core.AttributeDefinition) error {
	typeDef, err := c.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return err
	}

	typeDef.AddAttribute(attr)
	return c.typeRepo.Save(ctx, typeDef)
}

// CreateInstance creates a new instance of a type
func (c *Client) CreateInstance(ctx context.Context, id string, typeDef *core.TypeDefinition, attributes map[string]interface{}) (*core.Instance, error) {
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
func (c *Client) QueryInstances(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	return c.instanceRepo.Query(ctx, typeID, attributeFilters)
}

// SetAttributeDisabledState enables or disables an attribute on a type definition
func (c *Client) SetAttributeDisabledState(ctx context.Context, typeID, attributeID string, disabled bool) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeID, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.ID == attributeID {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeID, typeID)
	}

	// Update the disabled state
	foundAttr.SetDisabled(disabled)

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
func (c *Client) AddCascadeToAttribute(ctx context.Context, typeID, attributeID string, enabled bool, behavior core.CascadeBehavior, logic string, weight int) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeID, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.ID == attributeID {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeID, typeID)
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

// RemoveCascadeFromAttribute removes a cascade with the specified logic from an attribute
func (c *Client) RemoveCascadeFromAttribute(ctx context.Context, typeID, attributeID, logic string) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := c.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found: %w", typeID, err)
	}

	// Find the attribute
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.ID == attributeID {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with ID '%s' not found in type '%s'", attributeID, typeID)
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
	latestTypeDef, err := c.typeRepo.GetByID(ctx, instance.TypeDefinition.ID)
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
