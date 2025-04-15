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

// CreateInstance creates a new instance of a type
func (s *InstanceService) CreateInstance(ctx context.Context, id string, typeID string, attributes map[string]interface{}) (*core.Instance, error) {
	// Check for existing instance with same ID
	existing, err := s.instanceRepo.GetByID(ctx, id)
	if err == nil && existing != nil {
		return nil, fmt.Errorf("instance with ID '%s' already exists", id)
	}

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

// GetInstance retrieves an instance by ID
func (s *InstanceService) GetInstance(ctx context.Context, id string) (*core.Instance, error) {
	return s.instanceRepo.GetByID(ctx, id)
}

// UpdateInstance updates an existing instance
func (s *InstanceService) UpdateInstance(ctx context.Context, id string, attributes map[string]interface{}) (*core.Instance, error) {
	// Get the existing instance
	instance, err := s.instanceRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Get the latest type definition to ensure we're using the latest version
	latestTypeDef, err := s.typeRepo.GetByID(ctx, instance.TypeDefinition.ID)
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
	err = s.instanceRepo.Save(ctx, instance)
	if err != nil {
		return nil, fmt.Errorf("failed to save instance: %w", err)
	}

	return instance, nil
}

// DeleteInstance archives an instance instead of permanently deleting it
func (s *InstanceService) DeleteInstance(ctx context.Context, id string) error {
	return s.instanceRepo.Archive(ctx, id)
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

// QueryInstances queries instances by type and attribute filters
func (s *InstanceService) QueryInstances(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	// For backward compatibility, convert to QueryOptions and use QueryWithOptions
	options := &ports.QueryOptions{
		TypeID:           typeID,
		AttributeFilters: attributeFilters,
		IncludeArchived:  false, // By default, exclude archived instances
	}

	instances, _, err := s.instanceRepo.QueryWithOptions(ctx, options)
	return instances, err
}

// QueryInstancesWithOptions queries instances with advanced options
func (s *InstanceService) QueryInstancesWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	return s.instanceRepo.QueryWithOptions(ctx, options)
}
