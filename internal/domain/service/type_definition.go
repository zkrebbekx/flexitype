package service

import (
	"context"
	"fmt"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

// CreateTypeDefinitionDetails represents the data needed to create a new type definition
type CreateTypeDefinitionDetails struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// UpdateTypeDefinitionDetails represents the data needed to update an existing type definition
type UpdateTypeDefinitionDetails struct {
	ID          ulid.ULID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     int       `json:"version"`
}

// TypeDefinitionService handles business logic for type definitions
type TypeDefinitionService struct {
	repo repository.Repository
}

// NewTypeDefinitionService creates a new service for type definitions
func NewTypeDefinitionService(repo repository.Repository) *TypeDefinitionService {
	return &TypeDefinitionService{repo: repo}
}

// CreateTypeDefinition creates a new type definition
func (s *TypeDefinitionService) CreateTypeDefinition(ctx context.Context, details *CreateTypeDefinitionDetails) (*model.TypeDefinition, error) {
	// Create domain model from details
	typeDef := &model.TypeDefinition{
		ID:          ulid.Make(),
		Name:        details.Name,
		Description: details.Description,
	}

	// Validate the type definition using domain model validation
	if err := typeDef.Validate(); err != nil {
		return nil, fmt.Errorf("type definition validation failed: %w", err)
	}

	// Set timestamps and version using domain model methods
	typeDef.UpdateTimestamps()
	typeDef.Version = 1

	// Create the type definition
	if err := s.repo.UpsertTypeDefinition(ctx, typeDef); err != nil {
		return nil, fmt.Errorf("failed to create type definition: %w", err)
	}

	return typeDef, nil
}

// UpdateTypeDefinition updates an existing type definition
func (s *TypeDefinitionService) UpdateTypeDefinition(ctx context.Context, details *UpdateTypeDefinitionDetails) (*model.TypeDefinition, error) {
	// Get existing type definition to validate version
	existing, err := s.repo.GetTypeDefinition(ctx, details.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing type definition: %w", err)
	}
	if existing == nil {
		return nil, model.NewTypeDefinitionNotFoundError(details.ID)
	}

	// Create domain model from details
	typeDef := &model.TypeDefinition{
		ID:          details.ID,
		Name:        details.Name,
		Description: details.Description,
	}

	// Validate the type definition using domain model validation
	if err := typeDef.Validate(); err != nil {
		return nil, fmt.Errorf("type definition validation failed: %w", err)
	}

	// Update version and timestamp using domain model methods
	typeDef.Version = existing.Version + 1
	typeDef.UpdateTimestamps()
	typeDef.CreatedAt = existing.CreatedAt

	// Update the type definition
	if err := s.repo.UpsertTypeDefinition(ctx, typeDef); err != nil {
		return nil, fmt.Errorf("failed to update type definition: %w", err)
	}

	return typeDef, nil
}

// GetTypeDefinition retrieves a type definition by ID
func (s *TypeDefinitionService) GetTypeDefinition(ctx context.Context, id ulid.ULID) (*model.TypeDefinition, error) {
	typeDef, err := s.repo.GetTypeDefinition(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}
	if typeDef == nil {
		return nil, model.NewTypeDefinitionNotFoundError(id)
	}
	return typeDef, nil
}

// ArchiveTypeDefinition archives a type definition
func (s *TypeDefinitionService) ArchiveTypeDefinition(ctx context.Context, id ulid.ULID) error {
	typeDef, err := s.repo.GetTypeDefinition(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get type definition: %w", err)
	}
	if typeDef == nil {
		return model.NewTypeDefinitionNotFoundError(id)
	}

	// Archive using domain model method
	typeDef.Archive()
	if err := s.repo.UpsertTypeDefinition(ctx, typeDef); err != nil {
		return fmt.Errorf("failed to archive type definition: %w", err)
	}
	return nil
}

// ListTypeDefinitions lists type definitions based on filter
func (s *TypeDefinitionService) ListTypeDefinitions(ctx context.Context, filter model.TypeDefinitionFilter) ([]*model.TypeDefinition, error) {
	typeDefs, err := s.repo.ListTypeDefinitions(ctx, filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list type definitions: %w", err)
	}
	return typeDefs, nil
} 