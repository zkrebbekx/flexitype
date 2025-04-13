package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

// CreateAttributeDetails represents the data needed to create a new attribute
type CreateAttributeDetails struct {
	TypeDefinitionID ulid.ULID `json:"type_definition_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DataType        string    `json:"data_type"`
	IsRequired      bool      `json:"is_required"`
	DefaultValue    *model.DynamicValue `json:"default_value"`
	ValidationRules *model.ValidationRules `json:"validation_rules"`
}

// UpdateAttributeDetails represents the data needed to update an existing attribute
type UpdateAttributeDetails struct {
	ID              ulid.ULID `json:"id"`
	TypeDefinitionID ulid.ULID `json:"type_definition_id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	DataType        string    `json:"data_type"`
	IsRequired      bool      `json:"is_required"`
	DefaultValue    *model.DynamicValue `json:"default_value"`
	ValidationRules *model.ValidationRules `json:"validation_rules"`
	Version         int       `json:"version"`
}

// AttributeService handles business logic for attributes
type AttributeService struct {
	repo repository.Repository
}

// NewAttributeService creates a new service for attributes
func NewAttributeService(repo repository.Repository) *AttributeService {
	return &AttributeService{repo: repo}
}

// CreateAttribute creates a new attribute
func (s *AttributeService) CreateAttribute(ctx context.Context, details *CreateAttributeDetails) (*model.Attribute, error) {
	// Validate type definition exists
	typeDef, err := s.repo.GetTypeDefinition(ctx, details.TypeDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}
	if typeDef == nil {
		return nil, model.NewTypeDefinitionNotFoundError(details.TypeDefinitionID)
	}

	// Create domain model from details
	attribute := &model.Attribute{
		ID:              ulid.Make(),
		TypeDefinitionID: details.TypeDefinitionID,
		Name:            details.Name,
		Description:     details.Description,
		DataType:        details.DataType,
		IsRequired:      details.IsRequired,
		DefaultValue:    details.DefaultValue,
		ValidationRules: details.ValidationRules,
	}

	// Validate the attribute using domain model validation
	if err := attribute.Validate(); err != nil {
		return nil, fmt.Errorf("attribute validation failed: %w", err)
	}

	// Set timestamps and version using domain model methods
	attribute.UpdateTimestamps()
	attribute.Version = 1

	// Create the attribute
	if err := s.repo.UpsertAttribute(ctx, attribute); err != nil {
		return nil, fmt.Errorf("failed to create attribute: %w", err)
	}

	return attribute, nil
}

// UpdateAttribute updates an existing attribute
func (s *AttributeService) UpdateAttribute(ctx context.Context, details *UpdateAttributeDetails) (*model.Attribute, error) {
	// Get existing attribute to validate version
	existing, err := s.repo.GetAttribute(ctx, details.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing attribute: %w", err)
	}
	if existing == nil {
		return nil, model.NewAttributeNotFoundError(details.ID)
	}

	// Validate type definition exists
	typeDef, err := s.repo.GetTypeDefinition(ctx, details.TypeDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}
	if typeDef == nil {
		return nil, model.NewTypeDefinitionNotFoundError(details.TypeDefinitionID)
	}

	// Create domain model from details
	attribute := &model.Attribute{
		ID:              details.ID,
		TypeDefinitionID: details.TypeDefinitionID,
		Name:            details.Name,
		Description:     details.Description,
		DataType:        details.DataType,
		IsRequired:      details.IsRequired,
		DefaultValue:    details.DefaultValue,
		ValidationRules: details.ValidationRules,
	}

	// Validate the attribute using domain model validation
	if err := attribute.Validate(); err != nil {
		return nil, fmt.Errorf("attribute validation failed: %w", err)
	}

	// Update version and timestamp using domain model methods
	attribute.Version = existing.Version + 1
	attribute.UpdateTimestamps()
	attribute.CreatedAt = existing.CreatedAt

	// Update the attribute
	if err := s.repo.UpsertAttribute(ctx, attribute); err != nil {
		return nil, fmt.Errorf("failed to update attribute: %w", err)
	}

	return attribute, nil
}

// GetAttribute retrieves an attribute by ID
func (s *AttributeService) GetAttribute(ctx context.Context, id ulid.ULID) (*model.Attribute, error) {
	attribute, err := s.repo.GetAttribute(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get attribute: %w", err)
	}
	if attribute == nil {
		return nil, model.NewAttributeNotFoundError(id)
	}
	return attribute, nil
}

// ArchiveAttribute archives an attribute
func (s *AttributeService) ArchiveAttribute(ctx context.Context, id ulid.ULID) error {
	attribute, err := s.repo.GetAttribute(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get attribute: %w", err)
	}
	if attribute == nil {
		return model.NewAttributeNotFoundError(id)
	}

	// Archive using domain model method
	attribute.Archive()
	if err := s.repo.UpsertAttribute(ctx, attribute); err != nil {
		return fmt.Errorf("failed to archive attribute: %w", err)
	}
	return nil
}

// ListAttributes lists attributes based on filter
func (s *AttributeService) ListAttributes(ctx context.Context, filter model.AttributeFilter) ([]*model.Attribute, error) {
	attributes, err := s.repo.ListAttributes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list attributes: %w", err)
	}
	return attributes, nil
}

// GetAttributesByTypeDefinitionID retrieves all attributes for a given type definition
func (s *AttributeService) GetAttributesByTypeDefinitionID(ctx context.Context, typeDefinitionID ulid.ULID) ([]*model.Attribute, error) {
	// Validate type definition exists
	typeDef, err := s.repo.GetTypeDefinition(ctx, typeDefinitionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}
	if typeDef == nil {
		return nil, model.NewTypeDefinitionNotFoundError(typeDefinitionID)
	}

	// Get attributes using filter
	filter := model.AttributeFilter{
		TypeDefinitionID: typeDefinitionID,
	}
	attributes, err := s.repo.ListAttributes(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to get attributes: %w", err)
	}

	return attributes, nil
} 