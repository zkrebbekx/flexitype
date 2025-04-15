package memory

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/ports"
)

// InMemoryInstanceRepository is an in-memory implementation of the InstanceRepository interface
type InMemoryInstanceRepository struct {
	mutex     sync.RWMutex
	instances map[string]*core.Instance // Map of ID -> Instance
	typeIndex map[string][]string       // Map of TypeID -> []InstanceID
}

// NewInMemoryInstanceRepository creates a new in-memory instance repository
func NewInMemoryInstanceRepository() *InMemoryInstanceRepository {
	return &InMemoryInstanceRepository{
		instances: make(map[string]*core.Instance),
		typeIndex: make(map[string][]string),
	}
}

// Save persists an instance
func (r *InMemoryInstanceRepository) Save(ctx context.Context, instance *core.Instance) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Store the instance
	r.instances[instance.ID] = instance

	// Update type index
	typeID := instance.TypeDefinition.ID
	instanceIDs, exists := r.typeIndex[typeID]
	if !exists {
		instanceIDs = []string{}
	}

	// Check if already in the type index
	found := false
	for _, id := range instanceIDs {
		if id == instance.ID {
			found = true
			break
		}
	}

	if !found {
		instanceIDs = append(instanceIDs, instance.ID)
		r.typeIndex[typeID] = instanceIDs
	}

	return nil
}

// GetByID retrieves an instance by ID
func (r *InMemoryInstanceRepository) GetByID(ctx context.Context, id string) (*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	instance, exists := r.instances[id]
	if !exists {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	return instance, nil
}

// GetByIDs retrieves multiple instances by IDs
func (r *InMemoryInstanceRepository) GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make([]*core.Instance, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		instance, exists := r.instances[id]
		if exists {
			result = append(result, instance)
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) > 0 {
		return result, fmt.Errorf("some instances not found: %v", notFound)
	}

	return result, nil
}

// SaveMany persists multiple instances in a single transaction
func (r *InMemoryInstanceRepository) SaveMany(ctx context.Context, instances []*core.Instance) error {
	if len(instances) == 0 {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	for _, instance := range instances {
		// Store the instance
		r.instances[instance.ID] = instance

		// Update type index
		typeID := instance.TypeDefinition.ID
		instanceIDs, exists := r.typeIndex[typeID]
		if !exists {
			instanceIDs = []string{}
		}

		// Check if already in the type index
		found := false
		for _, id := range instanceIDs {
			if id == instance.ID {
				found = true
				break
			}
		}

		if !found {
			instanceIDs = append(instanceIDs, instance.ID)
			r.typeIndex[typeID] = instanceIDs
		}
	}

	return nil
}

// Query retrieves instances by query criteria
func (r *InMemoryInstanceRepository) Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// Get all instances of the specified type
	var candidates []*core.Instance
	if typeID != "" {
		// Use type index for filtering by type
		instanceIDs, exists := r.typeIndex[typeID]
		if !exists {
			return []*core.Instance{}, nil
		}

		candidates = make([]*core.Instance, 0, len(instanceIDs))
		for _, id := range instanceIDs {
			if instance, ok := r.instances[id]; ok {
				candidates = append(candidates, instance)
			}
		}
	} else {
		// No type filter, include all instances
		candidates = make([]*core.Instance, 0, len(r.instances))
		for _, instance := range r.instances {
			candidates = append(candidates, instance)
		}
	}

	// Apply attribute filters
	if len(attributeFilters) == 0 {
		return candidates, nil
	}

	result := make([]*core.Instance, 0)
	for _, instance := range candidates {
		match := true

		// Check each filter
		for attrName, filterValue := range attributeFilters {
			attrValue, err := instance.GetAttribute(attrName)
			if err != nil || !reflect.DeepEqual(attrValue, filterValue) {
				match = false
				break
			}
		}

		if match {
			result = append(result, instance)
		}
	}

	return result, nil
}

// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
func (r *InMemoryInstanceRepository) QueryWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// If options not provided, use defaults
	if options == nil {
		options = ports.DefaultQueryOptions()
	}

	// Start with all instances or filter by type if specified
	filtered := make([]*core.Instance, 0)

	if options.TypeID != "" {
		// Use type index for filtering by type
		instanceIDs, exists := r.typeIndex[options.TypeID]
		if !exists {
			return []*core.Instance{}, 0, nil
		}

		for _, id := range instanceIDs {
			if instance, ok := r.instances[id]; ok {
				// Apply additional filtering
				if r.matchesInstanceFilter(instance, options) {
					filtered = append(filtered, instance)
				}
			}
		}
	} else {
		// No type filter, include all instances that match other filters
		for _, instance := range r.instances {
			if r.matchesInstanceFilter(instance, options) {
				filtered = append(filtered, instance)
			}
		}
	}

	// Get total count before pagination
	totalCount := len(filtered)

	// Apply sorting
	r.sortInstances(filtered, options.OrderBy, options.OrderDir)

	// Apply pagination
	start := options.Offset
	if start >= len(filtered) {
		return []*core.Instance{}, totalCount, nil
	}

	end := options.Offset + options.Limit
	if end > len(filtered) {
		end = len(filtered)
	}

	return filtered[start:end], totalCount, nil
}

// matchesInstanceFilter checks if an instance matches the filter criteria
func (r *InMemoryInstanceRepository) matchesInstanceFilter(instance *core.Instance, options *ports.QueryOptions) bool {
	// Filter by archive status
	if !options.IncludeArchived && instance.ArchivedAt != nil {
		return false
	}

	// Filter by IDs if specified
	if len(options.IDs) > 0 {
		found := false
		for _, id := range options.IDs {
			if instance.ID == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by name if specified - Instance doesn't have a Name field
	if options.Name != "" {
		// Since instances don't have a Name field directly, we skip this filter
		return false
	}

	// Filter by description if specified - Instance doesn't have a Description field
	if options.Description != "" {
		// Since instances don't have a Description field directly, we skip this filter
		return false
	}

	// Filter by version if specified
	if options.Version > 0 && instance.TypeVersion != options.Version {
		return false
	}

	// Filter by name in list if specified - Instance doesn't have a Name field
	if len(options.NameIn) > 0 {
		// Since instances don't have a Name field directly, we skip this filter
		return false
	}

	// Filter by version in list if specified
	if len(options.VersionIn) > 0 {
		found := false
		for _, version := range options.VersionIn {
			if instance.TypeVersion == version {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Apply attribute filters if any
	if len(options.AttributeFilters) > 0 {
		for attrName, filterValue := range options.AttributeFilters {
			attrValue, err := instance.GetAttribute(attrName)
			if err != nil || !reflect.DeepEqual(attrValue, filterValue) {
				return false
			}
		}
	}

	return true
}

// sortInstances sorts instances by the specified field and direction
func (r *InMemoryInstanceRepository) sortInstances(instances []*core.Instance, orderBy, orderDir string) {
	if orderBy == "" {
		orderBy = "id"
	}

	ascending := strings.ToUpper(orderDir) != "DESC"

	sort.Slice(instances, func(i, j int) bool {
		var less bool

		switch strings.ToLower(orderBy) {
		case "id":
			less = instances[i].ID < instances[j].ID
		case "typeversion":
			less = instances[i].TypeVersion < instances[j].TypeVersion
		case "typeid":
			less = instances[i].TypeDefinition.ID < instances[j].TypeDefinition.ID
		case "typename":
			less = instances[i].TypeDefinition.Name < instances[j].TypeDefinition.Name
		default:
			less = instances[i].ID < instances[j].ID
		}

		if !ascending {
			return !less
		}
		return less
	})
}

// Archive marks an instance as archived at the current time
func (r *InMemoryInstanceRepository) Archive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	instance, exists := r.instances[id]
	if !exists {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Set archived timestamp
	now := time.Now()
	instance.ArchivedAt = &now

	return nil
}

// Unarchive removes the archived status from an instance
func (r *InMemoryInstanceRepository) Unarchive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	instance, exists := r.instances[id]
	if !exists {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Remove archived timestamp
	instance.ArchivedAt = nil

	return nil
}

// ArchiveMany marks multiple instances as archived
func (r *InMemoryInstanceRepository) ArchiveMany(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	r.mutex.Lock()
	defer r.mutex.Unlock()

	notFound := make([]string, 0)
	now := time.Now()

	for _, id := range ids {
		instance, exists := r.instances[id]
		if exists {
			instance.ArchivedAt = &now
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) > 0 {
		return fmt.Errorf("some instances not found: %v", notFound)
	}

	return nil
}
