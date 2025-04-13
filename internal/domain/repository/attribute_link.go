package repository

import (
	"context"

	"github.com/oklog/ulid"

	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// AttributeLinkRepository defines the interface for attribute link operations
type AttributeLinkRepository interface {
	// UpsertAttributeLink creates or updates an attribute link
	UpsertAttributeLink(ctx context.Context, id ulid.ULID, sourceAttributeID ulid.ULID, targetAttributeID ulid.ULID, linkType string, description string, version int, createdAt, updatedAt time.Time) error
	// GetAttributeLink retrieves an attribute link by ID
	GetAttributeLink(ctx context.Context, id ulid.ULID) (*model.AttributeLink, error)
	// ArchiveAttributeLink archives an attribute link
	ArchiveAttributeLink(ctx context.Context, id ulid.ULID, archivedAt time.Time) error
	// ListAttributeLinks lists attribute links based on filter criteria
	ListAttributeLinks(ctx context.Context, filter model.AttributeLinkFilter) ([]*model.AttributeLink, error)
	// GetAttributeLinksBySourceID retrieves all attribute links for a given source attribute ID
	GetAttributeLinksBySourceID(ctx context.Context, sourceAttributeID ulid.ULID) ([]*model.AttributeLink, error)
	// GetAttributeLinksByTargetID retrieves all attribute links for a given target attribute ID
	GetAttributeLinksByTargetID(ctx context.Context, targetAttributeID ulid.ULID) ([]*model.AttributeLink, error)
} 