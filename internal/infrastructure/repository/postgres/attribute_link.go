package repository

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/your-project/model"
)

type postgresRepository struct {
	db *sqlx.DB
}

// UpsertAttributeLink creates or updates an attribute link
func (r *postgresRepository) UpsertAttributeLink(ctx context.Context, link *model.AttributeLink) error {
	query := `
		INSERT INTO attribute_link (
			id,
			source_attribute_id,
			target_attribute_id,
			link_type,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			source_attribute_id = EXCLUDED.source_attribute_id,
			target_attribute_id = EXCLUDED.target_attribute_id,
			link_type = EXCLUDED.link_type,
			version = attribute_link.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING version
	`
	now := time.Now()
	if link.CreatedAt.IsZero() {
		link.CreatedAt = now
	}
	link.UpdatedAt = now
	if link.Version == 0 {
		link.Version = 1
	}

	var newVersion int
	err := r.db.GetContext(ctx, &newVersion, query,
		link.ID,
		link.SourceAttributeID,
		link.TargetAttributeID,
		link.LinkType,
		link.Version,
		link.CreatedAt,
		link.UpdatedAt,
	)
	if err == nil {
		link.Version = newVersion
	}
	return err
} 