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

// UpsertAttributeValueDependency creates or updates an attribute value dependency
func (r *postgresRepository) UpsertAttributeValueDependency(ctx context.Context, dep *model.AttributeValueDependency) error {
	query := `
		INSERT INTO attribute_value_dependency (
			id,
			source_attribute_definition_id,
			target_attribute_definition_id,
			source_conditions,
			target_values,
			validation_rules,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (id) DO UPDATE SET
			source_attribute_definition_id = EXCLUDED.source_attribute_definition_id,
			target_attribute_definition_id = EXCLUDED.target_attribute_definition_id,
			source_conditions = EXCLUDED.source_conditions,
			target_values = EXCLUDED.target_values,
			validation_rules = EXCLUDED.validation_rules,
			version = attribute_value_dependency.version + 1,
			updated_at = EXCLUDED.updated_at
		RETURNING version
	`
	now := time.Now()
	if dep.CreatedAt.IsZero() {
		dep.CreatedAt = now
	}
	dep.UpdatedAt = now
	if dep.Version == 0 {
		dep.Version = 1
	}

	sourceConditionsJSON, err := json.Marshal(dep.SourceConditions)
	if err != nil {
		return fmt.Errorf("failed to marshal source conditions: %w", err)
	}

	targetValuesJSON, err := json.Marshal(dep.TargetValues)
	if err != nil {
		return fmt.Errorf("failed to marshal target values: %w", err)
	}

	validationRulesJSON, err := json.Marshal(dep.ValidationRules)
	if err != nil {
		return fmt.Errorf("failed to marshal validation rules: %w", err)
	}

	var newVersion int
	err = r.db.GetContext(ctx, &newVersion, query,
		dep.ID,
		dep.SourceAttributeDefinitionID,
		dep.TargetAttributeDefinitionID,
		sourceConditionsJSON,
		targetValuesJSON,
		validationRulesJSON,
		dep.Version,
		dep.CreatedAt,
		dep.UpdatedAt,
	)
	if err == nil {
		dep.Version = newVersion
	}
	return err
}

// GetAttributeValueDependency retrieves an attribute value dependency by ID
func (r *postgresRepository) GetAttributeValueDependency(ctx context.Context, id ulid.ULID) (*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			target_attribute_definition_id,
			source_conditions,
			target_values,
			validation_rules,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE id = $1 AND archived_at IS NULL
	`
	var dep model.AttributeValueDependency
	err := r.db.GetContext(ctx, &dep, query, id)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields
	if err := json.Unmarshal(dep.SourceConditions, &dep.SourceConditions); err != nil {
		return nil, fmt.Errorf("failed to unmarshal source conditions: %w", err)
	}
	if err := json.Unmarshal(dep.TargetValues, &dep.TargetValues); err != nil {
		return nil, fmt.Errorf("failed to unmarshal target values: %w", err)
	}
	if err := json.Unmarshal(dep.ValidationRules, &dep.ValidationRules); err != nil {
		return nil, fmt.Errorf("failed to unmarshal validation rules: %w", err)
	}

	return &dep, nil
}

// ArchiveAttributeValueDependency archives an attribute value dependency
func (r *postgresRepository) ArchiveAttributeValueDependency(ctx context.Context, id ulid.ULID) error {
	query := `
		UPDATE attribute_value_dependency
		SET archived_at = $1
		WHERE id = $2 AND archived_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// ListAttributeValueDependencies lists attribute value dependencies based on filter
func (r *postgresRepository) ListAttributeValueDependencies(ctx context.Context, filter model.AttributeValueDependencyFilter) ([]*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			target_attribute_definition_id,
			source_conditions,
			target_values,
			validation_rules,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE 1=1
	`
	args := []interface{}{}
	argNum := 1

	if !filter.IncludeArchived {
		query += fmt.Sprintf(" AND archived_at IS NULL")
	}

	if filter.Version > 0 {
		query += fmt.Sprintf(" AND version = $%d", argNum)
		args = append(args, filter.Version)
		argNum++
	}

	if filter.OnlyLatest {
		query += ` AND version = (
			SELECT MAX(version)
			FROM attribute_value_dependency avd2
			WHERE avd2.id = attribute_value_dependency.id
		)`
	}

	if len(filter.SourceAttributeID) > 0 {
		query += fmt.Sprintf(" AND source_attribute_definition_id = ANY($%d)", argNum)
		args = append(args, filter.SourceAttributeID)
		argNum++
	}

	if len(filter.TargetAttributeID) > 0 {
		query += fmt.Sprintf(" AND target_attribute_definition_id = ANY($%d)", argNum)
		args = append(args, filter.TargetAttributeID)
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

	var deps []*model.AttributeValueDependency
	err := r.db.SelectContext(ctx, &deps, query, args...)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields for each dependency
	for i := range deps {
		if err := json.Unmarshal(deps[i].SourceConditions, &deps[i].SourceConditions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal source conditions: %w", err)
		}
		if err := json.Unmarshal(deps[i].TargetValues, &deps[i].TargetValues); err != nil {
			return nil, fmt.Errorf("failed to unmarshal target values: %w", err)
		}
		if err := json.Unmarshal(deps[i].ValidationRules, &deps[i].ValidationRules); err != nil {
			return nil, fmt.Errorf("failed to unmarshal validation rules: %w", err)
		}
	}

	return deps, nil
}

// GetDependentValues retrieves dependent values for a given source attribute value
func (r *postgresRepository) GetDependentValues(ctx context.Context, sourceAttributeDefinitionID ulid.ULID, sourceValue interface{}) ([]*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			target_attribute_definition_id,
			source_conditions,
			target_values,
			validation_rules,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE source_attribute_definition_id = $1 AND archived_at IS NULL
	`
	var deps []*model.AttributeValueDependency
	err := r.db.SelectContext(ctx, &deps, query, sourceAttributeDefinitionID)
	if err != nil {
		return nil, err
	}

	// Unmarshal JSON fields for each dependency
	for i := range deps {
		if err := json.Unmarshal(deps[i].SourceConditions, &deps[i].SourceConditions); err != nil {
			return nil, fmt.Errorf("failed to unmarshal source conditions: %w", err)
		}
		if err := json.Unmarshal(deps[i].TargetValues, &deps[i].TargetValues); err != nil {
			return nil, fmt.Errorf("failed to unmarshal target values: %w", err)
		}
		if err := json.Unmarshal(deps[i].ValidationRules, &deps[i].ValidationRules); err != nil {
			return nil, fmt.Errorf("failed to unmarshal validation rules: %w", err)
		}
	}

	// Filter dependencies based on source value
	var filteredDeps []*model.AttributeValueDependency
	for _, dep := range deps {
		if dep.EvaluateConditions(sourceValue) {
			filteredDeps = append(filteredDeps, dep)
		}
	}

	return filteredDeps, nil
} 