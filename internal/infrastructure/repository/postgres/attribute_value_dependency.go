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

// GetAttributeValueDependency retrieves an attribute value dependency by ID
func (r *Repository) GetAttributeValueDependency(ctx context.Context, id ulid.ULID) (*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			source_conditions,
			target_attribute_definition_id,
			target_values,
			target_constraints,
			description,
			validation_rules,
			is_required,
			default_value,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE id = $1 AND archived_at IS NULL
	`

	var dependency model.AttributeValueDependency
	var sourceConditions, targetValues, targetConstraints, validationRules, defaultValue []byte

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&dependency.ID,
		&dependency.SourceAttributeDefinitionID,
		&sourceConditions,
		&dependency.TargetAttributeDefinitionID,
		&targetValues,
		&targetConstraints,
		&dependency.Description,
		&validationRules,
		&dependency.IsRequired,
		&defaultValue,
		&dependency.Version,
		&dependency.CreatedAt,
		&dependency.UpdatedAt,
		&dependency.ArchivedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(sourceConditions, &dependency.SourceConditions); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(targetValues, &dependency.TargetValues); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(targetConstraints, &dependency.TargetConstraints); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(validationRules, &dependency.ValidationRules); err != nil {
		return nil, err
	}

	if err := json.Unmarshal(defaultValue, &dependency.DefaultValue); err != nil {
		return nil, err
	}

	return &dependency, nil
}

// ArchiveAttributeValueDependency archives an attribute value dependency
func (r *Repository) ArchiveAttributeValueDependency(ctx context.Context, id ulid.ULID) error {
	query := `
		UPDATE attribute_value_dependency
		SET archived_at = $1
		WHERE id = $2 AND archived_at IS NULL
	`

	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// ListAttributeValueDependencies lists attribute value dependencies based on filter
func (r *Repository) ListAttributeValueDependencies(ctx context.Context, filter model.AttributeValueDependencyFilter) ([]*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			source_conditions,
			target_attribute_definition_id,
			target_values,
			target_constraints,
			description,
			validation_rules,
			is_required,
			default_value,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE archived_at IS NULL
	`

	var args []interface{}
	if filter.SourceAttributeDefinitionID != nil {
		query += " AND source_attribute_definition_id = $" + string(len(args)+1)
		args = append(args, *filter.SourceAttributeDefinitionID)
	}

	if filter.TargetAttributeDefinitionID != nil {
		query += " AND target_attribute_definition_id = $" + string(len(args)+1)
		args = append(args, *filter.TargetAttributeDefinitionID)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dependencies []*model.AttributeValueDependency
	for rows.Next() {
		var dependency model.AttributeValueDependency
		var sourceConditions, targetValues, targetConstraints, validationRules, defaultValue []byte

		err := rows.Scan(
			&dependency.ID,
			&dependency.SourceAttributeDefinitionID,
			&sourceConditions,
			&dependency.TargetAttributeDefinitionID,
			&targetValues,
			&targetConstraints,
			&dependency.Description,
			&validationRules,
			&dependency.IsRequired,
			&defaultValue,
			&dependency.Version,
			&dependency.CreatedAt,
			&dependency.UpdatedAt,
			&dependency.ArchivedAt,
		)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(sourceConditions, &dependency.SourceConditions); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(targetValues, &dependency.TargetValues); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(targetConstraints, &dependency.TargetConstraints); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(validationRules, &dependency.ValidationRules); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(defaultValue, &dependency.DefaultValue); err != nil {
			return nil, err
		}

		dependencies = append(dependencies, &dependency)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return dependencies, nil
}

// GetDependentValues retrieves dependencies based on a source attribute definition ID
func (r *Repository) GetDependentValues(ctx context.Context, sourceAttributeDefinitionID ulid.ULID, sourceValue interface{}) ([]*model.AttributeValueDependency, error) {
	query := `
		SELECT
			id,
			source_attribute_definition_id,
			source_conditions,
			target_attribute_definition_id,
			target_values,
			target_constraints,
			description,
			validation_rules,
			is_required,
			default_value,
			version,
			created_at,
			updated_at,
			archived_at
		FROM attribute_value_dependency
		WHERE source_attribute_definition_id = $1 AND archived_at IS NULL
	`

	rows, err := r.db.QueryContext(ctx, query, sourceAttributeDefinitionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dependencies []*model.AttributeValueDependency
	for rows.Next() {
		var dependency model.AttributeValueDependency
		var sourceConditions, targetValues, targetConstraints, validationRules, defaultValue []byte

		err := rows.Scan(
			&dependency.ID,
			&dependency.SourceAttributeDefinitionID,
			&sourceConditions,
			&dependency.TargetAttributeDefinitionID,
			&targetValues,
			&targetConstraints,
			&dependency.Description,
			&validationRules,
			&dependency.IsRequired,
			&defaultValue,
			&dependency.Version,
			&dependency.CreatedAt,
			&dependency.UpdatedAt,
			&dependency.ArchivedAt,
		)
		if err != nil {
			return nil, err
		}

		if err := json.Unmarshal(sourceConditions, &dependency.SourceConditions); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(targetValues, &dependency.TargetValues); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(targetConstraints, &dependency.TargetConstraints); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(validationRules, &dependency.ValidationRules); err != nil {
			return nil, err
		}

		if err := json.Unmarshal(defaultValue, &dependency.DefaultValue); err != nil {
			return nil, err
		}

		dependencies = append(dependencies, &dependency)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return dependencies, nil
}

// UpsertAttributeValueDependency creates or updates an attribute value dependency
func (r *Repository) UpsertAttributeValueDependency(ctx context.Context, dep *model.AttributeValueDependency) error {
	query := `
		INSERT INTO attribute_value_dependency (
			id,
			source_attribute_id,
			source_conditions,
			target_attribute_id,
			target_values,
			target_constraints,
			description,
			version,
			created_at,
			updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			source_attribute_id = EXCLUDED.source_attribute_id,
			source_conditions = EXCLUDED.source_conditions,
			target_attribute_id = EXCLUDED.target_attribute_id,
			target_values = EXCLUDED.target_values,
			target_constraints = EXCLUDED.target_constraints,
			description = EXCLUDED.description,
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

	targetConstraintsJSON, err := json.Marshal(dep.TargetConstraints)
	if err != nil {
		return fmt.Errorf("failed to marshal target constraints: %w", err)
	}

	var newVersion int
	err = r.db.GetContext(ctx, &newVersion, query,
		dep.ID,
		dep.SourceAttributeID,
		sourceConditionsJSON,
		dep.TargetAttributeID,
		targetValuesJSON,
		targetConstraintsJSON,
		dep.Description,
		dep.Version,
		dep.CreatedAt,
		dep.UpdatedAt,
	)
	if err == nil {
		dep.Version = newVersion
	}
	return err
} 