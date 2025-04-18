package services

import (
	"context"
	"fmt"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// InstanceService provides high-level operations for managing instances
type InstanceService struct {
	typeRepo     ports.TypeRepository
	instanceRepo ports.InstanceRepository
}

// NewInstanceService creates a new instance service
func NewInstanceService(typeRepo ports.TypeRepository, instanceRepo ports.InstanceRepository) *InstanceService {
	return &InstanceService{
		typeRepo:     typeRepo,
		instanceRepo: instanceRepo,
	}
}

// SaveInstance creates or updates an instance based on whether it already exists
func (s *InstanceService) SaveInstance(ctx context.Context, id string, typeID string, attributes map[string]interface{}) (*core.Instance, error) {
	// Check if the instance already exists
	existing, err := s.instanceRepo.GetByID(ctx, id)
	if err == nil && existing != nil {
		// Instance exists - perform update

		// Get the latest type definition to ensure we're using the latest version
		latestTypeDef, err := s.typeRepo.GetByID(ctx, existing.TypeDefinition.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest type definition: %w", err)
		}

		// Get the latest version number and increment it
		latestVersion, err := s.instanceRepo.GetLatestVersion(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("failed to determine latest version: %w", err)
		}

		// Create a new instance version
		newInstance := core.NewInstanceVersion(existing, latestVersion+1)
		newInstance.TypeDefinition = latestTypeDef

		// If the type definition version has changed, update the instance type version
		if existing.TypeVersion != latestTypeDef.Version {
			newInstance.TypeVersion = latestTypeDef.Version
		}

		// Copy all attributes from the previous version
		for attrID, value := range existing.Attributes {
			// Check if the attribute is still active in the new type version
			attrDef := newInstance.FindAttributeDefinition(attrID)
			if attrDef != nil && !attrDef.Disabled {
				// Only copy attributes that are still active
				newInstance.Attributes[attrDef.ID] = value
			}
		}

		// Apply the new/updated attributes
		for name, value := range attributes {
			attrDef := newInstance.FindAttributeDefinition(name)
			if attrDef == nil {
				return nil, fmt.Errorf("attribute '%s' not found in type definition", name)
			}
			err := newInstance.SetAttribute(attrDef.ID, value)
			if err != nil {
				return nil, fmt.Errorf("failed to set attribute '%s': %w", name, err)
			}
		}

		// Validate the instance
		errors := newInstance.Validate()
		if len(errors) > 0 {
			return nil, fmt.Errorf("validation failed: %v", errors)
		}

		// Save the new instance version
		err = s.instanceRepo.Save(ctx, newInstance)
		if err != nil {
			return nil, fmt.Errorf("failed to save instance: %w", err)
		}

		return newInstance, nil
	} else {
		// Instance doesn't exist - create a new one

		// Get the type definition
		typeDef, err := s.typeRepo.GetByID(ctx, typeID)
		if err != nil {
			return nil, fmt.Errorf("type with ID '%s' not found: %w", typeID, err)
		}

		// Create the instance
		instance := core.NewInstance(id, typeDef)

		// Set all provided attributes
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

		// Save the instance
		err = s.instanceRepo.Save(ctx, instance)
		if err != nil {
			return nil, fmt.Errorf("failed to save instance: %w", err)
		}

		return instance, nil
	}
}

// GetInstance retrieves the latest version of an instance by ID
func (s *InstanceService) GetInstance(ctx context.Context, id string) (*core.Instance, error) {
	return s.instanceRepo.GetByID(ctx, id)
}

// GetInstanceVersion retrieves a specific version of an instance
func (s *InstanceService) GetInstanceVersion(ctx context.Context, id string, version int) (*core.Instance, error) {
	return s.instanceRepo.GetByIDAndVersion(ctx, id, version)
}

// GetAllInstanceVersions retrieves all versions of an instance
func (s *InstanceService) GetAllInstanceVersions(ctx context.Context, id string) ([]*core.Instance, error) {
	return s.instanceRepo.GetAllVersions(ctx, id)
}

// GetLatestInstanceVersion gets the latest version number for an instance
func (s *InstanceService) GetLatestInstanceVersion(ctx context.Context, id string) (int, error) {
	return s.instanceRepo.GetLatestVersion(ctx, id)
}

// ArchiveInstance marks an instance as archived
func (s *InstanceService) ArchiveInstance(ctx context.Context, id string) error {
	return s.instanceRepo.Archive(ctx, id)
}

// UnarchiveInstance restores a previously archived instance
func (s *InstanceService) UnarchiveInstance(ctx context.Context, id string) error {
	// Check if the instance exists
	instance, err := s.instanceRepo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Check if it's actually archived
	if instance.ArchivedAt == nil {
		return fmt.Errorf("instance with ID '%s' is not archived", id)
	}

	return s.instanceRepo.Unarchive(ctx, id)
}

// QueryInstancesWithOptions queries instances with advanced options
func (s *InstanceService) QueryInstancesWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	return s.instanceRepo.QueryWithOptions(ctx, options)
}
