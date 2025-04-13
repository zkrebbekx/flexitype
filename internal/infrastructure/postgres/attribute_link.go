package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/oklog/ulid"
	"github.com/pkg/errors"

	"github.com/zkrebbekx/flexitype/internal/domain/model"
	"github.com/zkrebbekx/flexitype/internal/domain/repository"
)

// UpsertAttributeLink creates or updates an attribute link
func (r *PostgresRepository) UpsertAttributeLink(ctx context.Context, link *model.AttributeLink) error {
	// Validate the link
	if err := link.Validate(); err != nil {
		return err
	}

	// Update timestamps and version
	link.UpdateTimestamps()
	link.IncrementVersion()

	query := `
		INSERT INTO attribute_link (
			id,
			source_attribute_id,
			target_attribute_id,
			link_type,
			description,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (id) DO UPDATE SET
			source_attribute_id = EXCLUDED.source_attribute_id,
			target_attribute_id = EXCLUDED.target_attribute_id,
			link_type = EXCLUDED.link_type,
			description = EXCLUDED.description,
			version = EXCLUDED.version,
			updated_at = EXCLUDED.updated_at
		WHERE attribute_link.version < EXCLUDED.version
	`

	_, err := r.db.ExecContext(ctx, query,
		link.ID,
		link.SourceAttributeID,
		link.TargetAttributeID,
		link.LinkType,
		link.Description,
		link.Version,
		link.CreatedAt,
		link.UpdatedAt,
	)
	if err != nil {
		return errors.Wrap(err, "failed to upsert attribute link")
	}

	return nil
}

// GetAttributeLink retrieves an attribute link by ID
func (r *PostgresRepository) GetAttributeLink(ctx context.Context, id ulid.ULID) (*model.AttributeLink, error) {
	query := `
		SELECT
			id,
			source_attribute_id,
			target_attribute_id,
			link_type,
			description,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_link
		WHERE id = $1 AND archived_at IS NULL
	`

	var link model.AttributeLink
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&link.ID,
		&link.SourceAttributeID,
		&link.TargetAttributeID,
		&link.LinkType,
		&link.Description,
		&link.Version,
		&link.CreatedAt,
		&link.UpdatedAt,
		&link.ArchivedAt,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, model.NewAttributeLinkNotFoundError(id)
		}
		return nil, errors.Wrap(err, "failed to get attribute link")
	}

	return &link, nil
}

// ArchiveAttributeLink archives an attribute link
func (r *PostgresRepository) ArchiveAttributeLink(ctx context.Context, id ulid.ULID) error {
	// Get the link first to ensure it exists
	link, err := r.GetAttributeLink(ctx, id)
	if err != nil {
		return err
	}

	// Archive the link in the domain model
	link.Archive()

	query := `
		UPDATE attribute_link
		SET archived_at = $1,
			updated_at = $2
		WHERE id = $3 AND archived_at IS NULL
	`

	result, err := r.db.ExecContext(ctx, query, link.ArchivedAt, link.UpdatedAt, id)
	if err != nil {
		return errors.Wrap(err, "failed to archive attribute link")
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return errors.Wrap(err, "failed to get rows affected")
	}
	if rowsAffected == 0 {
		return model.NewAttributeLinkNotFoundError(id)
	}

	return nil
}

// ListAttributeLinks lists attribute links based on filter criteria
func (r *PostgresRepository) ListAttributeLinks(ctx context.Context, filter model.AttributeLinkFilter) ([]*model.AttributeLink, error) {
	query := `
		SELECT
			id,
			source_attribute_id,
			target_attribute_id,
			link_type,
			description,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_link
		WHERE archived_at IS NULL
	`

	var args []interface{}
	argCount := 1

	if len(filter.SourceAttributeID) > 0 {
		query += " AND source_attribute_id = ANY($" + string(argCount) + ")"
		args = append(args, filter.SourceAttributeID)
		argCount++
	}

	if len(filter.TargetAttributeID) > 0 {
		query += " AND target_attribute_id = ANY($" + string(argCount) + ")"
		args = append(args, filter.TargetAttributeID)
		argCount++
	}

	if len(filter.IDs) > 0 {
		query += " AND id = ANY($" + string(argCount) + ")"
		args = append(args, filter.IDs)
		argCount++
	}

	if filter.OnlyLatest {
		query += " AND version = (SELECT MAX(version) FROM attribute_link WHERE id = attribute_link.id)"
	}

	query += " ORDER BY created_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list attribute links")
	}
	defer rows.Close()

	var links []*model.AttributeLink
	for rows.Next() {
		var link model.AttributeLink
		err := rows.Scan(
			&link.ID,
			&link.SourceAttributeID,
			&link.TargetAttributeID,
			&link.LinkType,
			&link.Description,
			&link.Version,
			&link.CreatedAt,
			&link.UpdatedAt,
			&link.ArchivedAt,
		)
		if err != nil {
			return nil, errors.Wrap(err, "failed to scan attribute link")
		}
		links = append(links, &link)
	}

	if err := rows.Err(); err != nil {
		return nil, errors.Wrap(err, "failed to iterate attribute links")
	}

	return links, nil
}

// GetAttributeLinksBySourceID retrieves all attribute links for a given source attribute ID
func (r *PostgresRepository) GetAttributeLinksBySourceID(ctx context.Context, sourceAttributeID ulid.ULID) ([]*model.AttributeLink, error) {
	filter := model.AttributeLinkFilter{
		SourceAttributeID: []ulid.ULID{sourceAttributeID},
		OnlyLatest:        true,
	}
	return r.ListAttributeLinks(ctx, filter)
}

// GetAttributeLinksByTargetID retrieves all attribute links for a given target attribute ID
func (r *PostgresRepository) GetAttributeLinksByTargetID(ctx context.Context, targetAttributeID ulid.ULID) ([]*model.AttributeLink, error) {
	filter := model.AttributeLinkFilter{
		TargetAttributeID: []ulid.ULID{targetAttributeID},
		OnlyLatest:        true,
	}
	return r.ListAttributeLinks(ctx, filter)
} 