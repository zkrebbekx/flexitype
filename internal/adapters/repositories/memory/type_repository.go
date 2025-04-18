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
	types          map[string]*core.TypeDefinition         // Map of Name -> TypeDefinition (name is now the primary key)
	versionedTypes map[string]map[int]*core.TypeDefinition // Map of Name -> Map of Version -> TypeDefinition
}

// NewInMemoryTypeRepository creates a new in-memory type repository
func NewInMemoryTypeRepository() *InMemoryTypeRepository {
	return &InMemoryTypeRepository{
		types:          make(map[string]*core.TypeDefinition),
		versionedTypes: make(map[string]map[int]*core.TypeDefinition),
	}
}

// Save persists a type definition and automatically creates a version snapshot
func (r *InMemoryTypeRepository) Save(ctx context.Context, typeDef *core.TypeDefinition) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Store the latest version in the types map using name as the key
	r.types[strings.ToLower(typeDef.Name)] = typeDef

	// Initialize version map if needed
	if _, exists := r.versionedTypes[strings.ToLower(typeDef.Name)]; !exists {
		r.versionedTypes[strings.ToLower(typeDef.Name)] = make(map[int]*core.TypeDefinition)
	}

	// Create a deep copy of the type definition for storing as a version
	versionCopy := r.deepCopyTypeDef(typeDef)

	// Store the version in the versioned types map
	r.versionedTypes[strings.ToLower(typeDef.Name)][typeDef.Version] = versionCopy

	return nil
}

// deepCopyTypeDef creates a deep copy of a type definition
func (r *InMemoryTypeRepository) deepCopyTypeDef(typeDef *core.TypeDefinition) *core.TypeDefinition {
	// Create a new type definition with the same basic properties
	copy := core.NewTypeDefinition(typeDef.Name, typeDef.Description)
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
// With ID removed, we now assume ID is the same as name
func (r *InMemoryTypeRepository) GetByID(ctx context.Context, id string) (*core.TypeDefinition, error) {
	return r.GetByName(ctx, id)
}

// GetByIDs retrieves multiple type definitions by IDs
// With ID removed, we assume IDs are the same as names
func (r *InMemoryTypeRepository) GetByIDs(ctx context.Context, ids []string) ([]*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make([]*core.TypeDefinition, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		// Now we directly look up by name (case insensitive)
		typeDef, exists := r.types[strings.ToLower(id)]
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

	// Look up case-insensitive
	typeDef, exists := r.types[strings.ToLower(name)]
	if !exists {
		return nil, fmt.Errorf("type with name '%s' not found", name)
	}

	return typeDef, nil
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
	if len(options.Names) > 0 {
		found := false
		for _, name := range options.Names {
			if typeDef.Name == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by name if specified - case insensitive
	if options.Name != "" && strings.ToLower(typeDef.Name) != strings.ToLower(options.Name) {
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

	// Filter by name in list if specified - case insensitive
	if len(options.NameIn) > 0 {
		found := false
		for _, name := range options.NameIn {
			if strings.ToLower(typeDef.Name) == strings.ToLower(name) {
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
		orderBy = "name" // Change default sort to name (primary key)
	}

	ascending := strings.ToUpper(orderDir) != "DESC"

	sort.Slice(types, func(i, j int) bool {
		var less bool

		switch strings.ToLower(orderBy) {
		case "id":
			less = types[i].Name < types[j].Name
		case "name":
			// Case insensitive name comparison
			less = strings.ToLower(types[i].Name) < strings.ToLower(types[j].Name)
		case "description":
			less = types[i].Description < types[j].Description
		case "version":
			less = types[i].Version < types[j].Version
		default:
			// Default to name (case insensitive)
			less = strings.ToLower(types[i].Name) < strings.ToLower(types[j].Name)
		}

		if !ascending {
			return !less
		}
		return less
	})
}

// Archive marks a type definition as archived at the current time
func (r *InMemoryTypeRepository) Archive(ctx context.Context, name string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Get the type by name (case insensitive)
	typeDef, exists := r.types[strings.ToLower(name)]
	if !exists {
		return fmt.Errorf("type with name '%s' not found", name)
	}

	// Set archived timestamp
	now := time.Now()
	typeDef.ArchivedAt = &now

	return nil
}

// Unarchive removes the archived status from a type definition
func (r *InMemoryTypeRepository) Unarchive(ctx context.Context, name string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Get the type by name (case insensitive)
	typeDef, exists := r.types[strings.ToLower(name)]
	if !exists {
		return fmt.Errorf("type with name '%s' not found", name)
	}

	// Remove archived timestamp
	typeDef.ArchivedAt = nil

	return nil
}

// ArchiveMany marks multiple type definitions as archived
func (r *InMemoryTypeRepository) ArchiveMany(ctx context.Context, names []string) error {
	if len(names) == 0 {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	notFound := make([]string, 0)
	now := time.Now()

	for _, name := range names {
		// Get the type by name (case insensitive)
		typeDef, exists := r.types[strings.ToLower(name)]
		if exists {
			typeDef.ArchivedAt = &now
		} else {
			notFound = append(notFound, name)
		}
	}

	if len(notFound) > 0 {
		return fmt.Errorf("some types not found: %v", notFound)
	}

	return nil
}

// GetByIDAndVersion retrieves a specific version of a type definition
// With ID removed, we assume ID is the same as name
func (r *InMemoryTypeRepository) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.TypeDefinition, error) {
	return r.GetByNameAndVersion(ctx, id, version)
}

// GetByNameAndVersion retrieves a specific version of a type definition by name
func (r *InMemoryTypeRepository) GetByNameAndVersion(ctx context.Context, name string, version int) (*core.TypeDefinition, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// Convert to lowercase for case-insensitive lookup
	nameLower := strings.ToLower(name)

	// Check if we have this type
	if _, exists := r.types[nameLower]; !exists {
		return nil, fmt.Errorf("type with name '%s' not found", name)
	}

	// Check if we have versions stored
	versions, exists := r.versionedTypes[nameLower]
	if !exists {
		return nil, fmt.Errorf("no versions found for type '%s'", name)
	}

	// Get the specific version
	typeDef, exists := versions[version]
	if !exists {
		return nil, fmt.Errorf("version %d not found for type '%s'", version, name)
	}

	return typeDef, nil
}
