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

// GetByNameAndVersion retrieves a specific version of a type definition by name
func (r *TypeRepositoryImpl) GetByNameAndVersion(ctx context.Context, name string, version int) (*core.TypeDefinition, error) {
	// Get type definition from version table
	var record struct {
		Version     int            `db:"version"`
		Name        string         `db:"name"`
		Description string         `db:"description"`
		ParentName  sql.NullString `db:"parent_type_name"`
		CreatedAt   time.Time      `db:"created_at"`
		ArchivedAt  sql.NullTime   `db:"archived_at"`
	}

	err := r.repo.db.GetContext(ctx, &record,
		`SELECT version, name, description, parent_type_name, created_at, archived_at 
		 FROM flexitype.type_definition 
		 WHERE LOWER(name) = LOWER($1) AND version = $2`,
		name, version)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("type version '%s:%d' not found", name, version)
		}
		return nil, fmt.Errorf("failed to get type definition version: %w", err)
	}

	// Create type definition - use name as the stable identifier now
	typeDef := core.NewTypeDefinition(record.Name, record.Description)
	typeDef.Version = record.Version
	typeDef.CreatedAt = record.CreatedAt

	// Set archived status if present
	if record.ArchivedAt.Valid {
		archivedTime := record.ArchivedAt.Time
		typeDef.ArchivedAt = &archivedTime
	}

	// Load parent type if present
	if record.ParentName.Valid {
		// For parent types, always use the latest version by default
		parentType, err := r.GetByName(ctx, record.ParentName.String)
		if err != nil {
			// Log the error but continue - parent might be missing
			fmt.Printf("Warning: failed to load parent type %s: %v\n", record.ParentName.String, err)
		} else {
			typeDef.SetParentType(parentType)
		}
	}

	// Get versioned attributes
	var attributes []struct {
		Name         string `db:"name"`
		Description  string `db:"description"`
		DataType     string `db:"data_type"`
		Required     bool   `db:"required"`
		DefaultValue string `db:"default_value"`
		MultiValued  bool   `db:"multi_valued"`
		Disabled     bool   `db:"disabled"`
	}

	err = r.repo.db.SelectContext(ctx, &attributes,
		`SELECT name, description, data_type, required, default_value, 
		 multi_valued, disabled
		 FROM flexitype.attribute_definition 
		 WHERE type_name = $1 AND type_version = $2`,
		record.Name, version)
	if err != nil {
		return nil, fmt.Errorf("failed to get versioned attributes: %w", err)
	}

	// Process attributes
	for _, attrRecord := range attributes {
		// Create attribute
		attr := core.NewAttributeDefinition(
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
			 FROM flexitype.attribute_cascade
			 WHERE type_name = $1 AND type_version = $2 AND attribute_name = $3`,
			record.Name, version, attrRecord.Name)
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
			 FROM flexitype.validation_rule
			 WHERE type_name = $1 AND type_version = $2 AND attribute_name = $3`,
			record.Name, version, attrRecord.Name)
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
	Name        string         `db:"name"`
	Description string         `db:"description"`
	ParentName  sql.NullString `db:"parent_type_name"`
	Version     int            `db:"version"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
	ArchivedAt  sql.NullTime   `db:"archived_at"`
}

// Save persists a type definition
// Save persists a type definition using the name as the primary key
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

	// Get parent name if present
	var parentName sql.NullString
	if typeDef.ParentType != nil {
		parentName = sql.NullString{
			String: typeDef.ParentType.Name,
			Valid:  true,
		}
	}

	// Check if the type version already exists using name as primary key
	var exists bool
	err := r.repo.db.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE LOWER(name) = LOWER($1) AND version = $2)",
		typeDef.Name, typeDef.Version)
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
		// Update existing type version using name as primary key
		_, err = r.repo.db.ExecContext(ctx,
			`UPDATE flexitype.type_definition 
             SET description = $1, parent_type_name = $2, updated_at = $3, archived_at = $4
             WHERE LOWER(name) = LOWER($5) AND version = $6`,
			typeDef.Description, parentName, now, archivedAt, typeDef.Name, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to update type definition version: %w", err)
		}

		// Delete existing attribute cascades for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.attribute_cascade 
			 WHERE type_name = $1 AND type_version = $2`,
			typeDef.Name, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing cascades: %w", err)
		}

		// Delete existing validation rules for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.validation_rule 
			 WHERE type_name = $1 AND type_version = $2`,
			typeDef.Name, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing validation rules: %w", err)
		}

		// Delete existing attributes for this type version
		_, err = r.repo.db.ExecContext(ctx,
			`DELETE FROM flexitype.attribute_definition 
			 WHERE type_name = $1 AND type_version = $2`,
			typeDef.Name, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing attributes: %w", err)
		}
	} else {
		// Insert new type version - name is the primary key
		_, err = r.repo.db.ExecContext(ctx,
			`INSERT INTO flexitype.type_definition (
				version, name, description, parent_type_name, 
				created_at, updated_at, archived_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			typeDef.Version, typeDef.Name, typeDef.Description,
			parentName, now, now, archivedAt)
		if err != nil {
			return fmt.Errorf("failed to insert type definition version: %w", err)
		}
	}

	// Save attributes using only name references
	for _, attr := range typeDef.Attributes {
		// Save attribute to version table
		_, err = r.repo.db.ExecContext(ctx,
			`INSERT INTO flexitype.attribute_definition (
				type_version, type_name, name, description, 
				data_type, required, default_value, multi_valued, 
				disabled, created_at, updated_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
			typeDef.Version, typeDef.Name, attr.Name, attr.Description,
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
				`INSERT INTO flexitype.attribute_cascade (
					type_version, type_name, attribute_name, cascade_id,
					behavior, logic, weight, validation_action,
					validation_target_field, validation_values,
					validation_string_value, validation_numeric_value,
					created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
				typeDef.Version, typeDef.Name, attr.Name, cascade.ID,
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
				`INSERT INTO flexitype.validation_rule (
					type_version, type_name, attribute_name, rule_type,
					parameters, sort_order, created_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				typeDef.Version, typeDef.Name, attr.Name, ruleType,
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
	AttributeName         string          `db:"attribute_name"`
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

// GetByName retrieves a type definition by name
func (r *TypeRepositoryImpl) GetByName(ctx context.Context, name string) (*core.TypeDefinition, error) {
	var record typeRecord
	err := r.repo.db.GetContext(ctx, &record,
		`SELECT name, description, parent_type_name, version, created_at, updated_at, archived_at 
		 FROM flexitype.type_definition
		 WHERE LOWER(name) = LOWER($1)
		 ORDER BY version DESC LIMIT 1`,
		name)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("type with name '%s' not found", name)
		}
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}

	// Create type definition
	typeDef := core.NewTypeDefinition(record.Name, record.Description)
	typeDef.Version = record.Version
	typeDef.CreatedAt = record.CreatedAt
	typeDef.UpdatedAt = record.UpdatedAt

	// Set archived status if present
	if record.ArchivedAt.Valid {
		archivedTime := record.ArchivedAt.Time
		typeDef.ArchivedAt = &archivedTime
	}

	// Load parent type if present
	if record.ParentName.Valid {
		parentType, err := r.GetByName(ctx, record.ParentName.String)
		if err != nil {
			// Log the error but continue - parent might be missing
			fmt.Printf("Warning: failed to load parent type %s: %v\n", record.ParentName.String, err)
		} else {
			typeDef.SetParentType(parentType)
		}
	}

	// Get attributes using the type name
	var attributes []*attributeRecord
	err = r.repo.db.SelectContext(ctx, &attributes,
		`SELECT name, description, data_type, required, default_value, 
		 multi_valued, disabled
		 FROM flexitype.attribute_definition 
		 WHERE type_name = $1 AND type_version = $2`,
		record.Name, record.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get attributes: %w", err)
	}

	// Process attributes
	for _, attrRecord := range attributes {
		// Create attribute
		attr := core.NewAttributeDefinition(
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
			`SELECT id, attribute_name, cascade_id, enabled, behavior, logic, weight,
			 validation_action, validation_target_field, validation_values,
			 validation_string_value, validation_numeric_value
			 FROM flexitype.attribute_cascade
			 WHERE attribute_name = $1
			 ORDER BY type_version DESC LIMIT 1`,
			attrRecord.Name)
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
			 WHERE type_name = $1 AND type_version = $2 AND attribute_name = $3`,
			record.Name, record.Version, attrRecord.Name)
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

// List retrieves all type definitions with pagination and filtering
func (r *TypeRepositoryImpl) List(ctx context.Context, options *ports.QueryOptions) ([]*core.TypeDefinition, int, error) {
	if options == nil {
		options = ports.DefaultQueryOptions()
	}

	// Build the base query - now querying for name as the primary key
	query := "SELECT name FROM flexitype.type_definition"
	countQuery := "SELECT COUNT(*) FROM flexitype.type_definition"

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
		query += " ORDER BY name ASC" // Default sort by name now
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
	var typeNames []string
	err = r.repo.db.SelectContext(ctx, &typeNames, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list type names: %w", err)
	}

	// Fetch each type definition by name
	types := make([]*core.TypeDefinition, 0, len(typeNames))
	for _, name := range typeNames {
		typeDef, err := r.GetByName(ctx, name)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get type definition %s: %w", name, err)
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
	if len(options.Names) > 0 {
		placeholders := make([]string, len(options.Names))
		for i, id := range options.Names {
			placeholders[i] = strings.ToLower(fmt.Sprintf("$%d", argIndex))
			args = append(args, id)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("LOWER(name) IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Filter by name - case insensitive
	if options.Name != "" {
		conditions = append(conditions, fmt.Sprintf("LOWER(name) = LOWER($%d)", argIndex))
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

	// Filter by name in list - case insensitive
	if len(options.NameIn) > 0 {
		nameClauses := make([]string, len(options.NameIn))
		for i, name := range options.NameIn {
			nameClauses[i] = fmt.Sprintf("LOWER(name) = LOWER($%d)", argIndex)
			args = append(args, name)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("(%s)", strings.Join(nameClauses, " OR ")))
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

	// Default to name (the new primary key)
	return "name"
}

// Archive marks a type definition as archived at the current time
func (r *TypeRepositoryImpl) Archive(ctx context.Context, name string) error {
	// First get the type name from ID
	var typeName string
	err := r.repo.db.GetContext(ctx, &typeName,
		`SELECT name FROM flexitype.type_definition WHERE name = $1 ORDER BY version DESC LIMIT 1`,
		name)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("type with name '%s' not found", name)
		}
		return fmt.Errorf("failed to get type name: %w", err)
	}

	// Archive by name (primary key)
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.type_definition SET archived_at = NOW() WHERE LOWER(name) = LOWER($1) AND archived_at IS NULL",
		typeName)
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
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE LOWER(name) = LOWER($1))",
			typeName)

		if err != nil {
			return fmt.Errorf("failed to check if type exists: %w", err)
		}

		if exists {
			return fmt.Errorf("type '%s' is already archived", typeName)
		}

		return fmt.Errorf("type '%s' not found", typeName)
	}

	return nil
}

// Unarchive removes the archived status from a type definition
func (r *TypeRepositoryImpl) Unarchive(ctx context.Context, id string) error {
	// First get the type name from ID
	var typeName string
	err := r.repo.db.GetContext(ctx, &typeName,
		`SELECT name FROM flexitype.type_definition WHERE name = $1 ORDER BY version DESC LIMIT 1`,
		id)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("type with id '%s' not found", id)
		}
		return fmt.Errorf("failed to get type name: %w", err)
	}

	// Unarchive by name (primary key)
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.type_definition SET archived_at = NULL WHERE LOWER(name) = LOWER($1) AND archived_at IS NOT NULL",
		typeName)
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
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE LOWER(name) = LOWER($1))",
			typeName)

		if err != nil {
			return fmt.Errorf("failed to check if type exists: %w", err)
		}

		if exists {
			return fmt.Errorf("type '%s' is not archived", typeName)
		}

		return fmt.Errorf("type '%s' not found", typeName)
	}

	return nil
}

// ArchiveMany marks multiple type definitions as archived
func (r *TypeRepositoryImpl) ArchiveMany(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	// First get all the type names from the IDs
	query := "SELECT name FROM flexitype.type_definition WHERE name IN (?" + strings.Repeat(",?", len(names)-1) + ") ORDER BY version DESC"
	query = sqlx.Rebind(sqlx.DOLLAR, query) // Convert ? placeholders to $1, $2, etc.

	// Convert ids to interface{} slice for the query
	args := make([]interface{}, len(names))
	for i, name := range names {
		args[i] = name
	}

	var typeNames []string
	err := r.repo.db.SelectContext(ctx, &typeNames, query, args...)
	if err != nil {
		return fmt.Errorf("failed to get type names: %w", err)
	}

	if len(typeNames) == 0 {
		return fmt.Errorf("no types found for the given IDs")
	}

	// Create a query with multiple parameters - now using name as the key
	archiveQuery := "UPDATE flexitype.type_definition SET archived_at = NOW() WHERE name IN (?" + strings.Repeat(",?", len(typeNames)-1) + ") AND archived_at IS NULL"
	archiveQuery = sqlx.Rebind(sqlx.DOLLAR, archiveQuery) // Convert ? placeholders to $1, $2, etc.

	// Convert names to interface{} slice for the query
	nameArgs := make([]interface{}, len(typeNames))
	for i, name := range typeNames {
		nameArgs[i] = name
	}

	// Execute the update
	result, err := r.repo.db.ExecContext(ctx, archiveQuery, nameArgs...)
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

	// Check if all types were archived
	if int(rowsAffected) < len(typeNames) {
		return fmt.Errorf("not all types were archived (some may not exist or are already archived)")
	}

	return nil
}
