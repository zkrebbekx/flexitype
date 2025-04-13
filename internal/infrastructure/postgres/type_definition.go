package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// Type Definition operations

// GetTypeDefinition retrieves a type definition by ID
func (r *postgresRepository) GetTypeDefinition(ctx context.Context, id ulid.ULID) (*model.TypeDefinition, error) {
	query := `
		SELECT
			id,
			name,
			description,
			version,
			created_at,
			updated_at,
			archived_at
		FROM type_definition
		WHERE id = $1 AND archived_at IS NULL
	`
	var def model.TypeDefinition
	err := r.db.GetContext(ctx, &def, query, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// ArchiveTypeDefinition archives a type definition
func (r *postgresRepository) ArchiveTypeDefinition(ctx context.Context, id ulid.ULID) error {
	query := `
		UPDATE type_definition
		SET archived_at = $1
		WHERE id = $2 AND archived_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// ListTypeDefinitions lists type definitions based on filter
func (r *postgresRepository) ListTypeDefinitions(ctx context.Context, filter model.TypeDefinitionFilter) ([]*model.TypeDefinition, error) {
	query := `
		SELECT
			id,
			name,
			description,
			version,
			created_at,
			updated_at,
			archived_at
		FROM type_definition
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
			FROM type_definition td2
			WHERE td2.id = type_definition.id
		)`
	}

	if len(filter.InternalName) > 0 {
		query += fmt.Sprintf(" AND name = ANY($%d)", argNum)
		args = append(args, filter.InternalName)
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

	var defs []*model.TypeDefinition
	err := r.db.SelectContext(ctx, &defs, query, args...)
	if err != nil {
		return nil, err
	}

	return defs, nil
}

// UpsertTypeDefinition creates or updates a type definition
func (r *postgresRepository) UpsertTypeDefinition(ctx context.Context, def *model.TypeDefinition) error {
	query := `
		INSERT INTO type_definition (
			id,
			name,
			description,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			description = EXCLUDED.description,
			version = type_definition.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING version
	`
	now := time.Now()
	if def.CreatedAt.IsZero() {
		def.CreatedAt = now
	}
	def.UpdatedAt = now
	if def.Version == 0 {
		def.Version = 1
	}

	var newVersion int
	err := r.db.GetContext(ctx, &newVersion, query,
		def.ID,
		def.Name,
		def.Description,
		def.Version,
		def.CreatedAt,
		def.UpdatedAt,
	)
	if err == nil {
		def.Version = newVersion
	}
	return err
} 