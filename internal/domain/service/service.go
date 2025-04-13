package service

import (
	"context"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

// Service implements the business logic for attribute management
type Service struct {
	repo repository.Repository
}

// NewService creates a new service instance
func NewService(repo repository.Repository) *Service {
	return &Service{repo: repo}
}

// CreateAttribute creates a new attribute
func (s *Service) CreateAttribute(ctx context.Context, attr *model.Attribute) error {
	attr.ID = ulid.Make()
	attr.CreatedAt = time.Now()
	attr.UpdatedAt = time.Now()
	return s.repo.CreateAttribute(ctx, attr)
}

// GetAttribute retrieves an attribute by ID
func (s *Service) GetAttribute(ctx context.Context, id ulid.ULID) (*model.Attribute, error) {
	return s.repo.GetAttribute(ctx, id)
}

// UpdateAttribute updates an existing attribute
func (s *Service) UpdateAttribute(ctx context.Context, attr *model.Attribute) error {
	attr.UpdatedAt = time.Now()
	return s.repo.UpdateAttribute(ctx, attr)
}

// DeleteAttribute deletes an attribute by ID
func (s *Service) DeleteAttribute(ctx context.Context, id ulid.ULID) error {
	return s.repo.DeleteAttribute(ctx, id)
}

// ListAttributes lists all attributes
func (s *Service) ListAttributes(ctx context.Context) ([]*model.Attribute, error) {
	return s.repo.ListAttributes(ctx)
}

// CreateAttributeValue creates a new attribute value
func (s *Service) CreateAttributeValue(ctx context.Context, value *model.AttributeValue) error {
	value.ID = ulid.Make()
	value.CreatedAt = time.Now()
	value.UpdatedAt = time.Now()
	return s.repo.CreateAttributeValue(ctx, value)
}

// GetAttributeValue retrieves an attribute value by ID
func (s *Service) GetAttributeValue(ctx context.Context, id ulid.ULID) (*model.AttributeValue, error) {
	return s.repo.GetAttributeValue(ctx, id)
}

// UpdateAttributeValue updates an existing attribute value
func (s *Service) UpdateAttributeValue(ctx context.Context, value *model.AttributeValue) error {
	value.UpdatedAt = time.Now()
	return s.repo.UpdateAttributeValue(ctx, value)
}

// DeleteAttributeValue deletes an attribute value by ID
func (s *Service) DeleteAttributeValue(ctx context.Context, id ulid.ULID) error {
	return s.repo.DeleteAttributeValue(ctx, id)
}

// ListAttributeValues lists all values for a specific attribute
func (s *Service) ListAttributeValues(ctx context.Context, attributeID ulid.ULID) ([]*model.AttributeValue, error) {
	return s.repo.ListAttributeValues(ctx, attributeID)
}

// CreateTypeLink creates a new type link
func (s *Service) CreateTypeLink(ctx context.Context, link *model.TypeLink) error {
	link.ID = ulid.Make()
	link.CreatedAt = time.Now()
	link.UpdatedAt = time.Now()
	return s.repo.CreateTypeLink(ctx, link)
}

// GetTypeLink retrieves a type link by ID
func (s *Service) GetTypeLink(ctx context.Context, id ulid.ULID) (*model.TypeLink, error) {
	return s.repo.GetTypeLink(ctx, id)
}

// UpdateTypeLink updates an existing type link
func (s *Service) UpdateTypeLink(ctx context.Context, link *model.TypeLink) error {
	link.UpdatedAt = time.Now()
	return s.repo.UpdateTypeLink(ctx, link)
}

// DeleteTypeLink deletes a type link by ID
func (s *Service) DeleteTypeLink(ctx context.Context, id ulid.ULID) error {
	return s.repo.DeleteTypeLink(ctx, id)
}

// ListTypeLinks lists all type links
func (s *Service) ListTypeLinks(ctx context.Context) ([]*model.TypeLink, error) {
	return s.repo.ListTypeLinks(ctx)
}

// SearchAttributes searches attributes using the query language
func (s *Service) SearchAttributes(ctx context.Context, query string) ([]*model.Attribute, error) {
	return s.repo.SearchAttributes(ctx, query)
}

// SearchAttributeValues searches attribute values using the query language
func (s *Service) SearchAttributeValues(ctx context.Context, query string) ([]*model.AttributeValue, error) {
	return s.repo.SearchAttributeValues(ctx, query)
} 