package postgres

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
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
	// Insert or update the instance
	instanceQuery := `
		INSERT INTO flexitype.instance (
			id, version, type_id, type_version, created_at, updated_at, archived_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7
		)
		ON CONFLICT (id, version) DO UPDATE SET
			type_id = $3,
			type_version = $4,
			updated_at = $6,
			archived_at = $7
	`

	_, err := r.repo.db.ExecContext(
		ctx,
		instanceQuery,
		instance.ID,
		instance.Version,
		instance.TypeDefinition.ID,
		instance.TypeVersion,
		instance.CreatedAt,
		instance.UpdatedAt,
		instance.ArchivedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to insert instance: %w", err)
	}

	// Delete existing attribute values for this instance and version before inserting new ones
	deleteQuery := `
		DELETE FROM flexitype.attribute_value
		WHERE instance_id = $1 AND instance_version = $2
	`
	_, err = r.repo.db.ExecContext(ctx, deleteQuery, instance.ID, instance.Version)
	if err != nil {
		return fmt.Errorf("failed to delete existing attribute values: %w", err)
	}

	// Insert attribute values with the version
	for attrName, attrValue := range instance.Attributes {
		if err := r.saveAttributeValue(ctx, nil, instance.ID, instance.Version, attrName, attrValue, false, 0); err != nil {
			return fmt.Errorf("failed to save attribute value: %w", err)
		}
	}

	return nil
}

// saveAttributeValue saves a single attribute value
func (r *InstanceRepositoryImpl) saveAttributeValue(ctx context.Context, tx interface{}, instanceID string, instanceVersion int, attrName string, value interface{}, isDefault bool, listIndex int) error {
	// Skip nil values
	if value == nil {
		return nil
	}

	// Handle different types of values
	switch v := value.(type) {
	case string:
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "string", v, nil, nil, nil, nil, isDefault, listIndex)
		return err
	case int, int8, int16, int32, int64:
		intVal := reflect.ValueOf(v).Int()
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "int", nil, intVal, nil, nil, nil, isDefault, listIndex)
		return err
	case float32, float64:
		floatVal := reflect.ValueOf(v).Float()
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "float", nil, nil, floatVal, nil, nil, isDefault, listIndex)
		return err
	case bool:
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "boolean", nil, nil, nil, v, nil, isDefault, listIndex)
		return err
	case time.Time:
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "date", nil, nil, nil, nil, v, isDefault, listIndex)
		return err
	case []interface{}:
		// Handle arrays
		for i, item := range v {
			if err := r.saveAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, item, isDefault, i); err != nil {
				return err
			}
		}
		return nil
	case map[string]interface{}:
		// Handle objects by creating an attribute value and then object_value entries
		attrValueID, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "object", nil, nil, nil, nil, nil, isDefault, listIndex)
		if err != nil {
			return err
		}
		
		// Save each property in the object
		for propName, propValue := range v {
			if err := r.saveObjectProperty(ctx, tx, attrValueID, propName, propValue, 0); err != nil {
				return err
			}
		}
		return nil
	default:
		// Convert unknown types to string
		stringVal := fmt.Sprintf("%v", v)
		_, err := r.insertAttributeValue(ctx, tx, instanceID, instanceVersion, attrName, "string", stringVal, nil, nil, nil, nil, isDefault, listIndex)
		return err
	}
}

// saveObjectProperty saves a single object property
func (r *InstanceRepositoryImpl) saveObjectProperty(ctx context.Context, tx interface{}, attributeValueID int64, propName string, propValue interface{}, listIndex int) error {
	// Skip nil values
	if propValue == nil {
		return nil
	}

	// Handle different types of property values
	switch v := propValue.(type) {
	case string:
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "string", v, nil, nil, nil, nil, listIndex)
		return err
	case int, int8, int16, int32, int64:
		intVal := reflect.ValueOf(v).Int()
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "int", nil, intVal, nil, nil, nil, listIndex)
		return err
	case float32, float64:
		floatVal := reflect.ValueOf(v).Float()
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "float", nil, nil, floatVal, nil, nil, listIndex)
		return err
	case bool:
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "boolean", nil, nil, nil, v, nil, listIndex)
		return err
	case time.Time:
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "date", nil, nil, nil, nil, v, listIndex)
		return err
	case []interface{}:
		// Handle arrays
		for i, item := range v {
			if err := r.saveObjectProperty(ctx, tx, attributeValueID, propName, item, i); err != nil {
				return err
			}
		}
		return nil
	case map[string]interface{}:
		// Handle nested objects
		nestedObjID, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "nested_object", nil, nil, nil, nil, nil, listIndex)
		if err != nil {
			return err
		}
		
		// Save each property in the nested object
		for nestedPropName, nestedPropValue := range v {
			if err := r.saveObjectProperty(ctx, tx, nestedObjID, nestedPropName, nestedPropValue, 0); err != nil {
				return err
			}
		}
		return nil
	default:
		// Convert unknown types to string
		stringVal := fmt.Sprintf("%v", v)
		_, err := r.insertObjectValue(ctx, tx, attributeValueID, propName, "string", stringVal, nil, nil, nil, nil, listIndex)
		return err
	}
}

// insertAttributeValue inserts a single attribute value record and returns its ID
func (r *InstanceRepositoryImpl) insertAttributeValue(
	ctx context.Context, 
	tx interface{}, 
	instanceID string,
	instanceVersion int,
	attrName,
	valueType string, 
	stringValue interface{}, 
	intValue interface{}, 
	floatValue interface{}, 
	boolValue interface{}, 
	dateValue interface{}, 
	isDefault bool, 
	listIndex int,
) (int64, error) {
	query := `
		INSERT INTO flexitype.attribute_value (
			instance_id, instance_version, attribute_name, value_type, string_value, int_value, float_value, boolean_value, date_value, is_default, list_index
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		RETURNING id
	`

	var id int64
	var err error

	// Use the database connection directly
	err = r.repo.db.QueryRowContext(ctx, query, instanceID, instanceVersion, attrName, valueType, stringValue, intValue, floatValue, boolValue, dateValue, isDefault, listIndex).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("failed to insert attribute value: %w", err)
	}

	return id, nil
}

// insertObjectValue inserts a single object value record and returns its ID if needed for nesting
func (r *InstanceRepositoryImpl) insertObjectValue(
	ctx context.Context, 
	tx interface{}, 
	attributeValueID int64, 
	propName,
	valueType string, 
	stringValue interface{}, 
	intValue interface{}, 
	floatValue interface{}, 
	boolValue interface{}, 
	dateValue interface{}, 
	listIndex int,
) (int64, error) {
	query := `
		INSERT INTO flexitype.object_value (
			attribute_value_id, property_name, value_type, string_value, int_value, float_value, boolean_value, date_value, list_index
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
		RETURNING id
	`

	var id int64
	var err error

	// Use the database connection directly
	err = r.repo.db.QueryRowContext(ctx, query, attributeValueID, propName, valueType, stringValue, intValue, floatValue, boolValue, dateValue, listIndex).Scan(&id)

	if err != nil {
		return 0, fmt.Errorf("failed to insert object value: %w", err)
	}

	return id, nil
}

// SaveMany persists multiple instances in a single transaction
func (r *InstanceRepositoryImpl) SaveMany(ctx context.Context, instances []*core.Instance) error {
	for _, instance := range instances {
		// Insert or update the instance
		instanceQuery := `
			INSERT INTO flexitype.instance (
				id, version, type_id, type_version, created_at, updated_at, archived_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7
			)
			ON CONFLICT (id, version) DO UPDATE SET
				type_id = $3,
				type_version = $4,
				updated_at = $6,
				archived_at = $7
		`

		_, err := r.repo.db.ExecContext(
			ctx,
			instanceQuery,
			instance.ID,
			instance.Version,
			instance.TypeDefinition.ID,
			instance.TypeVersion,
			instance.CreatedAt,
			instance.UpdatedAt,
			instance.ArchivedAt,
		)

		if err != nil {
			return fmt.Errorf("failed to insert instance: %w", err)
		}

		// Delete existing attribute values for this instance and version
		deleteQuery := `
			DELETE FROM flexitype.attribute_value
			WHERE instance_id = $1 AND instance_version = $2
		`
		_, err = r.repo.db.ExecContext(ctx, deleteQuery, instance.ID, instance.Version)
		if err != nil {
			return fmt.Errorf("failed to delete existing attribute values: %w", err)
		}

		// Insert attribute values
		for attrName, attrValue := range instance.Attributes {
			if err := r.saveAttributeValue(ctx, nil, instance.ID, instance.Version, attrName, attrValue, false, 0); err != nil {
				return fmt.Errorf("failed to save attribute value: %w", err)
			}
		}
	}

	return nil
}

// GetByID retrieves the latest instance by ID
func (r *InstanceRepositoryImpl) GetByID(ctx context.Context, id string) (*core.Instance, error) {
	query := `
		SELECT id, version, type_id, type_version, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1
		ORDER BY version DESC
		LIMIT 1
	`

	var instance struct {
		ID          string     `db:"id"`
		Version     int        `db:"version"`
		TypeID      string     `db:"type_id"`
		TypeVersion int        `db:"type_version"`
		CreatedAt   time.Time  `db:"created_at"`
		UpdatedAt   time.Time  `db:"updated_at"`
		ArchivedAt  *time.Time `db:"archived_at"`
	}

	err := r.repo.db.QueryRowxContext(ctx, query, id).StructScan(&instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Get the type definition - with specific version
	typeDef, err := r.typeRepo.GetByIDAndVersion(ctx, instance.TypeID, instance.TypeVersion)
	if err != nil {
		// Fallback to latest type version if specific version not found
		typeDef, err = r.typeRepo.GetByID(ctx, instance.TypeID)
		if err != nil {
			return nil, fmt.Errorf("failed to get type definition: %w", err)
		}
	}

	// Get attribute values for this instance
	attributes, err := r.getAttributeValues(ctx, instance.ID, instance.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get attribute values: %w", err)
	}

	// Create the instance
	result := &core.Instance{
		ID:             instance.ID,
		Version:        instance.Version,
		TypeDefinition: typeDef,
		TypeVersion:    instance.TypeVersion,
		Attributes:     attributes,
		CreatedAt:      instance.CreatedAt,
		UpdatedAt:      instance.UpdatedAt,
		ArchivedAt:     instance.ArchivedAt,
	}

	return result, nil
}

// getAttributeValues retrieves all attribute values for an instance version
func (r *InstanceRepositoryImpl) getAttributeValues(ctx context.Context, instanceID string, instanceVersion int) (map[string]interface{}, error) {
	query := `
		SELECT id, attribute_name, value_type, string_value, int_value, float_value, boolean_value, date_value, list_index
		FROM flexitype.attribute_value
		WHERE instance_id = $1 AND instance_version = $2
		ORDER BY attribute_name, list_index
	`

	rows, err := r.repo.db.QueryContext(ctx, query, instanceID, instanceVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to query attribute values: %w", err)
	}
	defer rows.Close()

	attributes := make(map[string]interface{})
	attributeArrays := make(map[string][]interface{})

	for rows.Next() {
		var id int64
		var attrName, valueType string
		var stringValue *string
		var intValue *int64
		var floatValue *float64
		var boolValue *bool
		var dateValue *time.Time
		var listIndex int

		if err := rows.Scan(&id, &attrName, &valueType, &stringValue, &intValue, &floatValue, &boolValue, &dateValue, &listIndex); err != nil {
			return nil, fmt.Errorf("failed to scan attribute value: %w", err)
		}

		// Extract the actual value based on type
		var value interface{}
		switch valueType {
		case "string":
			if stringValue != nil {
				value = *stringValue
			}
		case "int":
			if intValue != nil {
				value = *intValue
			}
		case "float":
			if floatValue != nil {
				value = *floatValue
			}
		case "boolean":
			if boolValue != nil {
				value = *boolValue
			}
		case "date":
			if dateValue != nil {
				value = *dateValue
			}
		case "object":
			// For objects, get all object values
			objValues, err := r.getObjectValues(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("failed to get object values: %w", err)
			}
			value = objValues
		}

		// If list index is > 0, it's part of an array
		if listIndex > 0 {
			// Initialize the array if needed
			if _, ok := attributeArrays[attrName]; !ok {
				attributeArrays[attrName] = make([]interface{}, 0)
			}

			// Extend the array if needed
			arr := attributeArrays[attrName]
			for len(arr) <= listIndex {
				arr = append(arr, nil)
			}
			arr[listIndex] = value
			attributeArrays[attrName] = arr
		} else if listIndex == 0 {
			// Check if this attribute is already registered as an array
			if arr, ok := attributeArrays[attrName]; ok {
				arr[0] = value
				attributeArrays[attrName] = arr
			} else {
				// Otherwise, set it as a regular value
				attributes[attrName] = value
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	// Merge arrays into attributes
	for attrName, arr := range attributeArrays {
		attributes[attrName] = arr
	}

	return attributes, nil
}

// getObjectValues retrieves all values for an object attribute
func (r *InstanceRepositoryImpl) getObjectValues(ctx context.Context, attributeValueID int64) (map[string]interface{}, error) {
	query := `
		SELECT id, property_name, value_type, string_value, int_value, float_value, boolean_value, date_value, list_index
		FROM flexitype.object_value
		WHERE attribute_value_id = $1
		ORDER BY property_name, list_index
	`

	rows, err := r.repo.db.QueryContext(ctx, query, attributeValueID)
	if err != nil {
		return nil, fmt.Errorf("failed to query object values: %w", err)
	}
	defer rows.Close()

	properties := make(map[string]interface{})
	propertyArrays := make(map[string][]interface{})

	for rows.Next() {
		var id int64
		var propName, valueType string
		var stringValue *string
		var intValue *int64
		var floatValue *float64
		var boolValue *bool
		var dateValue *time.Time
		var listIndex int

		if err := rows.Scan(&id, &propName, &valueType, &stringValue, &intValue, &floatValue, &boolValue, &dateValue, &listIndex); err != nil {
			return nil, fmt.Errorf("failed to scan object value: %w", err)
		}

		// Extract the actual value based on type
		var value interface{}
		switch valueType {
		case "string":
			if stringValue != nil {
				value = *stringValue
			}
		case "int":
			if intValue != nil {
				value = *intValue
			}
		case "float":
			if floatValue != nil {
				value = *floatValue
			}
		case "boolean":
			if boolValue != nil {
				value = *boolValue
			}
		case "date":
			if dateValue != nil {
				value = *dateValue
			}
		case "nested_object":
			{
				// Recursively get nested object values using this ID
				nestedValues, err := r.getObjectValues(ctx, id)
				if err != nil {
					return nil, fmt.Errorf("failed to get nested object values: %w", err)
				}
				value = nestedValues
			}
		}

		// If list index is > 0, it's part of an array
		if listIndex > 0 {
			// Initialize the array if needed
			if _, ok := propertyArrays[propName]; !ok {
				propertyArrays[propName] = make([]interface{}, 0)
			}

			// Extend the array if needed
			arr := propertyArrays[propName]
			for len(arr) <= listIndex {
				arr = append(arr, nil)
			}
			arr[listIndex] = value
			propertyArrays[propName] = arr
		} else if listIndex == 0 {
			// Check if this property is already registered as an array
			if arr, ok := propertyArrays[propName]; ok {
				arr[0] = value
				propertyArrays[propName] = arr
			} else {
				// Otherwise, set it as a regular value
				properties[propName] = value
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	// Merge arrays into properties
	for propName, arr := range propertyArrays {
		properties[propName] = arr
	}

	return properties, nil
}

// GetByIDAndVersion retrieves a specific version of an instance
func (r *InstanceRepositoryImpl) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.Instance, error) {
	query := `
		SELECT id, version, type_id, type_version, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1 AND version = $2
	`

	var instance struct {
		ID          string     `db:"id"`
		Version     int        `db:"version"`
		TypeID      string     `db:"type_id"`
		TypeVersion int        `db:"type_version"`
		CreatedAt   time.Time  `db:"created_at"`
		UpdatedAt   time.Time  `db:"updated_at"`
		ArchivedAt  *time.Time `db:"archived_at"`
	}

	err := r.repo.db.QueryRowxContext(ctx, query, id, version).StructScan(&instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// Get the type definition with specific version
	typeDef, err := r.typeRepo.GetByIDAndVersion(ctx, instance.TypeID, instance.TypeVersion)
	if err != nil {
		// Fallback to latest type version if specific version not found
		typeDef, err = r.typeRepo.GetByID(ctx, instance.TypeID)
		if err != nil {
			return nil, fmt.Errorf("failed to get type definition: %w", err)
		}
	}

	// Get attribute values for this instance version
	attributes, err := r.getAttributeValues(ctx, instance.ID, instance.Version)
	if err != nil {
		return nil, fmt.Errorf("failed to get attribute values: %w", err)
	}

	// Create the instance
	result := &core.Instance{
		ID:             instance.ID,
		Version:        instance.Version,
		TypeDefinition: typeDef,
		TypeVersion:    instance.TypeVersion,
		Attributes:     attributes,
		CreatedAt:      instance.CreatedAt,
		UpdatedAt:      instance.UpdatedAt,
		ArchivedAt:     instance.ArchivedAt,
	}

	return result, nil
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
		SELECT id, version, type_id, type_version, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE id = $1
		ORDER BY version ASC
	`

	rows, err := r.repo.db.QueryxContext(ctx, query, id)
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	instances, _, err := r.scanInstances(ctx, rows, 0)
	if err != nil {
		return nil, err
	}

	if len(instances) == 0 {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	return instances, nil
}

// scanInstances scans rows into Instance objects
func (r *InstanceRepositoryImpl) scanInstances(ctx context.Context, rows *sqlx.Rows, totalCount int) ([]*core.Instance, int, error) {
	instances := make([]*core.Instance, 0)
	typeCache := make(map[string]*core.TypeDefinition)
	typeVersionCache := make(map[string]map[int]*core.TypeDefinition)

	for rows.Next() {
		// Use StructScan for sqlx.Rows
		var instance struct {
			ID          string     `db:"id"`
			Version     int        `db:"version"`
			TypeID      string     `db:"type_id"`
			TypeVersion int        `db:"type_version"`
			CreatedAt   time.Time  `db:"created_at"`
			UpdatedAt   time.Time  `db:"updated_at"`
			ArchivedAt  *time.Time `db:"archived_at"`
		}

		if err := rows.StructScan(&instance); err != nil {
			return nil, 0, fmt.Errorf("failed to scan instance: %w", err)
		}

		// First try to get the specific type version from cache
		var typeDef *core.TypeDefinition
		var ok bool
		
		// Check if we have versions of this type in cache
		if versionMap, hasType := typeVersionCache[instance.TypeID]; hasType {
			// Check if we have this specific version
			if typeDef, ok = versionMap[instance.TypeVersion]; !ok {
				// No specific version in cache, fetch it
				var err error
				typeDef, err = r.typeRepo.GetByIDAndVersion(ctx, instance.TypeID, instance.TypeVersion)
				if err != nil {
					// Fallback to latest version if not found
					typeDef, ok = typeCache[instance.TypeID]
					if !ok {
						var err error
						typeDef, err = r.typeRepo.GetByID(ctx, instance.TypeID)
						if err != nil {
							return nil, 0, fmt.Errorf("failed to get type definition: %w", err)
						}
						typeCache[instance.TypeID] = typeDef
					}
				} else {
					// Add to version cache
					versionMap[instance.TypeVersion] = typeDef
				}
			}
		} else {
			// No versions of this type in cache
			var err error
			typeDef, err = r.typeRepo.GetByIDAndVersion(ctx, instance.TypeID, instance.TypeVersion)
			if err != nil {
				// Fallback to latest version
				typeDef, ok = typeCache[instance.TypeID]
				if !ok {
					var err error
					typeDef, err = r.typeRepo.GetByID(ctx, instance.TypeID)
					if err != nil {
						return nil, 0, fmt.Errorf("failed to get type definition: %w", err)
					}
					typeCache[instance.TypeID] = typeDef
				}
			} else {
				// Initialize version map and cache the type version
				typeVersionCache[instance.TypeID] = map[int]*core.TypeDefinition{
					instance.TypeVersion: typeDef,
				}
			}
		}

		// Get attribute values for this instance
		attributes, err := r.getAttributeValues(ctx, instance.ID, instance.Version)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get attribute values: %w", err)
		}

		// Create the instance
		coreInstance := &core.Instance{
			ID:             instance.ID,
			Version:        instance.Version,
			TypeDefinition: typeDef,
			TypeVersion:    instance.TypeVersion,
			Attributes:     attributes,
			CreatedAt:      instance.CreatedAt,
			UpdatedAt:      instance.UpdatedAt,
			ArchivedAt:     instance.ArchivedAt,
		}

		instances = append(instances, coreInstance)
	}

	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating over rows: %w", err)
	}

	return instances, totalCount, nil
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

// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
func (r *InstanceRepositoryImpl) QueryWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	// Build the query
	whereConditions := make([]string, 0)
	queryArgs := make([]interface{}, 0)
	argIdx := 1

	baseQuery := `
		SELECT id, version, type_id, type_version, created_at, updated_at, archived_at
		FROM flexitype.instance
		WHERE 1=1
	`

	// Filter by type ID if specified
	if options.TypeID != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("type_id = $%d", argIdx))
		queryArgs = append(queryArgs, options.TypeID)
		argIdx++
	}

	// Filter by instance ID if specified
	if options.InstanceID != "" {
		whereConditions = append(whereConditions, fmt.Sprintf("id = $%d", argIdx))
		queryArgs = append(queryArgs, options.InstanceID)
		argIdx++
	}

	// Filter by version if specified
	if options.InstanceVersion > 0 {
		whereConditions = append(whereConditions, fmt.Sprintf("version = $%d", argIdx))
		queryArgs = append(queryArgs, options.InstanceVersion)
		argIdx++
	}

	// Include archived instances if specified
	if !options.IncludeArchived {
		whereConditions = append(whereConditions, "archived_at IS NULL")
	}

	// Latest version only logic
	if options.LatestVersionOnly {
		if len(whereConditions) > 0 {
			baseQuery += " AND " + strings.Join(whereConditions, " AND ")
		}

		// Use a window function to get the latest version
		query := fmt.Sprintf(`
			WITH ranked AS (
				SELECT
					id, version, type_id, type_version, created_at, updated_at, archived_at,
					ROW_NUMBER() OVER (PARTITION BY id ORDER BY version DESC) as rn
				FROM flexitype.instance
				WHERE %s
			)
			SELECT id, version, type_id, type_version, created_at, updated_at, archived_at
			FROM ranked
			WHERE rn = 1
		`, strings.Join(whereConditions, " AND "))

		// Count total instances
		countQuery := fmt.Sprintf(`
			SELECT COUNT(DISTINCT id)
			FROM flexitype.instance
			WHERE %s
		`, strings.Join(whereConditions, " AND "))

		var totalCount int
		err := r.repo.db.QueryRowContext(ctx, countQuery, queryArgs...).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count instances: %w", err)
		}

		// Add attribute filter logic for the attribute_value table
		if len(options.AttributeFilters) > 0 {
			attributeFilterPart := `
				AND id IN (
					SELECT DISTINCT instance_id
					FROM flexitype.attribute_value
					WHERE 
			`
			attrConditions := make([]string, 0)
			for attrName, attrValue := range options.AttributeFilters {
				switch v := attrValue.(type) {
				case string:
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND string_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, v)
					argIdx += 2
				case int, int8, int16, int32, int64:
					intVal := reflect.ValueOf(v).Int()
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND int_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, intVal)
					argIdx += 2
				case float32, float64:
					floatVal := reflect.ValueOf(v).Float()
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND float_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, floatVal)
					argIdx += 2
				case bool:
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND boolean_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, v)
					argIdx += 2
				default:
					// Convert to string for other types
					stringVal := fmt.Sprintf("%v", v)
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND string_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, stringVal)
					argIdx += 2
				}
			}
			
			if len(attrConditions) > 0 {
				attributeFilterPart += strings.Join(attrConditions, " OR ") + ")"
				query += attributeFilterPart
			}
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

		// Execute the query
		rows, err := r.repo.db.QueryxContext(ctx, query, queryArgs...)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query instances: %w", err)
		}
		defer rows.Close()

		return r.scanInstances(ctx, rows, totalCount)
	} else {
		// When not specifically looking for latest versions
		if len(whereConditions) > 0 {
			baseQuery += " AND " + strings.Join(whereConditions, " AND ")
		}

		// Count total instances
		countQuery := baseQuery
		countQuery = strings.Replace(countQuery, "SELECT id, version, type_id, type_version, created_at, updated_at, archived_at", "SELECT COUNT(*)", 1)

		var totalCount int
		err := r.repo.db.QueryRowContext(ctx, countQuery, queryArgs...).Scan(&totalCount)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count instances: %w", err)
		}

		// Add attribute filter logic for the attribute_value table
		if len(options.AttributeFilters) > 0 {
			attributeFilterPart := `
				AND id IN (
					SELECT DISTINCT instance_id
					FROM flexitype.attribute_value
					WHERE 
			`
			attrConditions := make([]string, 0)
			for attrName, attrValue := range options.AttributeFilters {
				switch v := attrValue.(type) {
				case string:
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND string_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, v)
					argIdx += 2
				case int, int8, int16, int32, int64:
					intVal := reflect.ValueOf(v).Int()
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND int_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, intVal)
					argIdx += 2
				case float32, float64:
					floatVal := reflect.ValueOf(v).Float()
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND float_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, floatVal)
					argIdx += 2
				case bool:
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND boolean_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, v)
					argIdx += 2
				default:
					// Convert to string for other types
					stringVal := fmt.Sprintf("%v", v)
					attrConditions = append(attrConditions, fmt.Sprintf("(attribute_name = $%d AND string_value = $%d)", argIdx, argIdx+1))
					queryArgs = append(queryArgs, attrName, stringVal)
					argIdx += 2
				}
			}
			
			if len(attrConditions) > 0 {
				attributeFilterPart += strings.Join(attrConditions, " OR ") + ")"
				baseQuery += attributeFilterPart
			}
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

		baseQuery += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

		// Add pagination
		if options.Limit > 0 {
			baseQuery += fmt.Sprintf(" LIMIT $%d", argIdx)
			queryArgs = append(queryArgs, options.Limit)
			argIdx++

			if options.Offset > 0 {
				baseQuery += fmt.Sprintf(" OFFSET $%d", argIdx)
				queryArgs = append(queryArgs, options.Offset)
				argIdx++
			}
		}

		// Execute the query
		rows, err := r.repo.db.QueryxContext(ctx, baseQuery, queryArgs...)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to query instances: %w", err)
		}
		defer rows.Close()

		return r.scanInstances(ctx, rows, totalCount)
	}
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
		SELECT i.id, i.version, i.type_id, i.type_version, i.created_at, i.updated_at, i.archived_at
		FROM flexitype.instance i
		JOIN latest_versions lv ON i.id = lv.id AND i.version = lv.max_version
	`, strings.Join(placeholders, ","))

	rows, err := r.repo.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	// We can reuse our scanInstances method for consistent scanning logic
	instances, _, err := r.scanInstances(ctx, rows, len(ids))
	if err != nil {
		return nil, err
	}

	// Check if all requested IDs were found
	foundIDs := make(map[string]bool)
	for _, instance := range instances {
		foundIDs[instance.ID] = true
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

// ArchiveMany archives multiple instances
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