package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

// CreateAttributeValueDependencyDetails represents the data needed to create a new attribute value dependency
type CreateAttributeValueDependencyDetails struct {
	SourceAttributeID ulid.ULID `json:"source_attribute_id"`
	TargetAttributeID ulid.ULID `json:"target_attribute_id"`
	SourceValue      *model.DynamicValue `json:"source_value"`
	TargetValue      *model.DynamicValue `json:"target_value"`
	Description      string    `json:"description"`
}

// UpdateAttributeValueDependencyDetails represents the data needed to update an existing attribute value dependency
type UpdateAttributeValueDependencyDetails struct {
	ID               ulid.ULID `json:"id"`
	SourceAttributeID ulid.ULID `json:"source_attribute_id"`
	TargetAttributeID ulid.ULID `json:"target_attribute_id"`
	SourceValue      *model.DynamicValue `json:"source_value"`
	TargetValue      *model.DynamicValue `json:"target_value"`
	Description      string    `json:"description"`
	Version          int       `json:"version"`
}

// AttributeValueDependencyService handles business logic for attribute value dependencies
type AttributeValueDependencyService struct {
	repo repository.Repository
}

// NewAttributeValueDependencyService creates a new service for attribute value dependencies
func NewAttributeValueDependencyService(repo repository.Repository) *AttributeValueDependencyService {
	return &AttributeValueDependencyService{repo: repo}
}

// CreateDependency creates a new attribute value dependency
func (s *AttributeValueDependencyService) CreateDependency(ctx context.Context, details *CreateAttributeValueDependencyDetails) (*model.AttributeValueDependency, error) {
	// Validate source and target attributes exist and belong to the same type definition
	sourceAttr, err := s.repo.GetAttribute(ctx, details.SourceAttributeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source attribute: %w", err)
	}
	if sourceAttr == nil {
		return nil, model.NewAttributeNotFoundError(details.SourceAttributeID)
	}

	targetAttr, err := s.repo.GetAttribute(ctx, details.TargetAttributeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get target attribute: %w", err)
	}
	if targetAttr == nil {
		return nil, model.NewAttributeNotFoundError(details.TargetAttributeID)
	}

	if sourceAttr.TypeDefinitionID != targetAttr.TypeDefinitionID {
		return nil, fmt.Errorf("source and target attributes must belong to the same type definition")
	}

	// Create domain model from details
	dependency := &model.AttributeValueDependency{
		ID:               ulid.Make(),
		SourceAttributeID: details.SourceAttributeID,
		TargetAttributeID: details.TargetAttributeID,
		SourceValue:      details.SourceValue,
		TargetValue:      details.TargetValue,
		Description:      details.Description,
	}

	// Validate the dependency using domain model validation
	if err := dependency.Validate(); err != nil {
		return nil, fmt.Errorf("dependency validation failed: %w", err)
	}

	// Set timestamps and version using domain model methods
	dependency.UpdateTimestamps()
	dependency.Version = 1

	// Create the dependency
	if err := s.repo.UpsertAttributeValueDependency(ctx, dependency); err != nil {
		return nil, fmt.Errorf("failed to create dependency: %w", err)
	}

	return dependency, nil
}

// UpdateDependency updates an existing attribute value dependency
func (s *AttributeValueDependencyService) UpdateDependency(ctx context.Context, details *UpdateAttributeValueDependencyDetails) (*model.AttributeValueDependency, error) {
	// Get existing dependency to validate version
	existing, err := s.repo.GetAttributeValueDependency(ctx, details.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing dependency: %w", err)
	}
	if existing == nil {
		return nil, model.NewAttributeValueDependencyNotFoundError(details.ID)
	}

	// Validate source and target attributes exist and belong to the same type definition
	sourceAttr, err := s.repo.GetAttribute(ctx, details.SourceAttributeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source attribute: %w", err)
	}
	if sourceAttr == nil {
		return nil, model.NewAttributeNotFoundError(details.SourceAttributeID)
	}

	targetAttr, err := s.repo.GetAttribute(ctx, details.TargetAttributeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get target attribute: %w", err)
	}
	if targetAttr == nil {
		return nil, model.NewAttributeNotFoundError(details.TargetAttributeID)
	}

	if sourceAttr.TypeDefinitionID != targetAttr.TypeDefinitionID {
		return nil, fmt.Errorf("source and target attributes must belong to the same type definition")
	}

	// Create domain model from details
	dependency := &model.AttributeValueDependency{
		ID:               details.ID,
		SourceAttributeID: details.SourceAttributeID,
		TargetAttributeID: details.TargetAttributeID,
		SourceValue:      details.SourceValue,
		TargetValue:      details.TargetValue,
		Description:      details.Description,
	}

	// Validate the dependency using domain model validation
	if err := dependency.Validate(); err != nil {
		return nil, fmt.Errorf("dependency validation failed: %w", err)
	}

	// Update version and timestamp using domain model methods
	dependency.Version = existing.Version + 1
	dependency.UpdateTimestamps()
	dependency.CreatedAt = existing.CreatedAt

	// Update the dependency
	if err := s.repo.UpsertAttributeValueDependency(ctx, dependency); err != nil {
		return nil, fmt.Errorf("failed to update dependency: %w", err)
	}

	return dependency, nil
}

// GetDependency retrieves an attribute value dependency by ID
func (s *AttributeValueDependencyService) GetDependency(ctx context.Context, id ulid.ULID) (*model.AttributeValueDependency, error) {
	dependency, err := s.repo.GetAttributeValueDependency(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get dependency: %w", err)
	}
	if dependency == nil {
		return nil, model.NewAttributeValueDependencyNotFoundError(id)
	}
	return dependency, nil
}

// ArchiveDependency archives an attribute value dependency
func (s *AttributeValueDependencyService) ArchiveDependency(ctx context.Context, id ulid.ULID) error {
	dependency, err := s.repo.GetAttributeValueDependency(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get dependency: %w", err)
	}
	if dependency == nil {
		return model.NewAttributeValueDependencyNotFoundError(id)
	}

	// Archive using domain model method
	dependency.Archive()
	if err := s.repo.UpsertAttributeValueDependency(ctx, dependency); err != nil {
		return fmt.Errorf("failed to archive dependency: %w", err)
	}
	return nil
}

// ListDependencies lists attribute value dependencies based on filter
func (s *AttributeValueDependencyService) ListDependencies(ctx context.Context, filter model.AttributeValueDependencyFilter) ([]*model.AttributeValueDependency, error) {
	dependencies, err := s.repo.ListAttributeValueDependencies(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list dependencies: %w", err)
	}
	return dependencies, nil
}

// GetDependentValues retrieves all dependent values for a given source attribute value
func (s *AttributeValueDependencyService) GetDependentValues(ctx context.Context, sourceAttributeID ulid.ULID, sourceValue *model.DynamicValue) ([]*model.AttributeValueDependency, error) {
	// Validate source attribute exists
	sourceAttr, err := s.repo.GetAttribute(ctx, sourceAttributeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get source attribute: %w", err)
	}
	if sourceAttr == nil {
		return nil, model.NewAttributeNotFoundError(sourceAttributeID)
	}

	// Get all dependencies for the source attribute
	filter := model.AttributeValueDependencyFilter{
		SourceAttributeID: sourceAttributeID,
	}
	dependencies, err := s.repo.ListAttributeValueDependencies(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to get dependencies: %w", err)
	}

	// Filter dependencies based on source value
	var matchingDependencies []*model.AttributeValueDependency
	for _, dep := range dependencies {
		if dep.SourceValue.Equals(sourceValue) {
			matchingDependencies = append(matchingDependencies, dep)
		}
	}

	return matchingDependencies, nil
} 