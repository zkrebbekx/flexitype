package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// GetAttributeDefinition retrieves an attribute definition by ID
func (r *postgresRepository) GetAttributeDefinition(ctx context.Context, id ulid.ULID) (*model.AttributeDefinition, error) {
	query := `
		SELECT
			id,
			internal_name,
			display_name,
			description,
			type_definition_id,
			constraints,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_definition
		WHERE id = $1 AND archived_at IS NULL
	`
	var attr model.AttributeDefinition
	err := r.db.GetContext(ctx, &attr, query, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &attr, err
}

// ArchiveAttributeDefinition archives an attribute definition
func (r *postgresRepository) ArchiveAttributeDefinition(ctx context.Context, id ulid.ULID) error {
	query := `
		UPDATE attribute_definition
		SET archived_at = $1
		WHERE id = $2 AND archived_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// ListAttributeDefinitions lists attribute definitions based on filter
func (r *postgresRepository) ListAttributeDefinitions(ctx context.Context, filter model.AttributeDefinitionFilter) ([]*model.AttributeDefinition, error) {
	query := `
		SELECT
			id,
			internal_name,
			display_name,
			description,
			type_definition_id,
			constraints,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_definition
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if !filter.IncludeArchived {
		query += " AND archived_at IS NULL"
	}

	if filter.Version > 0 {
		query += fmt.Sprintf(" AND version = $%d", argNum)
		args = append(args, filter.Version)
		argNum++
	}

	if filter.OnlyLatest {
		query += ` AND version = (
			SELECT MAX(version)
			FROM attribute_definition ad2
			WHERE ad2.id = attribute_definition.id
		)`
	}

	if len(filter.InternalName) > 0 {
		query += fmt.Sprintf(" AND internal_name = ANY($%d)", argNum)
		args = append(args, filter.InternalName)
		argNum++
	}

	if len(filter.TypeDefinitionID) > 0 {
		query += fmt.Sprintf(" AND type_definition_id = ANY($%d)", argNum)
		args = append(args, filter.TypeDefinitionID)
		argNum++
	}

	if len(filter.IDs) > 0 {
		query += fmt.Sprintf(" AND id = ANY($%d)", argNum)
		args = append(args, filter.IDs)
		argNum++
	}

	if !filter.CreatedAfter.IsZero() {
		query += fmt.Sprintf(" AND created_at >= $%d", argNum)
		args = append(args, filter.CreatedAfter)
		argNum++
	}

	if !filter.CreatedBefore.IsZero() {
		query += fmt.Sprintf(" AND created_at <= $%d", argNum)
		args = append(args, filter.CreatedBefore)
		argNum++
	}

	if !filter.UpdatedAfter.IsZero() {
		query += fmt.Sprintf(" AND updated_at >= $%d", argNum)
		args = append(args, filter.UpdatedAfter)
		argNum++
	}

	if !filter.UpdatedBefore.IsZero() {
		query += fmt.Sprintf(" AND updated_at <= $%d", argNum)
		args = append(args, filter.UpdatedBefore)
		argNum++
	}

	if filter.Limit > 0 {
		query += fmt.Sprintf(" LIMIT $%d", argNum)
		args = append(args, filter.Limit)
		argNum++
	}

	if filter.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argNum)
		args = append(args, filter.Offset)
	}

	query += " ORDER BY created_at DESC"

	var attrs []*model.AttributeDefinition
	err := r.db.SelectContext(ctx, &attrs, query, args...)
	if err != nil {
		return nil, err
	}

	return attrs, err
}

// UpsertAttributeDefinition creates or updates an attribute definition
func (r *postgresRepository) UpsertAttributeDefinition(ctx context.Context, attrDef *model.AttributeDefinition) error {
	query := `
		INSERT INTO attribute_definition (
			id,
			internal_name,
			display_name,
			description,
			type_definition_id,
			type,
			constraints,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			internal_name = EXCLUDED.internal_name,
			display_name = EXCLUDED.display_name,
			description = EXCLUDED.description,
			type_definition_id = EXCLUDED.type_definition_id,
			type = EXCLUDED.type,
			constraints = EXCLUDED.constraints,
			version = attribute_definition.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING version
	`
	now := time.Now()
	if attrDef.CreatedAt.IsZero() {
		attrDef.CreatedAt = now
	}
	attrDef.UpdatedAt = now
	if attrDef.Version == 0 {
		attrDef.Version = 1
	}

	constraintsJSON, err := json.Marshal(attrDef.Constraints)
	if err != nil {
		return fmt.Errorf("failed to marshal constraints: %w", err)
	}

	var newVersion int
	err = r.db.GetContext(ctx, &newVersion, query,
		attrDef.ID,
		attrDef.InternalName,
		attrDef.DisplayName,
		attrDef.Description,
		attrDef.TypeDefinitionID,
		attrDef.Type,
		constraintsJSON,
		attrDef.Version,
		attrDef.CreatedAt,
		attrDef.UpdatedAt,
	)
	if err == nil {
		attrDef.Version = newVersion
	}
	return err
} 