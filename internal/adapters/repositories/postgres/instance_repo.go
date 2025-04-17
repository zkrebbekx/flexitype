package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// InstanceRepositoryImpl implements the InstanceRepository interface using PostgreSQL
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

// Save persists an instance
func (r *InstanceRepositoryImpl) Save(ctx context.Context, instance *core.Instance) error {
	// Marshal the attributes to JSON
	attributesJSON, err := json.Marshal(instance.Attributes)
	if err != nil {
		return fmt.Errorf("failed to marshal attributes: %w", err)
	}

	query := `
		INSERT INTO flexitype.instance (
			id, version, type_id, type_version, attributes, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (id, version) DO UPDATE SET
			type_id = $3,
			type_version = $4,
			attributes = $5,
			updated_at = $7
	`

	_, err = r.repo.db.ExecContext(
		ctx,
		query,
		instance.ID,
		instance.Version,
		instance.TypeDefinition.ID,
		instance.TypeVersion,
		attributesJSON,
		instance.CreatedAt,
		instance.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert instance: %w", err)
	}

	return nil
}

// SaveMany persists multiple instances in a single transaction
func (r *InstanceRepositoryImpl) SaveMany(ctx context.Context, instances []*core.Instance) error {
	tx, err := r.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}

	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	for _, instance := range instances {
		// Marshal the attributes to JSON
		attributesJSON, err := json.Marshal(instance.Attributes)
		if err != nil {
			return fmt.Errorf("failed to marshal attributes: %w", err)
		}

		query := `
			INSERT INTO flexitype.instance (
				id, version, type_id, type_version, attributes, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7
			)
			ON CONFLICT (id, version) DO UPDATE SET
				type_id = $3,
				type_version = $4,
				attributes = $5,
				updated_at = $7
		`

		_, err = tx.ExecContext(
			ctx,
			query,
			instance.ID,
			instance.Version,
			instance.TypeDefinition.ID,
			instance.TypeVersion,
			attributesJSON,
			instance.CreatedAt,
			instance.UpdatedAt,
		)

		if err != nil {
			return fmt.Errorf("failed to insert instance: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetByID retrieves the latest instance by ID
func (r *InstanceRepositoryImpl) GetByID(ctx context.Context, id string) (*core.Instance, error) {
	query := `
		SELECT id, version, type_id, type_version, attributes, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1
		ORDER BY version DESC
		LIMIT 1
	`

	var typeID string
	var typeVersion int
	var attributesJSON []byte
	var createdAt, updatedAt time.Time
	var archivedAt *time.Time
	var version int

	err := r.repo.db.QueryRowContext(
		ctx,
		query,
		id,
	).Scan(&id, &version, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Get the type definition
	typeDef, err := r.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}

	// Unmarshal attributes
	attributes := make(map[string]interface{})
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
	}

	// Create the instance
	instance := &core.Instance{
		ID:             id,
		Version:        version,
		TypeDefinition: typeDef,
		TypeVersion:    typeVersion,
		Attributes:     attributes,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		ArchivedAt:     archivedAt,
	}

	return instance, nil
}

// GetByIDAndVersion retrieves a specific version of an instance
func (r *InstanceRepositoryImpl) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.Instance, error) {
	query := `
		SELECT id, version, type_id, type_version, attributes, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1 AND version = $2
	`

	var typeID string
	var typeVersion int
	var attributesJSON []byte
	var createdAt, updatedAt time.Time
	var archivedAt *time.Time
	var dbVersion int

	err := r.repo.db.QueryRowContext(
		ctx,
		query,
		id,
		version,
	).Scan(&id, &dbVersion, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Get the type definition
	typeDef, err := r.typeRepo.GetByID(ctx, typeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get type definition: %w", err)
	}

	// Unmarshal attributes
	attributes := make(map[string]interface{})
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
	}

	// Create the instance
	instance := &core.Instance{
		ID:             id,
		Version:        dbVersion,
		TypeDefinition: typeDef,
		TypeVersion:    typeVersion,
		Attributes:     attributes,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		ArchivedAt:     archivedAt,
	}

	return instance, nil
}

// GetLatestVersion returns the highest version number for an instance
func (r *InstanceRepositoryImpl) GetLatestVersion(ctx context.Context, id string) (int, error) {
	query := `
		SELECT COALESCE(MAX(version), 0)
		FROM flexitype.instance
		WHERE id = $1
	`

	var version int
	err := r.repo.db.QueryRowContext(ctx, query, id).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get latest version: %w", err)
	}

	return version, nil
}

// GetAllVersions retrieves all versions of an instance
func (r *InstanceRepositoryImpl) GetAllVersions(ctx context.Context, id string) ([]*core.Instance, error) {
	query := `
		SELECT id, version, type_id, type_version, attributes, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1
		ORDER BY version ASC
	`

	rows, err := r.repo.db.QueryContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	instances := make([]*core.Instance, 0)
	typeCache := make(map[string]*core.TypeDefinition)

	for rows.Next() {
		var instanceID string
		var version, typeVersion int
		var typeID string
		var attributesJSON []byte
		var createdAt, updatedAt time.Time
		var archivedAt *time.Time

		if err := rows.Scan(&instanceID, &version, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt); err != nil {
			return nil, fmt.Errorf("failed to scan instance: %w", err)
		}

		// Get the type definition from cache or fetch it
		typeDef, ok := typeCache[typeID]
		if !ok {
			var err error
			typeDef, err = r.typeRepo.GetByID(ctx, typeID)
			if err != nil {
				return nil, fmt.Errorf("failed to get type definition: %w", err)
			}
			typeCache[typeID] = typeDef
		}

		// Unmarshal attributes
		attributes := make(map[string]interface{})
		if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}

		// Create the instance
		instance := &core.Instance{
			ID:             instanceID,
			Version:        version,
			TypeDefinition: typeDef,
			TypeVersion:    typeVersion,
			Attributes:     attributes,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
			ArchivedAt:     archivedAt,
		}

		instances = append(instances, instance)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	return instances, nil
}

// GetByIDs retrieves multiple instances by IDs
func (r *InstanceRepositoryImpl) GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error) {
	if len(ids) == 0 {
		return []*core.Instance{}, nil
	}

	// Create a placeholder for each ID
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids))
	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}

	// Build the query to get the latest version of each instance
	query := fmt.Sprintf(`
		WITH latest_versions AS (
			SELECT id, MAX(version) as max_version
			FROM flexitype.instance
			WHERE id IN (%s)
			GROUP BY id
		)
		SELECT i.id, i.version, i.type_id, i.type_version, i.attributes, i.created_at, i.updated_at, i.archived_at
		FROM flexitype.instance i
		JOIN latest_versions lv ON i.id = lv.id AND i.version = lv.max_version
	`, strings.Join(placeholders, ","))

	rows, err := r.repo.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	instances := make([]*core.Instance, 0)
	typeCache := make(map[string]*core.TypeDefinition)
	foundIDs := make(map[string]bool)

	for rows.Next() {
		var instanceID string
		var version, typeVersion int
		var typeID string
		var attributesJSON []byte
		var createdAt, updatedAt time.Time
		var archivedAt *time.Time

		if err := rows.Scan(&instanceID, &version, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt); err != nil {
			return nil, fmt.Errorf("failed to scan instance: %w", err)
		}

		foundIDs[instanceID] = true

		// Get the type definition from cache or fetch it
		typeDef, ok := typeCache[typeID]
		if !ok {
			var err error
			typeDef, err = r.typeRepo.GetByID(ctx, typeID)
			if err != nil {
				return nil, fmt.Errorf("failed to get type definition: %w", err)
			}
			typeCache[typeID] = typeDef
		}

		// Unmarshal attributes
		attributes := make(map[string]interface{})
		if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
			return nil, fmt.Errorf("failed to unmarshal attributes: %w", err)
		}

		// Create the instance
		instance := &core.Instance{
			ID:             instanceID,
			Version:        version,
			TypeDefinition: typeDef,
			TypeVersion:    typeVersion,
			Attributes:     attributes,
			CreatedAt:      createdAt,
			UpdatedAt:      updatedAt,
			ArchivedAt:     archivedAt,
		}

		instances = append(instances, instance)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	// Check if all requested IDs were found
	if len(foundIDs) != len(ids) {
		missingIDs := make([]string, 0)
		for _, id := range ids {
			if !foundIDs[id] {
				missingIDs = append(missingIDs, id)
			}
		}
		return instances, fmt.Errorf("some instances not found: %v", missingIDs)
	}

	return instances, nil
}

// Query retrieves instances by type ID and attribute filters
func (r *InstanceRepositoryImpl) Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	options := &ports.QueryOptions{
		TypeID:            typeID,
		AttributeFilters:  attributeFilters,
		IncludeArchived:   false, // By default, exclude archived instances
		LatestVersionOnly: true,  // By default, only return latest versions
	}

	instances, _, err := r.QueryWithOptions(ctx, options)
	return instances, err
}

// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
func (r *InstanceRepositoryImpl) QueryWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	// Build the query
	//whereConditions := make([]string, 0)
	queryArgs := make([]interface{}, 0)
	argIdx := 1

	// Handle versioning
	if options.LatestVersionOnly {
		// When we want latest versions, we need to use a CTE or subquery
		innerQuery := `
			WITH latest_versions AS (
				SELECT id, MAX(version) as max_version
				FROM flexitype.instance
				WHERE 1=1
		`

		// Filter by type ID if specified
		if options.TypeID != "" {
			innerQuery += fmt.Sprintf(" AND type_id = $%d", argIdx)
			queryArgs = append(queryArgs, options.TypeID)
			argIdx++
		}

		// Filter by instance ID if specified
		if options.InstanceID != "" {
			innerQuery += fmt.Sprintf(" AND id = $%d", argIdx)
			queryArgs = append(queryArgs, options.InstanceID)
			argIdx++
		}

		// Include archived instances if specified
		if !options.IncludeArchived {
			innerQuery += " AND archived_at IS NULL"
		}

		innerQuery += `
				GROUP BY id
			)
			SELECT i.id, i.version, i.type_id, i.type_version, i.attributes, i.created_at, i.updated_at, i.archived_at
			FROM flexitype.instance i
			JOIN latest_versions lv ON i.id = lv.id AND i.version = lv.max_version
			WHERE 1=1
		`

		// Add specific version filtering if needed (unlikely with LatestVersionOnly, but included for completeness)
		if options.InstanceVersion > 0 {
			innerQuery += fmt.Sprintf(" AND i.version = $%d", argIdx)
			queryArgs = append(queryArgs, options.InstanceVersion)
			argIdx++
		}

		// Add attribute filters
		if len(options.AttributeFilters) > 0 {
			for attrName, attrValue := range options.AttributeFilters {
				// PostgreSQL JSONB containment operator @> checks if the JSON document contains the specified key-value pairs
				valueJSON, err := json.Marshal(map[string]interface{}{attrName: attrValue})
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal attribute filter: %w", err)
				}
				innerQuery += fmt.Sprintf(" AND i.attributes @> $%d", argIdx)
				queryArgs = append(queryArgs, valueJSON)
				argIdx++
			}
		}

		// Add ordering
		orderBy := "i.id"
		if options.OrderBy != "" {
			// Map order by field to column
			switch options.OrderBy {
			case "id":
				orderBy = "i.id"
			case "type_id":
				orderBy = "i.type_id"
			case "version":
				orderBy = "i.version"
			case "created_at":
				orderBy = "i.created_at"
			case "updated_at":
				orderBy = "i.updated_at"
			}
		}

		orderDir := "ASC"
		if options.OrderDir != "" && (options.OrderDir == "DESC" || options.OrderDir == "desc") {
			orderDir = "DESC"
		}

		innerQuery += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

		// Add pagination
		if options.Limit > 0 {
			innerQuery += fmt.Sprintf(" LIMIT $%d", argIdx)
			queryArgs = append(queryArgs, options.Limit)
			argIdx++

			if options.Offset > 0 {
				innerQuery += fmt.Sprintf(" OFFSET $%d", argIdx)
				queryArgs = append(queryArgs, options.Offset)
				argIdx++
			}
		}

		// Count total results
		countQuery := `
			SELECT COUNT(*)
			FROM flexitype.instance i
			JOIN (
				SELECT id, MAX(version) as max_version
				FROM flexitype.instance
				WHERE 1=1
		`

		countQueryArgs := make([]interface{}, 0)
		countArgIdx := 1

		// Add the same filtering conditions to the count query
		if options.TypeID != "" {
			countQuery += fmt.Sprintf(" AND type_id = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.TypeID)
			countArgIdx++
		}

		if options.InstanceID != "" {
			countQuery += fmt.Sprintf(" AND id = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.InstanceID)
			countArgIdx++
		}

		if !options.IncludeArchived {
			countQuery += " AND archived_at IS NULL"
		}

		countQuery += `
				GROUP BY id
			) lv ON i.id = lv.id AND i.version = lv.max_version
			WHERE 1=1
		`

		if options.InstanceVersion > 0 {
			countQuery += fmt.Sprintf(" AND i.version = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.InstanceVersion)
			countArgIdx++
		}

		if len(options.AttributeFilters) > 0 {
			for attrName, attrValue := range options.AttributeFilters {
				valueJSON, err := json.Marshal(map[string]interface{}{attrName: attrValue})
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal attribute filter: %w", err)
				}
				countQuery += fmt.Sprintf(" AND i.attributes @> $%d", countArgIdx)
				countQueryArgs = append(countQueryArgs, valueJSON)
				countArgIdx++
			}
		}

		// Execute count query
		var totalCount int
		err := r.repo.db.QueryRowContext(ctx, countQuery, countQueryArgs...).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count instances: %w", err)
		}

		// Execute main query
		rows, err := r.repo.db.QueryContext(ctx, innerQuery, queryArgs...)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query instances: %w", err)
		}
		defer rows.Close()

		// Process results
		instances := make([]*core.Instance, 0)
		typeCache := make(map[string]*core.TypeDefinition)

		for rows.Next() {
			var instanceID string
			var version, typeVersion int
			var typeID string
			var attributesJSON []byte
			var createdAt, updatedAt time.Time
			var archivedAt *time.Time

			if err := rows.Scan(&instanceID, &version, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt); err != nil {
				return nil, 0, fmt.Errorf("failed to scan instance: %w", err)
			}

			// Get the type definition from cache or fetch it
			typeDef, ok := typeCache[typeID]
			if !ok {
				var err error
				typeDef, err = r.typeRepo.GetByID(ctx, typeID)
				if err != nil {
					return nil, 0, fmt.Errorf("failed to get type definition: %w", err)
				}
				typeCache[typeID] = typeDef
			}

			// Unmarshal attributes
			attributes := make(map[string]interface{})
			if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}

			// Create the instance
			instance := &core.Instance{
				ID:             instanceID,
				Version:        version,
				TypeDefinition: typeDef,
				TypeVersion:    typeVersion,
				Attributes:     attributes,
				CreatedAt:      createdAt,
				UpdatedAt:      updatedAt,
				ArchivedAt:     archivedAt,
			}

			instances = append(instances, instance)
		}

		if err := rows.Err(); err != nil {
			return nil, 0, fmt.Errorf("error iterating over rows: %w", err)
		}

		return instances, totalCount, nil
	} else {
		// When we want all versions or specific versions, use a simpler query
		query := `
			SELECT id, version, type_id, type_version, attributes, created_at, updated_at, archived_at
			FROM flexitype.instance
			WHERE 1=1
		`

		// Filter by type ID if specified
		if options.TypeID != "" {
			query += fmt.Sprintf(" AND type_id = $%d", argIdx)
			queryArgs = append(queryArgs, options.TypeID)
			argIdx++
		}

		// Filter by instance ID if specified
		if options.InstanceID != "" {
			query += fmt.Sprintf(" AND id = $%d", argIdx)
			queryArgs = append(queryArgs, options.InstanceID)
			argIdx++
		}

		// Filter by version if specified
		if options.InstanceVersion > 0 {
			query += fmt.Sprintf(" AND version = $%d", argIdx)
			queryArgs = append(queryArgs, options.InstanceVersion)
			argIdx++
		}

		// Include archived instances if specified
		if !options.IncludeArchived {
			query += " AND archived_at IS NULL"
		}

		// Add attribute filters
		if len(options.AttributeFilters) > 0 {
			for attrName, attrValue := range options.AttributeFilters {
				// PostgreSQL JSONB containment operator @> checks if the JSON document contains the specified key-value pairs
				valueJSON, err := json.Marshal(map[string]interface{}{attrName: attrValue})
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal attribute filter: %w", err)
				}
				query += fmt.Sprintf(" AND attributes @> $%d", argIdx)
				queryArgs = append(queryArgs, valueJSON)
				argIdx++
			}
		}

		// Count total results
		countQuery := `
			SELECT COUNT(*)
			FROM flexitype.instance
			WHERE 1=1
		`

		countQueryArgs := make([]interface{}, 0)
		countArgIdx := 1

		// Add the same filtering conditions to the count query
		if options.TypeID != "" {
			countQuery += fmt.Sprintf(" AND type_id = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.TypeID)
			countArgIdx++
		}

		if options.InstanceID != "" {
			countQuery += fmt.Sprintf(" AND id = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.InstanceID)
			countArgIdx++
		}

		if options.InstanceVersion > 0 {
			countQuery += fmt.Sprintf(" AND version = $%d", countArgIdx)
			countQueryArgs = append(countQueryArgs, options.InstanceVersion)
			countArgIdx++
		}

		if !options.IncludeArchived {
			countQuery += " AND archived_at IS NULL"
		}

		if len(options.AttributeFilters) > 0 {
			for attrName, attrValue := range options.AttributeFilters {
				valueJSON, err := json.Marshal(map[string]interface{}{attrName: attrValue})
				if err != nil {
					return nil, 0, fmt.Errorf("failed to marshal attribute filter: %w", err)
				}
				countQuery += fmt.Sprintf(" AND attributes @> $%d", countArgIdx)
				countQueryArgs = append(countQueryArgs, valueJSON)
				countArgIdx++
			}
		}

		// Execute count query
		var totalCount int
		err := r.repo.db.QueryRowContext(ctx, countQuery, countQueryArgs...).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count instances: %w", err)
		}

		// Add ordering
		orderBy := "id"
		if options.OrderBy != "" {
			// Map order by field to column
			switch options.OrderBy {
			case "id":
				orderBy = "id"
			case "type_id":
				orderBy = "type_id"
			case "version":
				orderBy = "version"
			case "created_at":
				orderBy = "created_at"
			case "updated_at":
				orderBy = "updated_at"
			}
		}

		orderDir := "ASC"
		if options.OrderDir != "" && (options.OrderDir == "DESC" || options.OrderDir == "desc") {
			orderDir = "DESC"
		}

		query += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

		// Add pagination
		if options.Limit > 0 {
			query += fmt.Sprintf(" LIMIT $%d", argIdx)
			queryArgs = append(queryArgs, options.Limit)
			argIdx++

			if options.Offset > 0 {
				query += fmt.Sprintf(" OFFSET $%d", argIdx)
				queryArgs = append(queryArgs, options.Offset)
				argIdx++
			}
		}

		// Execute main query
		rows, err := r.repo.db.QueryContext(ctx, query, queryArgs...)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query instances: %w", err)
		}
		defer rows.Close()

		// Process results
		instances := make([]*core.Instance, 0)
		typeCache := make(map[string]*core.TypeDefinition)

		for rows.Next() {
			var instanceID string
			var version, typeVersion int
			var typeID string
			var attributesJSON []byte
			var createdAt, updatedAt time.Time
			var archivedAt *time.Time

			if err := rows.Scan(&instanceID, &version, &typeID, &typeVersion, &attributesJSON, &createdAt, &updatedAt, &archivedAt); err != nil {
				return nil, 0, fmt.Errorf("failed to scan instance: %w", err)
			}

			// Get the type definition from cache or fetch it
			typeDef, ok := typeCache[typeID]
			if !ok {
				var err error
				typeDef, err = r.typeRepo.GetByID(ctx, typeID)
				if err != nil {
					return nil, 0, fmt.Errorf("failed to get type definition: %w", err)
				}
				typeCache[typeID] = typeDef
			}

			// Unmarshal attributes
			attributes := make(map[string]interface{})
			if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
				return nil, 0, fmt.Errorf("failed to unmarshal attributes: %w", err)
			}

			// Create the instance
			instance := &core.Instance{
				ID:             instanceID,
				Version:        version,
				TypeDefinition: typeDef,
				TypeVersion:    typeVersion,
				Attributes:     attributes,
				CreatedAt:      createdAt,
				UpdatedAt:      updatedAt,
				ArchivedAt:     archivedAt,
			}

			instances = append(instances, instance)
		}

		if err := rows.Err(); err != nil {
			return nil, 0, fmt.Errorf("error iterating over rows: %w", err)
		}

		return instances, totalCount, nil
	}
}

// Archive marks an instance as archived at the current time
func (r *InstanceRepositoryImpl) Archive(ctx context.Context, id string) error {
	now := time.Now()

	query := `
		UPDATE flexitype.instance
		SET archived_at = $1, updated_at = $1
		WHERE id = $2
	`

	result, err := r.repo.db.ExecContext(ctx, query, now, id)
	if err != nil {
		return fmt.Errorf("failed to archive instance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	return nil
}

// Unarchive removes the archived status from an instance
func (r *InstanceRepositoryImpl) Unarchive(ctx context.Context, id string) error {
	now := time.Now()

	query := `
		UPDATE flexitype.instance
		SET archived_at = NULL, updated_at = $1
		WHERE id = $2
	`

	result, err := r.repo.db.ExecContext(ctx, query, now, id)
	if err != nil {
		return fmt.Errorf("failed to unarchive instance: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	return nil
}

// ArchiveMany marks multiple instances as archived
func (r *InstanceRepositoryImpl) ArchiveMany(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	now := time.Now()

	// Create a placeholder for each ID
	placeholders := make([]string, len(ids))
	args := make([]interface{}, len(ids)+1)
	args[0] = now

	for i, id := range ids {
		placeholders[i] = fmt.Sprintf("$%d", i+2)
		args[i+1] = id
	}

	query := fmt.Sprintf(`
		UPDATE flexitype.instance
		SET archived_at = $1, updated_at = $1
		WHERE id IN (%s)
	`, strings.Join(placeholders, ","))

	_, err := r.repo.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to archive instances: %w", err)
	}

	return nil
}
