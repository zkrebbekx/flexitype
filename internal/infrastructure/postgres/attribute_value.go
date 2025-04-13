package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid"
	"github.com/zkrebbekx/flexitype/internal/domain/model"
)

// UpsertAttributeValue creates or updates an attribute value
func (r *postgresRepository) UpsertAttributeValue(ctx context.Context, value *model.AttributeValue) error {
	query := `
		INSERT INTO attribute_value (
			id,
			attribute_definition_id,
			value,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET
			attribute_definition_id = EXCLUDED.attribute_definition_id,
			value = EXCLUDED.value,
			version = attribute_value.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING version
	`
	now := time.Now()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	if value.Version == 0 {
		value.Version = 1
	}

	valueJSON, err := json.Marshal(value.Value)
	if err != nil {
		return fmt.Errorf("failed to marshal value: %w", err)
	}

	var newVersion int
	err = r.db.GetContext(ctx, &newVersion, query,
		value.ID,
		value.AttributeDefinitionID,
		valueJSON,
		value.Version,
		value.CreatedAt,
		value.UpdatedAt,
	)
	if err == nil {
		value.Version = newVersion
	}
	return err
}

// GetAttributeValue retrieves an attribute value by ID
func (r *postgresRepository) GetAttributeValue(ctx context.Context, id ulid.ULID) (*model.AttributeValue, error) {
	query := `
		SELECT
			id,
			attribute_definition_id,
			value,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value
		WHERE id = $1 AND archived_at IS NULL
	`
	var val model.AttributeValue
	err := r.db.GetContext(ctx, &val, query, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON value
	if err := json.Unmarshal(val.Value, &val.Value); err != nil {
		return nil, fmt.Errorf("failed to unmarshal value: %w", err)
	}

	return &val, nil
}

// ArchiveAttributeValue archives an attribute value
func (r *postgresRepository) ArchiveAttributeValue(ctx context.Context, id ulid.ULID) error {
	query := `
		UPDATE attribute_value
		SET archived_at = $1
		WHERE id = $2 AND archived_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// ListAttributeValues lists attribute values based on filter
func (r *postgresRepository) ListAttributeValues(ctx context.Context, filter model.AttributeValueFilter) ([]*model.AttributeValue, error) {
	query := `
		SELECT
			id,
			attribute_definition_id,
			value,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if !filter.IncludeArchived {
		query += " AND archived_at IS NULL"
	}

	if len(filter.AttributeDefinitionID) > 0 {
		query += fmt.Sprintf(" AND attribute_definition_id = ANY($%d)", argNum)
		args = append(args, filter.AttributeDefinitionID)
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

	var vals []*model.AttributeValue
	err := r.db.SelectContext(ctx, &vals, query, args...)
	if err != nil {
		return nil, err
	}

	return vals, nil
}

// GetAttributeValuesByDefinitionID retrieves all attribute values for a given attribute definition
func (r *postgresRepository) GetAttributeValuesByDefinitionID(ctx context.Context, attributeDefinitionID ulid.ULID) ([]*model.AttributeValue, error) {
	query := `
		SELECT
			id,
			attribute_definition_id,
			value,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value
		WHERE attribute_definition_id = $1 AND archived_at IS NULL
		ORDER BY created_at DESC
	`
	var vals []*model.AttributeValue
	err := r.db.SelectContext(ctx, &vals, query, attributeDefinitionID)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON values for each attribute value
	for i := range vals {
		if err := json.Unmarshal(vals[i].Value, &vals[i].Value); err != nil {
			return nil, fmt.Errorf("failed to unmarshal value: %w", err)
		}
	}

	return vals, nil
} 