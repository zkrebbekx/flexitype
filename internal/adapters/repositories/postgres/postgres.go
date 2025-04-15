package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
	"github.com/zac300/flexitype/internal/ports"
)

// PostgresRepository implements the TypeRepository and InstanceRepository interfaces using PostgreSQL
type PostgresRepository struct {
	db *sqlx.DB
}

// NewPostgresRepository creates a new PostgreSQL repository
func NewPostgresRepository(connectionString string) (*PostgresRepository, error) {
	db, err := sqlx.Connect("postgres", connectionString)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to PostgreSQL: %w", err)
	}

	return &PostgresRepository{
		db: db,
	}, nil
}

// Close closes the database connection
func (r *PostgresRepository) Close() error {
	return r.db.Close()
}

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
func (r *TypeRepositoryImpl) Save(ctx context.Context, typeDef *core.TypeDefinition) error {
	// Begin transaction
	tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure we rollback on error
	txErr := err
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	// Get parent ID if present
	var parentID sql.NullString
	if typeDef.ParentType != nil {
		parentID = sql.NullString{
			String: typeDef.ParentType.ID,
			Valid:  true,
		}
	}

	// Check if the type already exists
	var exists bool
	err = tx.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE id = $1)",
		typeDef.ID)
	if err != nil {
		return fmt.Errorf("failed to check if type exists: %w", err)
	}

	if exists {
		// Update existing type
		_, err = tx.ExecContext(ctx,
			`UPDATE flexitype.type_definition 
             SET name = $1, description = $2, parent_type_id = $3, version = $4, updated_at = CURRENT_TIMESTAMP
             WHERE id = $5`,
			typeDef.Name, typeDef.Description, parentID, typeDef.Version, typeDef.ID)
		if err != nil {
			return fmt.Errorf("failed to update type definition: %w", err)
		}

		// Delete existing attributes and cascades for this type
		// First, delete cascades associated with attributes for this type
		_, err = tx.ExecContext(ctx,
			`DELETE FROM flexitype.attribute_cascade 
			 WHERE attribute_id IN (
				SELECT id FROM flexitype.attribute_definition WHERE type_id = $1
			 )`,
			typeDef.ID)
		if err != nil {
			return fmt.Errorf("failed to delete existing cascades: %w", err)
		}

		// Then delete attributes
		_, err = tx.ExecContext(ctx,
			"DELETE FROM flexitype.attribute_definition WHERE type_id = $1",
			typeDef.ID)
		if err != nil {
			return fmt.Errorf("failed to delete existing attributes: %w", err)
		}
	} else {
		// Insert new type
		_, err = tx.ExecContext(ctx,
			`INSERT INTO flexitype.type_definition (id, name, description, parent_type_id, version, created_at, updated_at)
             VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			typeDef.ID, typeDef.Name, typeDef.Description, parentID, typeDef.Version)
		if err != nil {
			return fmt.Errorf("failed to insert type definition: %w", err)
		}
	}

	// Insert attributes
	for _, attr := range typeDef.Attributes {
		// Save attribute
		_, err = tx.ExecContext(ctx,
			`INSERT INTO flexitype.attribute_definition (
                id, type_id, name, description, data_type, required, 
                default_value, multi_valued, disabled
             ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
			attr.ID, typeDef.ID, attr.Name, attr.Description, string(attr.DataType),
			attr.Required, formatDefaultValue(attr.DefaultValue), attr.MultiValued, attr.Disabled)
		if err != nil {
			return fmt.Errorf("failed to insert attribute: %w", err)
		}

		// Save cascades
		for _, cascade := range attr.Cascades {
			_, err = tx.ExecContext(ctx,
				`INSERT INTO flexitype.attribute_cascade (
					attribute_id, cascade_id, enabled, behavior, logic, weight
				) VALUES ($1, $2, $3, $4, $5, $6)`,
				attr.ID, cascade.ID, cascade.Enabled, string(cascade.Behavior), cascade.Logic, cascade.Weight)
			if err != nil {
				return fmt.Errorf("failed to insert cascade: %w", err)
			}
		}

		// Save validation rules
		for _, rule := range attr.ValidationRules {
			ruleType := fmt.Sprintf("%T", rule)
			ruleParams, err := json.Marshal(rule)
			if err != nil {
				return fmt.Errorf("failed to marshal validation rule: %w", err)
			}

			_, err = tx.ExecContext(ctx,
				`INSERT INTO flexitype.validation_rule (attribute_id, rule_type, parameters)
                 VALUES ($1, $2, $3)`,
				attr.ID, ruleType, string(ruleParams))
			if err != nil {
				return fmt.Errorf("failed to insert validation rule: %w", err)
			}
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

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
	ID          int64  `db:"id"`
	AttributeID string `db:"attribute_id"`
	CascadeID   string `db:"cascade_id"`
	Enabled     bool   `db:"enabled"`
	Behavior    string `db:"behavior"`
	Logic       string `db:"logic"`
	Weight      int    `db:"weight"`
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
	tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get type definition (including archived)
	var record typeRecord
	err = tx.GetContext(ctx, &record,
		`SELECT id, name, description, parent_type_id, version, created_at, updated_at, archived_at 
         FROM flexitype.type_definition 
         WHERE id = $1`,
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
	err = tx.SelectContext(ctx, &attributes,
		`SELECT id, name, description, data_type, required, default_value, 
		 multi_valued, disabled
		 FROM flexitype.attribute_definition 
		 WHERE type_id = $1`,
		id)
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
		err = tx.SelectContext(ctx, &cascades,
			`SELECT id, attribute_id, cascade_id, enabled, behavior, logic, weight
			 FROM flexitype.attribute_cascade
			 WHERE attribute_id = $1`,
			attrRecord.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to get cascades: %w", err)
		}

		// Process cascades
		for _, cascadeRecord := range cascades {
			attr.AddCascade(
				cascadeRecord.CascadeID,
				cascadeRecord.Enabled,
				core.CascadeBehavior(cascadeRecord.Behavior),
				cascadeRecord.Logic,
				cascadeRecord.Weight,
			)
		}

		// Get validation rules
		var rules []*validationRuleRecord
		err = tx.SelectContext(ctx, &rules,
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
         FROM flexitype.type_definition 
         WHERE name = $1`,
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
	query := "SELECT id FROM flexitype.type_definition"
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
		"UPDATE flexitype.type_definition SET archived_at = NOW() WHERE id = $1 AND archived_at IS NULL",
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
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE id = $1)",
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
		"UPDATE flexitype.type_definition SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL",
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
			"SELECT EXISTS(SELECT 1 FROM flexitype.type_definition WHERE id = $1)",
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
	query := "UPDATE flexitype.type_definition SET archived_at = NOW() WHERE id IN (?" + strings.Repeat(",?", len(ids)-1) + ") AND archived_at IS NULL"
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

	if int(rowsAffected) < len(ids) {
		return fmt.Errorf("not all types were archived (some may not exist or are already archived)")
	}

	return nil
}

// InstanceRepositoryImpl implements the InstanceRepository interface
type InstanceRepositoryImpl struct {
	repo     *PostgresRepository
	typeRepo *TypeRepositoryImpl
}

// NewInstanceRepository creates a new PostgreSQL instance repository
func NewInstanceRepository(repo *PostgresRepository, typeRepo *TypeRepositoryImpl) *InstanceRepositoryImpl {
	return &InstanceRepositoryImpl{
		repo:     repo,
		typeRepo: typeRepo,
	}
}

// SaveMany persists multiple instances in a single transaction
func (r *InstanceRepositoryImpl) SaveMany(ctx context.Context, instances []*core.Instance) error {
	if len(instances) == 0 {
		return nil
	}

	// Begin transaction
	tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure we rollback on error
	txErr := err
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	for _, instance := range instances {
		// Check if the instance already exists
		var exists bool
		err = tx.GetContext(ctx, &exists,
			"SELECT EXISTS(SELECT 1 FROM flexitype.instance WHERE id = $1)",
			instance.ID)
		if err != nil {
			return fmt.Errorf("failed to check if instance exists: %w", err)
		}

		if exists {
			// Update existing instance
			_, err = tx.ExecContext(ctx,
				`UPDATE flexitype.instance 
             SET type_id = $1, type_version = $2, updated_at = CURRENT_TIMESTAMP
             WHERE id = $3`,
				instance.TypeDefinition.ID, instance.TypeVersion, instance.ID)
			if err != nil {
				return fmt.Errorf("failed to update instance: %w", err)
			}

			// Delete existing attribute values
			_, err = tx.ExecContext(ctx,
				"DELETE FROM flexitype.attribute_value WHERE instance_id = $1",
				instance.ID)
			if err != nil {
				return fmt.Errorf("failed to delete existing attribute values: %w", err)
			}
		} else {
			// Insert new instance
			_, err = tx.ExecContext(ctx,
				`INSERT INTO flexitype.instance (id, type_id, type_version, created_at, updated_at)
             VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
				instance.ID, instance.TypeDefinition.ID, instance.TypeVersion)
			if err != nil {
				return fmt.Errorf("failed to insert instance: %w", err)
			}
		}

		// Insert attribute values
		for name, value := range instance.Attributes {
			// Handle multi-valued attributes
			if multiValues, isMulti := value.([]interface{}); isMulti {
				for idx, item := range multiValues {
					err = saveAttributeValue(ctx, tx, instance.ID, name, item, idx)
					if err != nil {
						return err
					}
				}
			} else {
				// Single-valued attribute
				err = saveAttributeValue(ctx, tx, instance.ID, name, value, nil)
				if err != nil {
					return err
				}
			}
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetByIDs retrieves multiple instances by IDs
func (r *InstanceRepositoryImpl) GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error) {
	if len(ids) == 0 {
		return make([]*core.Instance, 0), nil
	}

	// Fetch each instance
	instances := make([]*core.Instance, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		instance, err := r.GetByID(ctx, id)
		if err != nil {
			// Record not found IDs but continue with others
			if strings.Contains(err.Error(), "not found") {
				notFound = append(notFound, id)
				continue
			}
			return nil, fmt.Errorf("failed to get instance %s: %w", id, err)
		}
		instances = append(instances, instance)
	}

	if len(notFound) > 0 {
		return instances, fmt.Errorf("some instances not found: %v", notFound)
	}

	return instances, nil
}

// instanceRecord is the database representation of an instance
type instanceRecord struct {
	ID          string       `db:"id"`
	TypeID      string       `db:"type_id"`
	TypeVersion int          `db:"type_version"`
	CreatedAt   time.Time    `db:"created_at"`
	UpdatedAt   time.Time    `db:"updated_at"`
	ArchivedAt  sql.NullTime `db:"archived_at"`
}

// Save persists an instance
func (r *InstanceRepositoryImpl) Save(ctx context.Context, instance *core.Instance) error {
	// Begin transaction
	tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure we rollback on error
	txErr := err
	defer func() {
		if txErr != nil {
			tx.Rollback()
		}
	}()

	// Check if the instance already exists
	var exists bool
	err = tx.GetContext(ctx, &exists,
		"SELECT EXISTS(SELECT 1 FROM flexitype.instance WHERE id = $1)",
		instance.ID)
	if err != nil {
		return fmt.Errorf("failed to check if instance exists: %w", err)
	}

	if exists {
		// Update existing instance
		_, err = tx.ExecContext(ctx,
			`UPDATE flexitype.instance 
             SET type_id = $1, type_version = $2, updated_at = CURRENT_TIMESTAMP
             WHERE id = $3`,
			instance.TypeDefinition.ID, instance.TypeVersion, instance.ID)
		if err != nil {
			return fmt.Errorf("failed to update instance: %w", err)
		}

		// Delete existing attribute values
		_, err = tx.ExecContext(ctx,
			"DELETE FROM flexitype.attribute_value WHERE instance_id = $1",
			instance.ID)
		if err != nil {
			return fmt.Errorf("failed to delete existing attribute values: %w", err)
		}
	} else {
		// Insert new instance
		_, err = tx.ExecContext(ctx,
			`INSERT INTO flexitype.instance (id, type_id, type_version, created_at, updated_at)
             VALUES ($1, $2, $3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
			instance.ID, instance.TypeDefinition.ID, instance.TypeVersion)
		if err != nil {
			return fmt.Errorf("failed to insert instance: %w", err)
		}
	}

	// Insert attribute values
	for name, value := range instance.Attributes {
		// Handle multi-valued attributes
		if multiValues, isMulti := value.([]interface{}); isMulti {
			for idx, item := range multiValues {
				err = saveAttributeValue(ctx, tx, instance.ID, name, item, idx)
				if err != nil {
					return err
				}
			}
		} else {
			// Single-valued attribute
			err = saveAttributeValue(ctx, tx, instance.ID, name, value, nil)
			if err != nil {
				return err
			}
		}
	}

	// Commit transaction
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// saveAttributeValue saves a single attribute value
func saveAttributeValue(ctx context.Context, tx *sqlx.Tx, instanceID, attrName string, value interface{}, listIndex interface{}) error {
	// Determine value type and store in appropriate column
	var valueType, stringValue, dateValue string
	var intValue *int64
	var floatValue *float64
	var boolValue *bool

	switch v := value.(type) {
	case string:
		valueType = "string"
		stringValue = v
	case int, int8, int16, int32, int64:
		valueType = "int"
		i := reflect.ValueOf(v).Int()
		intValue = &i
	case float32, float64:
		valueType = "float"
		f := reflect.ValueOf(v).Float()
		floatValue = &f
	case bool:
		valueType = "boolean"
		boolValue = &v
	default:
		// For complex types, convert to JSON string
		valueType = "string"
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("failed to marshal complex attribute value: %w", err)
		}
		stringValue = string(jsonBytes)
	}

	// Convert list index to SQL value
	var sqlListIndex interface{}
	if listIndex != nil {
		sqlListIndex = listIndex
	}

	// Insert the attribute value
	_, err := tx.ExecContext(ctx,
		`INSERT INTO flexitype.attribute_value (
			instance_id, attribute_name, value_type, 
			string_value, int_value, float_value, boolean_value, date_value,
			list_index, is_default
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		instanceID, attrName, valueType,
		stringValue, intValue, floatValue, boolValue, dateValue,
		sqlListIndex, false)
	if err != nil {
		return fmt.Errorf("failed to insert attribute value: %w", err)
	}

	return nil
}

// GetByID retrieves an instance by ID
func (r *InstanceRepositoryImpl) GetByID(ctx context.Context, id string) (*core.Instance, error) {
	// Begin transaction
	tx, err := r.repo.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get instance record (including archived)
	var record instanceRecord
	err = tx.GetContext(ctx, &record,
		`SELECT id, type_id, type_version, created_at, updated_at, archived_at
         FROM flexitype.instance 
         WHERE id = $1`,
		id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("instance with id '%s' not found", id)
		}
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Get type definition
	typeDef, err := r.typeRepo.GetByID(ctx, record.TypeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}

	// Create instance
	instance := core.NewInstance(record.ID, typeDef)
	instance.TypeVersion = record.TypeVersion
	instance.CreatedAt = record.CreatedAt
	instance.UpdatedAt = record.UpdatedAt

	// Set archived status if present
	if record.ArchivedAt.Valid {
		archivedTime := record.ArchivedAt.Time
		instance.ArchivedAt = &archivedTime
	}

	// Get attribute values
	type attributeValueRecord struct {
		AttributeName string          `db:"attribute_name"`
		ValueType     string          `db:"value_type"`
		StringValue   sql.NullString  `db:"string_value"`
		IntValue      sql.NullInt64   `db:"int_value"`
		FloatValue    sql.NullFloat64 `db:"float_value"`
		BooleanValue  sql.NullBool    `db:"boolean_value"`
		DateValue     sql.NullTime    `db:"date_value"`
		ListIndex     sql.NullInt32   `db:"list_index"`
	}

	var attributeValues []attributeValueRecord
	err = tx.SelectContext(ctx, &attributeValues,
		`SELECT attribute_name, value_type, string_value, int_value, float_value, boolean_value, date_value, list_index
		 FROM flexitype.attribute_value
		 WHERE instance_id = $1
		 ORDER BY attribute_name, COALESCE(list_index, 0)`,
		id)
	if err != nil {
		return nil, fmt.Errorf("failed to get attribute values: %w", err)
	}

	// Group attribute values by name
	attributesByName := make(map[string][]interface{})

	for _, attrValue := range attributeValues {
		// Get the value based on type
		var value interface{}

		switch attrValue.ValueType {
		case "string":
			if attrValue.StringValue.Valid {
				value = attrValue.StringValue.String
			}
		case "int":
			if attrValue.IntValue.Valid {
				value = attrValue.IntValue.Int64
			}
		case "float":
			if attrValue.FloatValue.Valid {
				value = attrValue.FloatValue.Float64
			}
		case "boolean":
			if attrValue.BooleanValue.Valid {
				value = attrValue.BooleanValue.Bool
			}
		case "date":
			if attrValue.DateValue.Valid {
				value = attrValue.DateValue.Time
			}
		default:
			// For unknown types, use string value
			if attrValue.StringValue.Valid {
				value = attrValue.StringValue.String
			}
		}

		// If value is nil, skip
		if value == nil {
			continue
		}

		// Check if this is a multi-valued attribute
		attrDef := findAttributeDef(typeDef, attrValue.AttributeName)
		isMultiValued := attrDef != nil && attrDef.MultiValued

		if isMultiValued {
			// Add to the array of values
			if _, exists := attributesByName[attrValue.AttributeName]; !exists {
				attributesByName[attrValue.AttributeName] = make([]interface{}, 0)
			}
			attributesByName[attrValue.AttributeName] = append(attributesByName[attrValue.AttributeName], value)
		} else {
			// Single-valued attribute
			attributesByName[attrValue.AttributeName] = []interface{}{value}
		}
	}

	// Set attributes on the instance
	for name, values := range attributesByName {
		if len(values) == 1 && !isMultiValued(typeDef, name) {
			// Single-valued attribute
			err = instance.SetAttribute(name, values[0])
		} else {
			// Multi-valued attribute
			err = instance.SetAttribute(name, values)
		}

		if err != nil {
			return nil, fmt.Errorf("failed to set attribute '%s': %w", name, err)
		}
	}

	return instance, nil
}

// findAttributeDef finds an attribute definition by name
func findAttributeDef(typeDef *core.TypeDefinition, name string) *core.AttributeDefinition {
	for _, attr := range typeDef.GetAllAttributes() {
		if attr.Name == name {
			return attr
		}
	}
	return nil
}

// isMultiValued checks if an attribute is multi-valued
func isMultiValued(typeDef *core.TypeDefinition, name string) bool {
	attr := findAttributeDef(typeDef, name)
	return attr != nil && attr.MultiValued
}

// Query retrieves instances by query criteria (for backward compatibility)
func (r *InstanceRepositoryImpl) Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	// Create options from the legacy parameters
	options := ports.DefaultQueryOptions()
	options.TypeID = typeID
	options.AttributeFilters = attributeFilters

	// Use QueryWithOptions with the converted options
	instances, _, err := r.QueryWithOptions(ctx, options)
	return instances, err
}

// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
func (r *InstanceRepositoryImpl) QueryWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	if options == nil {
		options = ports.DefaultQueryOptions()
	}

	// Build the query to get instance IDs
	query := "SELECT i.id FROM flexitype.instance i"
	countQuery := "SELECT COUNT(DISTINCT i.id) FROM flexitype.instance i"

	// Track query conditions, joins, and parameters
	var joins []string
	var conditions []string
	var args []interface{}
	var argIndex int = 1

	// Filter by archived status
	if !options.IncludeArchived {
		conditions = append(conditions, "i.archived_at IS NULL")
	}

	// Add type ID filter if provided
	if options.TypeID != "" {
		conditions = append(conditions, fmt.Sprintf("i.type_id = $%d", argIndex))
		args = append(args, options.TypeID)
		argIndex++
	}

	// Filter by IDs if specified
	if len(options.IDs) > 0 {
		placeholders := make([]string, len(options.IDs))
		for i, id := range options.IDs {
			placeholders[i] = fmt.Sprintf("$%d", argIndex)
			args = append(args, id)
			argIndex++
		}
		conditions = append(conditions, fmt.Sprintf("i.id IN (%s)", strings.Join(placeholders, ", ")))
	}

	// Add attribute filters if provided
	if len(options.AttributeFilters) > 0 {
		joinCount := 0
		for attrName, attrValue := range options.AttributeFilters {
			// For each attribute filter, create a join with the attribute_value table
			joinAlias := fmt.Sprintf("av%d", joinCount)
			joins = append(joins, fmt.Sprintf(
				"JOIN flexitype.attribute_value %s ON i.id = %s.instance_id AND %s.attribute_name = $%d",
				joinAlias, joinAlias, joinAlias, argIndex))
			args = append(args, attrName)
			argIndex++

			// Add the value condition based on type
			switch v := attrValue.(type) {
			case string:
				conditions = append(conditions, fmt.Sprintf("%s.string_value = $%d", joinAlias, argIndex))
				args = append(args, v)
			case int, int8, int16, int32, int64:
				conditions = append(conditions, fmt.Sprintf("%s.int_value = $%d", joinAlias, argIndex))
				args = append(args, reflect.ValueOf(v).Int())
			case float32, float64:
				conditions = append(conditions, fmt.Sprintf("%s.float_value = $%d", joinAlias, argIndex))
				args = append(args, reflect.ValueOf(v).Float())
			case bool:
				conditions = append(conditions, fmt.Sprintf("%s.boolean_value = $%d", joinAlias, argIndex))
				args = append(args, v)
			default:
				// For complex values, convert to string and match
				jsonStr, err := json.Marshal(v)
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal filter value: %w", err)
				}
				conditions = append(conditions, fmt.Sprintf("%s.string_value = $%d", joinAlias, argIndex))
				args = append(args, string(jsonStr))
			}
			argIndex++
			joinCount++
		}
	}

	// Add joins to the queries
	for _, join := range joins {
		query += " " + join
		countQuery += " " + join
	}

	// Add WHERE clause if we have conditions
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
		countQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	// Add GROUP BY if we have multiple attribute filters
	if len(options.AttributeFilters) > 1 {
		// For multiple attribute filters, we need to ensure ALL filters match
		// by counting how many distinct attribute joins succeeded
		query += " GROUP BY i.id HAVING COUNT(*) = " + strconv.Itoa(len(options.AttributeFilters))
	}

	// Add ORDER BY if specified
	if options.OrderBy != "" {
		orderBy := sanitizeOrderByInstance(options.OrderBy)
		orderDir := "ASC"
		if strings.ToUpper(options.OrderDir) == "DESC" {
			orderDir = "DESC"
		}
		query += fmt.Sprintf(" ORDER BY i.%s %s", orderBy, orderDir)
	} else {
		query += " ORDER BY i.id ASC"
	}

	// Execute count query to get total records
	var totalCount int
	err := r.repo.db.GetContext(ctx, &totalCount, countQuery, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count: %w", err)
	}

	// Add pagination
	query += fmt.Sprintf(" LIMIT %d OFFSET %d", options.Limit, options.Offset)

	// Execute the query to get matching instance IDs
	var instanceIDs []string
	err = r.repo.db.SelectContext(ctx, &instanceIDs, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query instance IDs: %w", err)
	}

	// Fetch full instances for each ID
	instances := make([]*core.Instance, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		instance, err := r.GetByID(ctx, id)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get instance %s: %w", id, err)
		}
		instances = append(instances, instance)
	}

	return instances, totalCount, nil
}

// sanitizeOrderByInstance sanitizes order by column for instances to prevent SQL injection
func sanitizeOrderByInstance(orderBy string) string {
	// Allow only specific columns
	allowedColumns := map[string]bool{
		"id":           true,
		"type_id":      true,
		"type_version": true,
		"created_at":   true,
		"updated_at":   true,
		"archived_at":  true,
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

// Archive marks an instance as archived at the current time
func (r *InstanceRepositoryImpl) Archive(ctx context.Context, id string) error {
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.instance SET archived_at = NOW() WHERE id = $1 AND archived_at IS NULL",
		id)
	if err != nil {
		return fmt.Errorf("failed to archive instance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Check if the instance exists but is already archived
		var exists bool
		err = r.repo.db.GetContext(ctx, &exists,
			"SELECT EXISTS(SELECT 1 FROM flexitype.instance WHERE id = $1)",
			id)

		if err != nil {
			return fmt.Errorf("failed to check if instance exists: %w", err)
		}

		if exists {
			return fmt.Errorf("instance with id '%s' is already archived", id)
		}

		return fmt.Errorf("instance with id '%s' not found", id)
	}

	return nil
}

// Unarchive removes the archived status from an instance
func (r *InstanceRepositoryImpl) Unarchive(ctx context.Context, id string) error {
	result, err := r.repo.db.ExecContext(ctx,
		"UPDATE flexitype.instance SET archived_at = NULL WHERE id = $1 AND archived_at IS NOT NULL",
		id)
	if err != nil {
		return fmt.Errorf("failed to unarchive instance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		// Check if the instance exists but is not archived
		var exists bool
		err = r.repo.db.GetContext(ctx, &exists,
			"SELECT EXISTS(SELECT 1 FROM flexitype.instance WHERE id = $1)",
			id)

		if err != nil {
			return fmt.Errorf("failed to check if instance exists: %w", err)
		}

		if exists {
			return fmt.Errorf("instance with id '%s' is not archived", id)
		}

		return fmt.Errorf("instance with id '%s' not found", id)
	}

	return nil
}

// ArchiveMany marks multiple instances as archived
func (r *InstanceRepositoryImpl) ArchiveMany(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	// Create a query with multiple parameters
	query := "UPDATE flexitype.instance SET archived_at = NOW() WHERE id IN (?" + strings.Repeat(",?", len(ids)-1) + ") AND archived_at IS NULL"
	query = sqlx.Rebind(sqlx.DOLLAR, query) // Convert ? placeholders to $1, $2, etc.

	// Convert ids to interface{} slice for the query
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	// Execute the update
	result, err := r.repo.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to archive instances: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no instances were archived (they may not exist or are already archived)")
	}

	if int(rowsAffected) < len(ids) {
		return fmt.Errorf("not all instances were archived (some may not exist or are already archived)")
	}

	return nil
}

// Initialize runs database migrations to set up the schema
func (r *PostgresRepository) Initialize(ctx context.Context) error {
	// Set up Goose for migrations
	goose.SetBaseFS(nil) // Use the OS filesystem

	// Apply migrations
	if err := goose.Up(r.db.DB, "db/migrations"); err != nil {
		return fmt.Errorf("failed to apply migrations: %w", err)
	}

	return nil
}
