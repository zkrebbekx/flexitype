package services

import (
	"context"
	"fmt"
	"strings"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// TypeService provides high-level operations for managing type definitions
type TypeService struct {
	typeRepo     ports.TypeRepository
	instanceRepo ports.InstanceRepository
}

// NewTypeService creates a new type service
func NewTypeService(typeRepo ports.TypeRepository, instanceRepo ports.InstanceRepository) *TypeService {
	return &TypeService{
		typeRepo:     typeRepo,
		instanceRepo: instanceRepo,
	}
}

// SaveType creates or updates a type definition based on whether it already exists
func (s *TypeService) SaveType(ctx context.Context, id, name, description string, parentTypeID string) (*core.TypeDefinition, error) {
	// Check for existing type with same ID
	existing, err := s.typeRepo.GetByID(ctx, id)
	if err == nil && existing != nil {
		// Type exists - perform update

		// Check for name collision if name is changing
		if existing.Name != name {
			nameCheck, err := s.typeRepo.GetByName(ctx, name)
			if err == nil && nameCheck != nil && nameCheck.ID != id {
				return nil, fmt.Errorf("type with name '%s' already exists", name)
			}
		}

		// Check if anything is changing that would require a version increment
		versionChange := false

		// Check if name is changing
		if existing.Name != name {
			versionChange = true
		}

		// Update the type
		existing.Name = name
		existing.Description = description

		// Update parent type if specified
		if parentTypeID != "" && (existing.ParentType == nil || existing.ParentType.ID != parentTypeID) {
			parentType, err := s.typeRepo.GetByID(ctx, parentTypeID)
			if err != nil {
				return nil, fmt.Errorf("failed to get parent type: %w", err)
			}

			// Check for circular inheritance
			current := parentType
			for current != nil {
				if current.ID == id {
					return nil, fmt.Errorf("circular inheritance detected")
				}
				current = current.ParentType
			}

			existing.SetParentType(parentType)
			versionChange = true
		} else if parentTypeID == "" && existing.ParentType != nil {
			// Remove parent type
			existing.SetParentType(nil)
			versionChange = true
		}

		// If we changed anything that affects the schema, increment the version
		if versionChange {
			existing.IncrementVersion()
		}

		// Save the updated type
		err = s.typeRepo.Save(ctx, existing)
		if err != nil {
			return nil, fmt.Errorf("failed to save type: %w", err)
		}

		return existing, nil
	} else {
		// Type doesn't exist - create a new one

		// Check for existing type with same name
		nameCheck, err := s.typeRepo.GetByName(ctx, name)
		if err == nil && nameCheck != nil {
			return nil, fmt.Errorf("type with name '%s' already exists", name)
		}

		// Create the type
		typeDef := core.NewTypeDefinition(id, name, description)

		// Set parent type if specified
		if parentTypeID != "" {
			parentType, err := s.typeRepo.GetByID(ctx, parentTypeID)
			if err != nil {
				return nil, fmt.Errorf("failed to get parent type: %w", err)
			}

			typeDef.SetParentType(parentType)
		}

		// Save the type
		err = s.typeRepo.Save(ctx, typeDef)
		if err != nil {
			return nil, fmt.Errorf("failed to save type: %w", err)
		}

		return typeDef, nil
	}
}

// GetType retrieves a type definition by ID
func (s *TypeService) GetType(ctx context.Context, id string) (*core.TypeDefinition, error) {
	return s.typeRepo.GetByID(ctx, id)
}

// GetTypeByName retrieves a type definition by name
func (s *TypeService) GetTypeByName(ctx context.Context, name string) (*core.TypeDefinition, error) {
	return s.typeRepo.GetByName(ctx, name)
}

// GetTypeVersion retrieves a specific version of a type definition
func (s *TypeService) GetTypeVersion(ctx context.Context, id string, version int) (*core.TypeDefinition, error) {
	return s.typeRepo.GetByIDAndVersion(ctx, id, version)
}

// ArchiveType archives a type definition
func (s *TypeService) ArchiveType(ctx context.Context, id string) error {
	// Same validation as DeleteType
	options := &ports.QueryOptions{
		TypeID:          id,
		IncludeArchived: false,
	}
	instances, _, err := s.instanceRepo.QueryWithOptions(ctx, options)
	if err != nil {
		return fmt.Errorf("failed to check for instances: %w", err)
	}

	if len(instances) > 0 {
		return fmt.Errorf("cannot archive type with existing instances")
	}

	// Check if this type is a parent of other types
	allTypes, _, err := s.typeRepo.List(ctx, &ports.QueryOptions{
		IncludeArchived: false,
	})
	if err != nil {
		return fmt.Errorf("failed to list types: %w", err)
	}

	for _, typeDef := range allTypes {
		if typeDef.ParentType != nil && typeDef.ParentType.ID == id {
			return fmt.Errorf("cannot archive type that is a parent of other types")
		}
	}

	return s.typeRepo.Archive(ctx, id)
}

// UnarchiveType restores a previously archived type definition
func (s *TypeService) UnarchiveType(ctx context.Context, id string) error {
	// Check if the type exists
	typeDef, err := s.typeRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("type with ID '%s' not found", id)
	}

	// Check if it's actually archived
	if typeDef.ArchivedAt == nil {
		return fmt.Errorf("type with ID '%s' is not archived", id)
	}

	return s.typeRepo.Unarchive(ctx, id)
}

// AddAttribute adds or updates an attribute on a type definition
func (s *TypeService) AddAttribute(ctx context.Context, typeID string, attribute *core.AttributeDefinition) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := s.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found", typeID)
	}

	// Add the attribute
	typeDef.AddAttribute(attribute)

	// Validate cascades to ensure they reference existing attributes and have no circular dependencies
	if errors := typeDef.ValidateCascades(); len(errors) > 0 {
		// Combine all validation errors into a single message
		errorMessages := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMessages = append(errorMessages, err.Error())
		}
		return nil, fmt.Errorf("cascade validation failed: %s", strings.Join(errorMessages, "; "))
	}

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// DeleteAttribute removes an attribute from a type definition
func (s *TypeService) DeleteAttribute(ctx context.Context, typeID string, attributeName string) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := s.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found", typeID)
	}

	// Find the attribute to be deleted by name
	var attributeToDelete *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			attributeToDelete = attr
			break
		}
	}

	if attributeToDelete == nil {
		return nil, fmt.Errorf("attribute with name '%s' not found", attributeName)
	}

	// Before removing, check if any other attributes reference this one in their cascades
	// Temporarily disable the attribute (to simulate removal for validation)
	attributeToDelete.SetDisabled(true)

	// Validate cascades to ensure they don't reference this soon-to-be-deleted attribute
	if errors := typeDef.ValidateCascades(); len(errors) > 0 {
		// Revert the temporary change
		attributeToDelete.SetDisabled(false)

		// Combine all validation errors into a single message
		errorMessages := make([]string, 0, len(errors))
		for _, err := range errors {
			errorMessages = append(errorMessages, err.Error())
		}
		return nil, fmt.Errorf("cannot delete attribute: %s", strings.Join(errorMessages, "; "))
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
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}

// ListTypes lists all type definitions
func (s *TypeService) ListTypes(ctx context.Context) ([]*core.TypeDefinition, error) {
	types, _, err := s.typeRepo.List(ctx, ports.DefaultQueryOptions())
	return types, err
}

// QueryTypes lists type definitions with advanced query options
func (s *TypeService) QueryTypes(ctx context.Context, options *ports.QueryOptions) ([]*core.TypeDefinition, int, error) {
	return s.typeRepo.List(ctx, options)
}

// SetAttributeDisabledState enables or disables an attribute on a type definition
func (s *TypeService) SetAttributeDisabledState(ctx context.Context, typeID, attributeName string, disabled bool) (*core.TypeDefinition, error) {
	// Get the type
	typeDef, err := s.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("type with ID '%s' not found", typeID)
	}

	// Find the attribute by name
	var foundAttr *core.AttributeDefinition
	for _, attr := range typeDef.Attributes {
		if attr.Name == attributeName {
			foundAttr = attr
			break
		}
	}

	if foundAttr == nil {
		return nil, fmt.Errorf("attribute with name '%s' not found in type '%s'", attributeName, typeID)
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

			// Combine all validation errors into a single message
			errorMessages := make([]string, 0, len(errors))
			for _, err := range errors {
				errorMessages = append(errorMessages, err.Error())
			}
			return nil, fmt.Errorf("cannot disable attribute: %s", strings.Join(errorMessages, "; "))
		}
	}

	// Increment the version since the type definition is changing
	typeDef.IncrementVersion()

	// Save the updated type
	err = s.typeRepo.Save(ctx, typeDef)
	if err != nil {
		return nil, fmt.Errorf("failed to save type: %w", err)
	}

	return typeDef, nil
}
