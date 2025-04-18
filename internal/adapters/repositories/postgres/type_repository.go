package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
	"github.com/zac300/flexitype/internal/ports"
)

// TypeRepositoryImpl implements the TypeRepository interface
type TypeRepositoryImpl struct {
	repo *PostgresRepository
}

// NewTypeRepository creates a new PostgreSQL type repository
func NewTypeRepository(repo *PostgresRepository) *TypeRepositoryImpl {
	return &TypeRepositoryImpl{
		repo: repo,
	}
}

// GetByIDAndVersion retrieves a specific version of a type definition
func (r *TypeRepositoryImpl) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.TypeDefinition, error) {
	// Begin transaction
	/*tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()*/

	// Get type definition from version table
	var record struct {
		ID          string         `db:"id"`
		Version     int            `db:"version"`
		Name        string         `db:"name"`
		Description string         `db:"description"`
		ParentID    sql.NullString `db:"parent_type_id"`
		CreatedAt   time.Time      `db:"created_at"`
		ArchivedAt  sql.NullTime   `db:"archived_at"`
	}

	err := r.repo.db.GetContext(ctx, &record,
		`SELECT id, version, name, description, parent_type_id, created_at, archived_at 
		 FROM flexitype.type_definition_version 
		 WHERE id = $1 AND version = $2`,
		id, version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("type version '%s:%d' not found", id, version)
		}
		return nil, fmt.Errorf("failed to get type definition version: %w", err)
	}

	// Create type definition
	typeDef := core.NewTypeDefinition(record.ID, record.Name, record.Description)
	typeDef.Version = record.Version
	typeDef.CreatedAt = record.CreatedAt

	// Set archived status if present
	if record.ArchivedAt.Valid {
		archivedTime := record.ArchivedAt.Time
		typeDef.ArchivedAt = &archivedTime
	}

	// Load parent type if present
	if record.ParentID.Valid {
		// For parent types, always use the latest version by default
		parentType, err := r.GetByID(ctx, record.ParentID.String)
		if err != nil {
			// Log the error but continue - parent might be missing
			fmt.Printf("Warning: failed to load parent type %s: %v\n", record.ParentID.String, err)
		} else {
			typeDef.SetParentType(parentType)
		}
	}

	// Get versioned attributes
	var attributes []struct {
		ID           string `db:"id"`
		Name         string `db:"name"`
		Description  string `db:"description"`
		DataType     string `db:"data_type"`
		Required     bool   `db:"required"`
		DefaultValue string `db:"default_value"`
		MultiValued  bool   `db:"multi_valued"`
		Disabled     bool   `db:"disabled"`
	}

	err = r.repo.db.SelectContext(ctx, &attributes,
		`SELECT id, name, description, data_type, required, default_value, 
		 multi_valued, disabled
		 FROM flexitype.attribute_definition_version 
		 WHERE type_id = $1 AND type_version = $2`,
		id, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get versioned attributes: %w", err)
	}

	// Process attributes
	for _, attrRecord := range attributes {
		// Generate a stable attribute ID
		attrID := fmt.Sprintf("%s:%s:%d", id, attrRecord.Name, version)

		// Create attribute
		attr := core.NewAttributeDefinition(
			attrID,
			attrRecord.Name,
			attrRecord.Description,
			core.DataType(attrRecord.DataType),
			attrRecord.Required,
		)

		// Set other properties
		if attrRecord.DefaultValue != "" {
			attr.SetDefaultValue(attrRecord.DefaultValue)
		}

		attr.SetMultiValued(attrRecord.MultiValued)
		attr.SetDisabled(attrRecord.Disabled)

		// Get versioned cascades
		var cascades []struct {
			CascadeID             string          `db:"cascade_id"`
			Behavior              string          `db:"behavior"`
			Logic                 string          `db:"logic"`
			Weight                int             `db:"weight"`
			ValidationAction      sql.NullString  `db:"validation_action"`
			ValidationTargetField sql.NullString  `db:"validation_target_field"`
			ValidationValues      []byte          `db:"validation_values"`
			ValidationStringValue sql.NullString  `db:"validation_string_value"`
			ValidationNumericVal  sql.NullFloat64 `db:"validation_numeric_value"`
		}

		err = r.repo.db.SelectContext(ctx, &cascades,
			`SELECT cascade_id, behavior, logic, weight,
			 validation_action, validation_target_field, validation_values,
			 validation_string_value, validation_numeric_value
			 FROM flexitype.attribute_cascade_version
			 WHERE type_id = $1 AND type_version = $2 AND id = $3`,
			id, version, attrRecord.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get versioned cascades: %w", err)
		}

		// Process cascades
		for _, cascadeRecord := range cascades {
			// Add the cascade
			attr.AddCascade(
				cascadeRecord.CascadeID,
				true, // Always enabled in historical versions
				core.CascadeBehavior(cascadeRecord.Behavior),
				cascadeRecord.Logic,
				cascadeRecord.Weight,
			)

			// Get a reference to the cascade we just added
			if len(attr.Cascades) == 0 {
				continue // Safeguard against empty cascade list
			}
			cascadeIndex := len(attr.Cascades) - 1
			cascade := &attr.Cascades[cascadeIndex]

			// Add validation configuration if present
			if cascadeRecord.ValidationAction.Valid {
				// Create validation config
				validationConfig := &core.CascadeValidationConfig{
					Action: core.CascadeValidationAction(cascadeRecord.ValidationAction.String),
				}

				// Set target field if present
				if cascadeRecord.ValidationTargetField.Valid {
					validationConfig.TargetField = cascadeRecord.ValidationTargetField.String
				}

				// Parse values from JSON if present
				if len(cascadeRecord.ValidationValues) > 0 {
					var values []interface{}
					if err := json.Unmarshal(cascadeRecord.ValidationValues, &values); err != nil {
						return nil, fmt.Errorf("failed to unmarshal validation values: %w", err)
					}
					validationConfig.Values = values
				}

				// Set string value if present
				if cascadeRecord.ValidationStringValue.Valid {
					validationConfig.StringValue = cascadeRecord.ValidationStringValue.String
				}

				// Set numeric value if present
				if cascadeRecord.ValidationNumericVal.Valid {
					validationConfig.NumericValue = cascadeRecord.ValidationNumericVal.Float64
				}

				// Set validation config on cascade
				cascade.ValidationConfig = validationConfig
			}
		}

		// Get versioned validation rules
		var rules []struct {
			RuleType   string `db:"rule_type"`
			Parameters string `db:"parameters"`
		}

		err = r.repo.db.SelectContext(ctx, &rules,
			`SELECT rule_type, parameters
			 FROM flexitype.validation_rule_version
			 WHERE type_id = $1 AND type_version = $2 AND id = $3`,
			id, version, attrRecord.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get versioned validation rules: %w", err)
		}

		// Process validation rules
		for _, ruleRecord := range rules {
			// Create rule based on type
			rule, err := createValidationRule(ruleRecord.RuleType, ruleRecord.Parameters)
			if err != nil {
				return nil, fmt.Errorf("failed to create validation rule: %w", err)
			}

			attr.AddValidationRule(rule)
		}

		// Add attribute to type
		typeDef.AddAttribute(attr)
	}

	return typeDef, nil
}

// typeRecord is the database representation of a type definition
type typeRecord struct {
	ID          string         `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	ParentID    sql.NullString `db:"parent_type_id"`
	Version     int            `db:"version"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	ArchivedAt  sql.NullTime   `db:"archived_at"`
}

// Save persists a type definition
// Save persists a type definition, using type_definition_version as the primary storage
func (r *TypeRepositoryImpl) Save(ctx context.Context, typeDef *core.TypeDefinition) error {
	// Begin transaction
	/*tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure we rollback on error
	txErr := err
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()*/

	// Get parent ID if present
	var parentID sql.NullString
	if typeDef.ParentType != nil {
		parentID = sql.NullString{
			String: typeDef.ParentType.ID,
			Valid:  true,
		}
	}

	// Check if the type version already exists
	var exists bool
	err := r.repo.db.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition_version WHERE id = $1 AND version = $2)",
		typeDef.ID, typeDef.Version)
	if err != nil {
		return fmt.Errorf("failed to check if type version exists: %w", err)
	}

	// Set current timestamp for created_at and updated_at
	now := time.Now()
	var archivedAt sql.NullTime
	if typeDef.ArchivedAt != nil {
		archivedAt = sql.NullTime{
			Time:  *typeDef.ArchivedAt,
			Valid: true,
		}
	}

	// Insert or update the type definition in the version table
	if exists {
		// Update existing type version
		_, err = r.repo.db.ExecContext(ctx,
			`UPDATE flexitype.type_definition_version 
             SET name = $1, description = $2, parent_type_id = $3, updated_at = $4, archived_at = $5
             WHERE id = $6 AND version = $7`,
			typeDef.Name, typeDef.Description, parentID, now, archivedAt, typeDef.ID, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to update type definition version: %w", err)
		}

		// Delete existing attribute cascades for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.attribute_cascade_version 
			 WHERE type_id = $1 AND type_version = $2`,
			typeDef.ID, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing cascades: %w", err)
		}

		// Delete existing validation rules for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.validation_rule_version 
			 WHERE type_id = $1 AND type_version = $2`,
			typeDef.ID, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing validation rules: %w", err)
		}

		// Delete existing attributes for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.attribute_definition_version 
			 WHERE type_id = $1 AND type_version = $2`,
			typeDef.ID, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing attributes: %w", err)
		}
	} else {
		// Insert new type version
		_, err = r.repo.db.ExecContext(ctx,
			`INSERT INTO flexitype.type_definition_version (
				id, version, name, description, parent_type_id, 
				created_at, updated_at, archived_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			typeDef.ID, typeDef.Version, typeDef.Name, typeDef.Description,
			parentID, now, now, archivedAt)
		if err != nil {
			return fmt.Errorf("failed to insert type definition version: %w", err)
		}
	}

	// Save attributes
	for _, attr := range typeDef.Attributes {
		// Save attribute to version table
		_, err = r.repo.db.ExecContext(ctx,
			`INSERT INTO flexitype.attribute_definition_version (
				id, type_id, type_version, name, description, 
				data_type, required, default_value, multi_valued, 
				disabled, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			attr.ID, typeDef.ID, typeDef.Version, attr.Name, attr.Description,
			string(attr.DataType), attr.Required, formatDefaultValue(attr.DefaultValue),
			attr.MultiValued, attr.Disabled, now, now)
		if err != nil {
			return fmt.Errorf("failed to insert attribute version: %w", err)
		}

		// Save cascades to version table
		for _, cascade := range attr.Cascades {
			// Handle validation cascade values
			var validationAction, validationTargetField sql.NullString
			var validationValues []byte
			var validationStringValue sql.NullString
			var validationNumericValue sql.NullFloat64

			// Set validation fields if this is a validation cascade
			if cascade.ValidationConfig != nil {
				validationAction = sql.NullString{
					String: string(cascade.ValidationConfig.Action),
					Valid:  true,
				}

				if cascade.ValidationConfig.TargetField != "" {
					validationTargetField = sql.NullString{
						String: cascade.ValidationConfig.TargetField,
						Valid:  true,
					}
				}

				// Convert values array to JSON if present
				if len(cascade.ValidationConfig.Values) > 0 {
					valuesJSON, err := json.Marshal(cascade.ValidationConfig.Values)
					if err != nil {
						return fmt.Errorf("failed to marshal validation values: %w", err)
					}
					validationValues = valuesJSON
				}

				// Set string or numeric value if present
				if cascade.ValidationConfig.StringValue != "" {
					validationStringValue = sql.NullString{
						String: cascade.ValidationConfig.StringValue,
						Valid:  true,
					}
				}

				if cascade.ValidationConfig.NumericValue != 0 {
					validationNumericValue = sql.NullFloat64{
						Float64: cascade.ValidationConfig.NumericValue,
						Valid:   true,
					}
				}
			}

			_, err = r.repo.db.ExecContext(ctx,
				`INSERT INTO flexitype.attribute_cascade_version (
					type_id, type_version, attribute_id, cascade_id,
					behavior, logic, weight, validation_action,
					validation_target_field, validation_values,
					validation_string_value, validation_numeric_value,
					created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
				typeDef.ID, typeDef.Version, attr.ID, cascade.ID,
				string(cascade.Behavior), cascade.Logic, cascade.Weight,
				validationAction, validationTargetField, validationValues,
				validationStringValue, validationNumericValue, now)
			if err != nil {
				return fmt.Errorf("failed to insert cascade version: %w", err)
			}
		}

		// Save validation rules to version table
		for i, rule := range attr.ValidationRules {
			ruleType := fmt.Sprintf("%T", rule)
			ruleParams, err := json.Marshal(rule)
			if err != nil {
				return fmt.Errorf("failed to marshal validation rule: %w", err)
			}

			_, err = r.repo.db.ExecContext(ctx,
				`INSERT INTO flexitype.validation_rule_version (
					type_id, type_version, attribute_id, rule_type,
					parameters, sort_order, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				typeDef.ID, typeDef.Version, attr.ID, ruleType,
				string(ruleParams), i, now)
			if err != nil {
				return fmt.Errorf("failed to insert validation rule version: %w", err)
			}
		}
	}

	// Commit transaction
	/*if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}*/

	return nil
}

// formatDefaultValue converts a default value to a string representation
func formatDefaultValue(value interface{}) string {
	if value == nil {
		return ""
	}

	return fmt.Sprintf("%v", value)
}

// attributeRecord is the database representation of an attribute definition
type attributeRecord struct {
	ID           string `db:"id"`
	Name         string `db:"name"`
	Description  string `db:"description"`
	DataType     string `db:"data_type"`
	Required     bool   `db:"required"`
	DefaultValue string `db:"default_value"`
	MultiValued  bool   `db:"multi_valued"`
	Disabled     bool   `db:"disabled"`
}

// cascadeRecord is the database representation of an attribute cascade
type cascadeRecord struct {
	ID                    int64           `db:"id"`
	AttributeID           string          `db:"attribute_id"`
	CascadeID             string          `db:"cascade_id"`
	Enabled               bool            `db:"enabled"`
	Behavior              string          `db:"behavior"`
	Logic                 string          `db:"logic"`
	Weight                int             `db:"weight"`
	ValidationAction      sql.NullString  `db:"validation_action"`
	ValidationTargetField sql.NullString  `db:"validation_target_field"`
	ValidationValues      []byte          `db:"validation_values"`
	ValidationStringValue sql.NullString  `db:"validation_string_value"`
	ValidationNumericVal  sql.NullFloat64 `db:"validation_numeric_value"`
}

// validationRuleRecord is the database representation of a validation rule
type validationRuleRecord struct {
	RuleType   string `db:"rule_type"`
	Parameters string `db:"parameters"`
}

// createValidationRule creates a validation rule based on its type and parameters
func createValidationRule(ruleType, parameters string) (validation.Rule, error) {
	// This is a simplified implementation - in a real system, you would
	// parse the parameters and create the appropriate rule type

	// For now, create a simple required rule
	if strings.Contains(ruleType, "RequiredRule") {
		return &validation.RequiredRule{}, nil
	}

	// Default to a generic rule that always succeeds
	return &validation.GenericRule{}, nil
}

// GetByID retrieves a type definition by ID
func (r *TypeRepositoryImpl) GetByID(ctx context.Context, id string) (*core.TypeDefinition, error) {
	// Begin transaction
	/*tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()*/

	// Get type definition (including archived)
	var record typeRecord
	err := r.repo.db.GetContext(ctx, &record,
		`SELECT id, name, description, parent_type_id, version, created_at, updated_at, archived_at 
         FROM flexitype.type_definition_version
         WHERE id = $1
         ORDER BY version DESC LIMIT 1`,
		id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("type with id '%s' not found", id)
		}
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}

	// Create type definition
	typeDef := core.NewTypeDefinition(record.ID, record.Name, record.Description)
	typeDef.Version = record.Version
	typeDef.CreatedAt = record.CreatedAt
	typeDef.UpdatedAt = record.UpdatedAt

	// Set archived status if present
	if record.ArchivedAt.Valid {
		archivedTime := record.ArchivedAt.Time
		typeDef.ArchivedAt = &archivedTime
	}

	// Load parent type if present
	if record.ParentID.Valid {
		parentType, err := r.GetByID(ctx, record.ParentID.String)
		if err != nil {
			// Log the error but continue - parent might be missing
			fmt.Printf("Warning: failed to load parent type %s: %v\n", record.ParentID.String, err)
		} else {
			typeDef.SetParentType(parentType)
		}
	}

	// Get attributes
	var attributes []*attributeRecord
	err = r.repo.db.SelectContext(ctx, &attributes,
		`SELECT id, name, description, data_type, required, default_value, 
		 multi_valued, disabled
		 FROM flexitype.attribute_definition_version 
		 WHERE type_id = $1 AND type_version = $2`,
		record.ID, record.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get attributes: %w", err)
	}

	// Process attributes
	for _, attrRecord := range attributes {
		// Create attribute
		attr := core.NewAttributeDefinition(
			attrRecord.ID,
			attrRecord.Name,
			attrRecord.Description,
			core.DataType(attrRecord.DataType),
			attrRecord.Required,
		)

		// Set other properties
		if attrRecord.DefaultValue != "" {
			attr.SetDefaultValue(attrRecord.DefaultValue)
		}

		attr.SetMultiValued(attrRecord.MultiValued)
		attr.SetDisabled(attrRecord.Disabled)

		// Get cascades
		var cascades []*cascadeRecord
		err = r.repo.db.SelectContext(ctx, &cascades,
			`SELECT id, attribute_id, cascade_id, enabled, behavior, logic, weight,
			 validation_action, validation_target_field, validation_values,
			 validation_string_value, validation_numeric_value
			 FROM flexitype.attribute_cascade_version
			 WHERE attribute_id = $1
			 ORDER BY type_version DESC LIMIT 1`,
			attrRecord.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get cascades: %w", err)
		}

		// Process cascades
		for _, cascadeRecord := range cascades {
			// Add the cascade
			attr.AddCascade(
				cascadeRecord.CascadeID,
				cascadeRecord.Enabled,
				core.CascadeBehavior(cascadeRecord.Behavior),
				cascadeRecord.Logic,
				cascadeRecord.Weight,
			)

			// Get a reference to the cascade we just added
			if len(attr.Cascades) == 0 {
				continue // Safeguard against empty cascade list
			}
			cascadeIndex := len(attr.Cascades) - 1
			cascade := &attr.Cascades[cascadeIndex]

			// Add validation configuration if present
			if cascadeRecord.ValidationAction.Valid {
				// Create validation config
				validationConfig := &core.CascadeValidationConfig{
					Action: core.CascadeValidationAction(cascadeRecord.ValidationAction.String),
				}

				// Set target field if present
				if cascadeRecord.ValidationTargetField.Valid {
					validationConfig.TargetField = cascadeRecord.ValidationTargetField.String
				}

				// Parse values from JSON if present
				if len(cascadeRecord.ValidationValues) > 0 {
					var values []interface{}
					if err := json.Unmarshal(cascadeRecord.ValidationValues, &values); err != nil {
						return nil, fmt.Errorf("failed to unmarshal validation values: %w", err)
					}
					validationConfig.Values = values
				}

				// Set string value if present
				if cascadeRecord.ValidationStringValue.Valid {
					validationConfig.StringValue = cascadeRecord.ValidationStringValue.String
				}

				// Set numeric value if present
				if cascadeRecord.ValidationNumericVal.Valid {
					validationConfig.NumericValue = cascadeRecord.ValidationNumericVal.Float64
				}

				// Set validation config on cascade
				cascade.ValidationConfig = validationConfig
			}
		}

		// Get validation rules
		var rules []*validationRuleRecord
		err = r.repo.db.SelectContext(ctx, &rules,
			`SELECT rule_type, parameters
			 FROM flexitype.validation_rule
			 WHERE attribute_id = $1`,
			attrRecord.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get validation rules: %w", err)
		}

		// Process validation rules
		for _, ruleRecord := range rules {
			// Create rule based on type
			rule, err := createValidationRule(ruleRecord.RuleType, ruleRecord.Parameters)
			if err != nil {
				return nil, fmt.Errorf("failed to create validation rule: %w", err)
			}

			attr.AddValidationRule(rule)
		}

		// Add attribute to type
		typeDef.AddAttribute(attr)
	}

	return typeDef, nil
}

// GetByName retrieves a type definition by name
func (r *TypeRepositoryImpl) GetByName(ctx context.Context, name string) (*core.TypeDefinition, error) {
	// First, get the ID of the type with this name
	var id string
	err := r.repo.db.GetContext(ctx, &id,
		`SELECT id 
         FROM flexitype.type_definition_version 
         WHERE name = $1
		 ORDER BY version DESC LIMIT 1`,
		name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("type with name '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to get type id: %w", err)
	}

	// Now use GetByID to fetch the full type definition
	return r.GetByID(ctx, id)
}

// GetByIDs retrieves multiple type definitions by IDs
func (r *TypeRepositoryImpl) GetByIDs(ctx context.Context, ids []string) ([]*core.TypeDefinition, error) {
	if len(ids) == 0 {
		return make([]*core.TypeDefinition, 0), nil
	}

	// Fetch each type definition
	types := make([]*core.TypeDefinition, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		typeDef, err := r.GetByID(ctx, id)
		if err != nil {
			// Record not found IDs but continue with others
			if strings.Contains(err.Error(), "not found") {
				notFound = append(notFound, id)
				continue
			}
			return nil, fmt.Errorf("failed to get type definition %s: %w", id, err)
		}
		types = append(types, typeDef)
	}

	if len(notFound) > 0 {
		return types, fmt.Errorf("some types not found: %v", notFound)
	}

	return types, nil
}

// List retrieves all type definitions with pagination and filtering
func (r *TypeRepositoryImpl) List(ctx context.Context, options *ports.QueryOptions) ([]*core.TypeDefinition, int, error) {
	if options == nil {
		options = ports.DefaultQueryOptions()
	}

	// Build the base query
	query := "SELECT id FROM flexitype.type_definition_version"
	countQuery := "SELECT COUNT(*) FROM flexitype.type_definition_version"

	// Build WHERE clauses
	whereClause, args := r.buildWhereClause(options)
	if whereClause != "" {
		query += " WHERE " + whereClause
		countQuery += " WHERE " + whereClause
	}

	// Add ORDER BY
	if options.OrderBy != "" {
		orderBy := sanitizeOrderBy(options.OrderBy)
		orderDir := "ASC"
		if strings.ToUpper(options.OrderDir) == "DESC" {
			orderDir = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)
	} else {
		query += " ORDER BY id ASC"
	}

	// Add LIMIT and OFFSET for pagination
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", options.Limit, options.Offset)

	// Execute count query
	var totalCount int
	err := r.repo.db.GetContext(ctx, &totalCount, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count: %w", err)
	}

	// Execute main query
	var typeIDs []string
	err = r.repo.db.SelectContext(ctx, &typeIDs, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list type IDs: %w", err)
	}

	// Fetch each type definition
	types := make([]*core.TypeDefinition, 0, len(typeIDs))
	for _, id := range typeIDs {
		typeDef, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get type definition %s: %w", id, err)
		}
		types = append(types, typeDef)
	}

	return types, totalCount, nil
}

// buildWhereClause builds the WHERE clause for the query based on query options
func (r *TypeRepositoryImpl) buildWhereClause(options *ports.QueryOptions) (string, []interface{}) {
	var conditions []string
	var args []interface{}
	argIndex := 1

	// Filter by archived status
	if !options.IncludeArchived {
		conditions = append(conditions, "archived_at IS NULL")
	}

	// Filter by IDs
	if len(options.IDs) > 0 {
		placeholders := make([]string, len(options.IDs))
		for i, id := range options.IDs {
			placeholders[i] = fmt.Sprintf("$%d", argIndex)
			args = append(args, id)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Filter by name
	if options.Name != "" {
		conditions = append(conditions, fmt.Sprintf("name = $%d", argIndex))
		args = append(args, options.Name)
		argIndex++
	}

	// Filter by description
	if options.Description != "" {
		conditions = append(conditions, fmt.Sprintf("description LIKE $%d", argIndex))
		args = append(args, "%"+options.Description+"%")
		argIndex++
	}

	// Filter by version
	if options.Version > 0 {
		conditions = append(conditions, fmt.Sprintf("version = $%d", argIndex))
		args = append(args, options.Version)
		argIndex++
	}

	// Filter by name in list
	if len(options.NameIn) > 0 {
		placeholders := make([]string, len(options.NameIn))
		for i, name := range options.NameIn {
			placeholders[i] = fmt.Sprintf("$%d", argIndex)
			args = append(args, name)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("name IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Filter by version in list
	if len(options.VersionIn) > 0 {
		placeholders := make([]string, len(options.VersionIn))
		for i, version := range options.VersionIn {
			placeholders[i] = fmt.Sprintf("$%d", argIndex)
			args = append(args, version)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("version IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Combine all conditions with AND
	whereClause := ""
	if len(conditions) > 0 {
		whereClause = strings.Join(conditions, " AND ")
	}

	return whereClause, args
}

// sanitizeOrderBy sanitizes order by column to prevent SQL injection
func sanitizeOrderBy(orderBy string) string {
	// Allow only specific columns
	allowedColumns := map[string]bool{
		"id":          true,
		"name":        true,
		"description": true,
		"version":     true,
		"created_at":  true,
		"updated_at":  true,
	}

	// Lowercase and trim the column name
	orderBy = strings.ToLower(strings.TrimSpace(orderBy))

	// Check if the column is allowed
	if allowedColumns[orderBy] {
		return orderBy
	}

	// Default to id
	return "id"
}

// Archive marks a type definition as archived at the current time
func (r *TypeRepositoryImpl) Archive(ctx context.Context, id string) error {
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.type_definition_version SET archived_at = NOW() WHERE id = $1 AND archived_at IS NULL",
		id)
	if err != nil {
		return fmt.Errorf("failed to archive type definition: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Check if the type exists but is already archived
		var exists bool
		err = r.repo.db.GetContext(ctx, &exists,
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition_version WHERE id = $1)",
			id)

		if err != nil {
			return fmt.Errorf("failed to check if type exists: %w", err)
		}

		if exists {
			return fmt.Errorf("type with id '%s' is already archived", id)
		}

		return fmt.Errorf("type with id '%s' not found", id)
	}

	return nil
}

// Unarchive removes the archived status from a type definition
func (r *TypeRepositoryImpl) Unarchive(ctx context.Context, id string) error {
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.type_definition_version SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL",
		id)
	if err != nil {
		return fmt.Errorf("failed to unarchive type definition: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Check if the type exists but is not archived
		var exists bool
		err = r.repo.db.GetContext(ctx, &exists,
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition_version WHERE id = $1)",
			id)

		if err != nil {
			return fmt.Errorf("failed to check if type exists: %w", err)
		}

		if exists {
			return fmt.Errorf("type with id '%s' is not archived", id)
		}

		return fmt.Errorf("type with id '%s' not found", id)
	}

	return nil
}

// ArchiveMany marks multiple type definitions as archived
func (r *TypeRepositoryImpl) ArchiveMany(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Create a query with multiple parameters
	query := "UPDATE flexitype.type_definition_version SET archived_at = NOW() WHERE id IN (?" + strings.Repeat(",?", len(ids)-1) + ") AND archived_at IS NULL"
	query = sqlx.Rebind(sqlx.DOLLAR, query) // Convert ? placeholders to $1, $2, etc.

	// Convert ids to interface{} slice for the query
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	// Execute the update
	result, err := r.repo.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to archive type definitions: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no types were archived (they may not exist or are already archived)")
	}

	// TODO - Check if all types were archived
	if int(rowsAffected) < len(ids) {
		return fmt.Errorf("not all types were archived (some may not exist or are already archived)")
	}

	return nil
}
