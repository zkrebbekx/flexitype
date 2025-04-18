package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// InMemoryTypeRepository is an in-memory implementation of the TypeRepository interface
type InMemoryTypeRepository struct {
	mutex          sync.RWMutex
	types          map[string]*core.TypeDefinition         // Map of ID -> TypeDefinition
	nameIndex      map[string]string                       // Map of Name -> ID for lookups
	versionedTypes map[string]map[int]*core.TypeDefinition // Map of ID -> Map of Version -> TypeDefinition
}

// NewInMemoryTypeRepository creates a new in-memory type repository
func NewInMemoryTypeRepository() *InMemoryTypeRepository {
	return &InMemoryTypeRepository{
		types:          make(map[string]*core.TypeDefinition),
		nameIndex:      make(map[string]string),
		versionedTypes: make(map[string]map[int]*core.TypeDefinition),
	}
}

// Save persists a type definition and automatically creates a version snapshot
func (r *InMemoryTypeRepository) Save(ctx context.Context, typeDef *core.TypeDefinition) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Check for name collision (except with self)
	if existingID, found := r.nameIndex[typeDef.Name]; found && existingID != typeDef.ID {
		return fmt.Errorf("type with name '%s' already exists", typeDef.Name)
	}

	// Store the latest version in the types map
	r.types[typeDef.ID] = typeDef
	r.nameIndex[typeDef.Name] = typeDef.ID

	// Initialize version map if needed
	if _, exists := r.versionedTypes[typeDef.ID]; !exists {
		r.versionedTypes[typeDef.ID] = make(map[int]*core.TypeDefinition)
	}

	// Create a deep copy of the type definition for storing as a version
	versionCopy := r.deepCopyTypeDef(typeDef)

	// Store the version in the versioned types map
	r.versionedTypes[typeDef.ID][typeDef.Version] = versionCopy

	return nil
}

// deepCopyTypeDef creates a deep copy of a type definition
func (r *InMemoryTypeRepository) deepCopyTypeDef(typeDef *core.TypeDefinition) *core.TypeDefinition {
	// Create a new type definition with the same basic properties
	copy := core.NewTypeDefinition(typeDef.ID, typeDef.Name, typeDef.Description)
	copy.Version = typeDef.Version
	copy.CreatedAt = typeDef.CreatedAt
	copy.UpdatedAt = typeDef.UpdatedAt

	// Copy archived status if present
	if typeDef.ArchivedAt != nil {
		archivedTime := *typeDef.ArchivedAt
		copy.ArchivedAt = &archivedTime
	}

	// Copy parent type reference (but don't deep copy the parent)
	copy.ParentType = typeDef.ParentType

	// Copy each attribute (deep copy)
	for _, attr := range typeDef.Attributes {
		// Create a new attribute with same properties
		newAttr := core.NewAttributeDefinition(
			attr.ID,
			attr.Name,
			attr.Description,
			attr.DataType,
			attr.Required,
		)

		// Copy other properties
		newAttr.SetMultiValued(attr.MultiValued)
		newAttr.SetDisabled(attr.Disabled)
		if attr.DefaultValue != nil {
			newAttr.SetDefaultValue(attr.DefaultValue)
		}

		// Copy validation rules and cascades
		for _, rule := range attr.ValidationRules {
			newAttr.AddValidationRule(rule)
		}

		for _, cascade := range attr.Cascades {
			newAttr.AddCascade(
				cascade.ID,
				cascade.Enabled,
				cascade.Behavior,
				cascade.Logic,
				cascade.Weight,
			)
		}

		// Add to type
		copy.AddAttribute(newAttr)
	}

	return copy
}

// GetByID retrieves a type definition by ID
func (r *InMemoryTypeRepository) GetByID(ctx context.Context, id string) (*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	typeDef, exists := r.types[id]
	if !exists {
		return nil, fmt.Errorf("type with ID '%s' not found", id)
	}

	return typeDef, nil
}

// GetByIDs retrieves multiple type definitions by IDs
func (r *InMemoryTypeRepository) GetByIDs(ctx context.Context, ids []string) ([]*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make([]*core.TypeDefinition, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		typeDef, exists := r.types[id]
		if exists {
			result = append(result, typeDef)
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) > 0 {
		return result, fmt.Errorf("some types not found: %v", notFound)
	}

	return result, nil
}

// GetByName retrieves a type definition by name
func (r *InMemoryTypeRepository) GetByName(ctx context.Context, name string) (*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	id, exists := r.nameIndex[name]
	if !exists {
		return nil, fmt.Errorf("type with name '%s' not found", name)
	}

	return r.types[id], nil
}

// ListWithOptions retrieves all type definitions with pagination and filtering
func (r *InMemoryTypeRepository) ListWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.TypeDefinition, int, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// If options not provided, use defaults
	if options == nil {
		options = ports.DefaultQueryOptions()
	}

	// Start with all types
	filtered := make([]*core.TypeDefinition, 0, len(r.types))
	for _, typeDef := range r.types {
		// Apply filtering
		if !r.matchesTypeFilter(typeDef, options) {
			continue
		}

		filtered = append(filtered, typeDef)
	}

	// Get total count before pagination
	totalCount := len(filtered)

	// Apply sorting
	r.sortTypes(filtered, options.OrderBy, options.OrderDir)

	// Apply pagination
	start := options.Offset
	if start >= len(filtered) {
		return []*core.TypeDefinition{}, totalCount, nil
	}

	end := options.Offset + options.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return filtered[start:end], totalCount, nil
}

// List retrieves all type definitions with pagination and filtering
func (r *InMemoryTypeRepository) List(ctx context.Context, options *ports.QueryOptions) ([]*core.TypeDefinition, int, error) {
	return r.ListWithOptions(ctx, options)
}

// matchesTypeFilter checks if a type definition matches the filter criteria
func (r *InMemoryTypeRepository) matchesTypeFilter(typeDef *core.TypeDefinition, options *ports.QueryOptions) bool {
	// Filter by archive status
	if !options.IncludeArchived && typeDef.ArchivedAt != nil {
		return false
	}

	// Filter by IDs if specified
	if len(options.IDs) > 0 {
		found := false
		for _, id := range options.IDs {
			if typeDef.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by name if specified
	if options.Name != "" && typeDef.Name != options.Name {
		return false
	}

	// Filter by description if specified
	if options.Description != "" && !strings.Contains(typeDef.Description, options.Description) {
		return false
	}

	// Filter by version if specified
	if options.Version > 0 && typeDef.Version != options.Version {
		return false
	}

	// Filter by name in list if specified
	if len(options.NameIn) > 0 {
		found := false
		for _, name := range options.NameIn {
			if typeDef.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by version in list if specified
	if len(options.VersionIn) > 0 {
		found := false
		for _, version := range options.VersionIn {
			if typeDef.Version == version {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// sortTypes sorts type definitions by the specified field and direction
func (r *InMemoryTypeRepository) sortTypes(types []*core.TypeDefinition, orderBy, orderDir string) {
	if orderBy == "" {
		orderBy = "id"
	}

	ascending := strings.ToUpper(orderDir) != "DESC"

	sort.Slice(types, func(i, j int) bool {
		var less bool

		switch strings.ToLower(orderBy) {
		case "id":
			less = types[i].ID < types[j].ID
		case "name":
			less = types[i].Name < types[j].Name
		case "description":
			less = types[i].Description < types[j].Description
		case "version":
			less = types[i].Version < types[j].Version
		default:
			less = types[i].ID < types[j].ID
		}

		if !ascending {
			return !less
		}
		return less
	})
}

// Archive marks a type definition as archived at the current time
func (r *InMemoryTypeRepository) Archive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	typeDef, exists := r.types[id]
	if !exists {
		return fmt.Errorf("type with ID '%s' not found", id)
	}

	// Set archived timestamp
	now := time.Now()
	typeDef.ArchivedAt = &now

	return nil
}

// Unarchive removes the archived status from a type definition
func (r *InMemoryTypeRepository) Unarchive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	typeDef, exists := r.types[id]
	if !exists {
		return fmt.Errorf("type with ID '%s' not found", id)
	}

	// Remove archived timestamp
	typeDef.ArchivedAt = nil

	return nil
}

// ArchiveMany marks multiple type definitions as archived
func (r *InMemoryTypeRepository) ArchiveMany(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	notFound := make([]string, 0)
	now := time.Now()

	for _, id := range ids {
		typeDef, exists := r.types[id]
		if exists {
			typeDef.ArchivedAt = &now
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) > 0 {
		return fmt.Errorf("some types not found: %v", notFound)
	}

	return nil
}

// GetByIDAndVersion retrieves a specific version of a type definition
func (r *InMemoryTypeRepository) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// Check if the type exists
	if _, exists := r.types[id]; !exists {
		return nil, fmt.Errorf("type with ID '%s' not found", id)
	}

	// Check if we have versions stored
	versions, exists := r.versionedTypes[id]
	if !exists {
		return nil, fmt.Errorf("no versions found for type '%s'", id)
	}

	// Get the specific version
	typeDef, exists := versions[version]
	if !exists {
		return nil, fmt.Errorf("version %d not found for type '%s'", version, id)
	}

	return typeDef, nil
}
