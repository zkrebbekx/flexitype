package repository

import (
	"context"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// Repository defines the interface for data access operations
type Repository interface {
	// TypeDefinition operations
	CreateTypeDefinition(ctx context.Context, typeDef *model.TypeDefinition) error
	GetTypeDefinition(ctx context.Context, id ulid.ULID) (*model.TypeDefinition, error)
	GetTypeDefinitionByInternalName(ctx context.Context, internalName string) (*model.TypeDefinition, error)
	UpdateTypeDefinition(ctx context.Context, typeDef *model.TypeDefinition) error
	ArchiveTypeDefinition(ctx context.Context, id ulid.ULID) error
	ListTypeDefinitions(ctx context.Context, filter model.TypeDefinitionFilter) ([]*model.TypeDefinition, error)
	SearchTypeDefinitions(ctx context.Context, query string, filter model.TypeDefinitionFilter) ([]*model.TypeDefinition, error)

	// AttributeDefinition operations
	CreateAttributeDefinition(ctx context.Context, attrDef *model.AttributeDefinition) error
	GetAttributeDefinition(ctx context.Context, id ulid.ULID) (*model.AttributeDefinition, error)
	GetAttributeDefinitionByInternalName(ctx context.Context, internalName string) (*model.AttributeDefinition, error)
	UpdateAttributeDefinition(ctx context.Context, attrDef *model.AttributeDefinition) error
	ArchiveAttributeDefinition(ctx context.Context, id ulid.ULID) error
	ListAttributeDefinitions(ctx context.Context, filter model.AttributeDefinitionFilter) ([]*model.AttributeDefinition, error)
	SearchAttributeDefinitions(ctx context.Context, query string, filter model.AttributeDefinitionFilter) ([]*model.AttributeDefinition, error)

	// AttributeValue operations
	CreateAttributeValue(ctx context.Context, value *model.AttributeValue) error
	GetAttributeValue(ctx context.Context, id ulid.ULID) (*model.AttributeValue, error)
	UpdateAttributeValue(ctx context.Context, value *model.AttributeValue) error
	ArchiveAttributeValue(ctx context.Context, id ulid.ULID) error
	ListAttributeValues(ctx context.Context, filter model.AttributeValueFilter) ([]*model.AttributeValue, error)
	SearchAttributeValues(ctx context.Context, query string, filter model.AttributeValueFilter) ([]*model.AttributeValue, error)

	// AttributeValueDependency operations
	CreateAttributeValueDependency(ctx context.Context, dependency *model.AttributeValueDependency) error
	GetAttributeValueDependency(ctx context.Context, id ulid.ULID) (*model.AttributeValueDependency, error)
	UpdateAttributeValueDependency(ctx context.Context, dependency *model.AttributeValueDependency) error
	ArchiveAttributeValueDependency(ctx context.Context, id ulid.ULID) error
	ListAttributeValueDependencies(ctx context.Context, filter model.AttributeValueDependencyFilter) ([]*model.AttributeValueDependency, error)
	GetDependentValues(ctx context.Context, sourceAttributeID ulid.ULID, sourceValue interface{}) ([]*model.AttributeValueDependency, error)

	// AttributeLink operations
	CreateAttributeLink(ctx context.Context, link *model.AttributeLink) error
	GetAttributeLink(ctx context.Context, id ulid.ULID) (*model.AttributeLink, error)
	UpdateAttributeLink(ctx context.Context, link *model.AttributeLink) error
	ArchiveAttributeLink(ctx context.Context, id ulid.ULID) error
	ListAttributeLinks(ctx context.Context, filter model.AttributeLinkFilter) ([]*model.AttributeLink, error)
	SearchAttributeLinks(ctx context.Context, query string, filter model.AttributeLinkFilter) ([]*model.AttributeLink, error)

	// Attribute operations
	CreateAttribute(ctx context.Context, attr *model.Attribute) error
	GetAttribute(ctx context.Context, id ulid.ULID) (*model.Attribute, error)
	UpdateAttribute(ctx context.Context, attr *model.Attribute) error
	ArchiveAttribute(ctx context.Context, id ulid.ULID) error
	ListAttributes(ctx context.Context, filter model.AttributeFilter) ([]*model.Attribute, error)

	// TypeLink operations
	CreateTypeLink(ctx context.Context, link *model.TypeLink) error
	GetTypeLink(ctx context.Context, id ulid.ULID) (*model.TypeLink, error)
	UpdateTypeLink(ctx context.Context, link *model.TypeLink) error
	ArchiveTypeLink(ctx context.Context, id ulid.ULID) error
	ListTypeLinks(ctx context.Context, filter model.TypeLinkFilter) ([]*model.TypeLink, error)

	// Search operations
	SearchAttributes(ctx context.Context, query string, filter model.AttributeFilter) ([]*model.Attribute, error)
} 