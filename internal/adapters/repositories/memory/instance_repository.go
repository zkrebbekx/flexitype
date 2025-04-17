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

// InMemoryInstanceRepository is an in-memory implementation of the InstanceRepository interface
type InMemoryInstanceRepository struct {
	mutex     sync.RWMutex
	instances map[string]map[int]*core.Instance // Map of ID -> Map of Version -> Instance
	typeIndex map[string][]string               // Map of TypeID -> []InstanceID
}

// NewInMemoryInstanceRepository creates a new in-memory instance repository
func NewInMemoryInstanceRepository() *InMemoryInstanceRepository {
	return &InMemoryInstanceRepository{
		instances: make(map[string]map[int]*core.Instance),
		typeIndex: make(map[string][]string),
	}
}

// Save persists an instance
func (r *InMemoryInstanceRepository) Save(ctx context.Context, instance *core.Instance) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	// Ensure the instance map for this ID exists
	if _, exists := r.instances[instance.ID]; !exists {
		r.instances[instance.ID] = make(map[int]*core.Instance)
	}

	// Store the instance with its version
	r.instances[instance.ID][instance.Version] = instance

	// Update type index - only need to track IDs, not versions
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

// SaveMany persists multiple instances
func (r *InMemoryInstanceRepository) SaveMany(ctx context.Context, instances []*core.Instance) error {
	for _, instance := range instances {
		if err := r.Save(ctx, instance); err != nil {
			return err
		}
	}
	return nil
}

// GetByID retrieves the latest version of an instance by ID
func (r *InMemoryInstanceRepository) GetByID(ctx context.Context, id string) (*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	versions, exists := r.instances[id]
	if !exists || len(versions) == 0 {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Find the highest version
	var latestVersion int
	for version := range versions {
		if version > latestVersion {
			latestVersion = version
		}
	}

	return versions[latestVersion], nil
}

// GetByIDAndVersion retrieves a specific version of an instance
func (r *InMemoryInstanceRepository) GetByIDAndVersion(ctx context.Context, id string, version int) (*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	versions, exists := r.instances[id]
	if !exists {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	instance, exists := versions[version]
	if !exists {
		return nil, fmt.Errorf("instance with ID '%s' and version %d not found", id, version)
	}

	return instance, nil
}

// GetLatestVersion returns the highest version number for an instance
func (r *InMemoryInstanceRepository) GetLatestVersion(ctx context.Context, id string) (int, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	versions, exists := r.instances[id]
	if !exists || len(versions) == 0 {
		return 0, fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Find the highest version
	var latestVersion int
	for version := range versions {
		if version > latestVersion {
			latestVersion = version
		}
	}

	return latestVersion, nil
}

// GetAllVersions retrieves all versions of an instance
func (r *InMemoryInstanceRepository) GetAllVersions(ctx context.Context, id string) ([]*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	versions, exists := r.instances[id]
	if !exists || len(versions) == 0 {
		return nil, fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Convert map to slice and sort by version
	result := make([]*core.Instance, 0, len(versions))
	for _, instance := range versions {
		result = append(result, instance)
	}

	// Sort by version (ascending)
	sort.Slice(result, func(i, j int) bool {
		return result[i].Version < result[j].Version
	})

	return result, nil
}

// GetByIDs retrieves multiple instances by IDs (latest versions)
func (r *InMemoryInstanceRepository) GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	result := make([]*core.Instance, 0, len(ids))
	notFound := make([]string, 0)

	for _, id := range ids {
		versions, exists := r.instances[id]
		if exists && len(versions) > 0 {
			// Find the highest version
			var latestVersion int
			for version := range versions {
				if version > latestVersion {
					latestVersion = version
				}
			}
			result = append(result, versions[latestVersion])
		} else {
			notFound = append(notFound, id)
		}
	}

	if len(notFound) > 0 {
		return result, fmt.Errorf("some instances not found: %v", notFound)
	}

	return result, nil
}

// Query retrieves instances by type ID and attribute filters (for backward compatibility)
func (r *InMemoryInstanceRepository) Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error) {
	options := &ports.QueryOptions{
		TypeID:           typeID,
		AttributeFilters: attributeFilters,
		IncludeArchived:  false,
		LatestVersionOnly: true,
	}
	instances, _, err := r.QueryWithOptions(ctx, options)
	return instances, err
}

// QueryWithOptions retrieves instances with advanced filtering options
func (r *InMemoryInstanceRepository) QueryWithOptions(ctx context.Context, options *ports.QueryOptions) ([]*core.Instance, int, error) {
	r.mutex.RLock()
	defer r.mutex.RUnlock()

	// First, identify candidate instances by typeID
	var candidateIDs []string
	if options.TypeID != "" {
		// Get instances for specific type
		candidateIDs = r.typeIndex[options.TypeID]
	} else {
		// Get all instances
		for id := range r.instances {
			candidateIDs = append(candidateIDs, id)
		}
	}

	// Filter by IDs if specified
	if len(options.IDs) > 0 {
		// Create a set of requested IDs for quick lookup
		idSet := make(map[string]bool)
		for _, id := range options.IDs {
			idSet[id] = true
		}

		// Only keep IDs that are in the requested set
		filteredIDs := make([]string, 0)
		for _, id := range candidateIDs {
			if idSet[id] {
				filteredIDs = append(filteredIDs, id)
			}
		}
		candidateIDs = filteredIDs
	}

	// Process the candidates
	filtered := make([]*core.Instance, 0)

	for _, id := range candidateIDs {
		// Process the matching instances
		versions, exists := r.instances[id]
		if !exists || len(versions) == 0 {
			continue
		}

		// Handle versioning
		if options.LatestVersionOnly {
			// Get only the latest version
			var latestVersion int
			for version := range versions {
				if version > latestVersion {
					latestVersion = version
				}
			}
			
			// If specific version is requested and it doesn't match latest, skip
			if options.InstanceVersion > 0 && options.InstanceVersion != latestVersion {
				continue
			}
			
			instance := versions[latestVersion]
			
			// Apply instance-level filters
			if r.matchesInstanceFilter(instance, options) {
				filtered = append(filtered, instance)
			}
		} else {
			// Get all versions or specific version
			if options.InstanceVersion > 0 {
				// Get specific version if it exists
				if instance, hasVersion := versions[options.InstanceVersion]; hasVersion {
					if r.matchesInstanceFilter(instance, options) {
						filtered = append(filtered, instance)
					}
				}
			} else {
				// Get all versions
				for _, instance := range versions {
					if r.matchesInstanceFilter(instance, options) {
						filtered = append(filtered, instance)
					}
				}
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

	// Filter by InstanceID if specified
	if options.InstanceID != "" && instance.ID != options.InstanceID {
		return false
	}

	// Filter by attribute filters
	for attrName, filterValue := range options.AttributeFilters {
		// Get attribute value
		attrValue, exists := instance.Attributes[attrName]
		if !exists {
			// Attribute doesn't exist on instance
			return false
		}

		// Check if the attribute value matches the filter
		if !r.matchesAttributeValue(attrValue, filterValue) {
			return false
		}
	}

	return true
}

// matchesAttributeValue checks if an attribute value matches a filter value
func (r *InMemoryInstanceRepository) matchesAttributeValue(value, filter interface{}) bool {
	// Handle nil values
	if value == nil {
		return filter == nil
	}

	// Handle string case insensitive comparison
	if strValue, ok := value.(string); ok {
		if strFilter, ok := filter.(string); ok {
			return strings.EqualFold(strValue, strFilter)
		}
	}

	// For other types, do direct comparison
	return fmt.Sprintf("%v", value) == fmt.Sprintf("%v", filter)
}

// sortInstances sorts the instances by the specified field and direction
func (r *InMemoryInstanceRepository) sortInstances(instances []*core.Instance, orderBy, orderDir string) {
	if orderBy == "" {
		orderBy = "id" // Default sort field
	}
	
	ascending := true
	if strings.ToUpper(orderDir) == "DESC" {
		ascending = false
	}

	sort.Slice(instances, func(i, j int) bool {
		var result bool
		
		// Get values to compare based on the orderBy field
		switch orderBy {
		case "id":
			result = instances[i].ID < instances[j].ID
		case "type_id":
			result = instances[i].TypeDefinition.ID < instances[j].TypeDefinition.ID
		case "version":
			result = instances[i].Version < instances[j].Version
		case "created_at":
			result = instances[i].CreatedAt.Before(instances[j].CreatedAt)
		case "updated_at":
			result = instances[i].UpdatedAt.Before(instances[j].UpdatedAt)
		default:
			// Sort by ID as fallback
			result = instances[i].ID < instances[j].ID
		}
		
		// Reverse the result for descending order
		if !ascending {
			return !result
		}
		return result
	})
}

// Archive marks an instance as archived at the current time
func (r *InMemoryInstanceRepository) Archive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	versions, exists := r.instances[id]
	if !exists || len(versions) == 0 {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Archive all versions
	now := time.Now()
	for version, instance := range versions {
		instance.ArchivedAt = &now
		versions[version] = instance
	}

	return nil
}

// Unarchive removes the archived status from an instance
func (r *InMemoryInstanceRepository) Unarchive(ctx context.Context, id string) error {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	versions, exists := r.instances[id]
	if !exists || len(versions) == 0 {
		return fmt.Errorf("instance with ID '%s' not found", id)
	}

	// Unarchive all versions
	for version, instance := range versions {
		instance.ArchivedAt = nil
		versions[version] = instance
	}

	return nil
}

// ArchiveMany marks multiple instances as archived
func (r *InMemoryInstanceRepository) ArchiveMany(ctx context.Context, ids []string) error {
	for _, id := range ids {
		if err := r.Archive(ctx, id); err != nil {
			return err
		}
	}
	return nil
}