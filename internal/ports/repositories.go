package ports

import (
	"context"
	"github.com/zac300/flexitype/internal/domain/core"
)

// QueryOptions defines options for list/query operations
type QueryOptions struct {
	// Pagination
	Offset int
	Limit  int

	// Ordering
	OrderBy  string
	OrderDir string // "ASC" or "DESC"

	// Filtering by multiple IDs
	IDs []string // Primary keys or foreign keys

	// Filtering by type
	TypeID string

	// Core field filters
	Name        string
	Description string
	Version     int
	
	// Instance field filters
	InstanceID      string
	InstanceVersion int

	// Attribute filters
	AttributeFilters map[string]interface{}

	// For advanced filtering with multiple values
	NameIn        []string
	DescriptionIn []string
	VersionIn     []int

	// Include archived items in results (defaults to false)
	IncludeArchived bool
	
	// Whether to get the latest version only (defaults to true)
	LatestVersionOnly bool
}

// DefaultQueryOptions returns default query options
func DefaultQueryOptions() *QueryOptions {
	return &QueryOptions{
		Offset:           0,
		Limit:            100,
		OrderBy:          "id",
		OrderDir:         "ASC",
		IDs:              []string{},
		AttributeFilters: make(map[string]interface{}),
		IncludeArchived:  false, // By default, don't include archived items
		LatestVersionOnly: true, // By default, get only the latest version
	}
}

// TypeRepository defines the interface for type definition storage
type TypeRepository interface {
	// Save persists a type definition
	Save(ctx context.Context, typeDef *core.TypeDefinition) error

	// GetByID retrieves a type definition by ID
	GetByID(ctx context.Context, id string) (*core.TypeDefinition, error)

	// GetByIDs retrieves multiple type definitions by IDs
	GetByIDs(ctx context.Context, ids []string) ([]*core.TypeDefinition, error)

	// GetByName retrieves a type definition by name
	GetByName(ctx context.Context, name string) (*core.TypeDefinition, error)

	// List retrieves all type definitions with pagination and filtering
	List(ctx context.Context, options *QueryOptions) ([]*core.TypeDefinition, int, error)

	// Archive marks a type definition as archived at the current time
	Archive(ctx context.Context, id string) error

	// Unarchive removes the archived status from a type definition
	Unarchive(ctx context.Context, id string) error

	// ArchiveMany marks multiple type definitions as archived
	ArchiveMany(ctx context.Context, ids []string) error
	
	// SaveVersion creates a snapshot of the current type definition as a version
	SaveVersion(ctx context.Context, typeID string) error
	
	// GetByIDAndVersion retrieves a specific version of a type definition
	GetByIDAndVersion(ctx context.Context, id string, version int) (*core.TypeDefinition, error)
}

// InstanceRepository defines the interface for instance storage
type InstanceRepository interface {
	// Save persists an instance
	Save(ctx context.Context, instance *core.Instance) error

	// SaveMany persists multiple instances in a single transaction
	SaveMany(ctx context.Context, instances []*core.Instance) error

	// GetByID retrieves the latest instance version by ID
	GetByID(ctx context.Context, id string) (*core.Instance, error)
	
	// GetByIDAndVersion retrieves a specific instance version by ID and version
	GetByIDAndVersion(ctx context.Context, id string, version int) (*core.Instance, error)
	
	// GetLatestVersion returns the highest version number for the given instance ID
	GetLatestVersion(ctx context.Context, id string) (int, error)
	
	// GetAllVersions retrieves all versions of an instance by ID
	GetAllVersions(ctx context.Context, id string) ([]*core.Instance, error)

	// GetByIDs retrieves multiple instances by IDs (latest versions)
	GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error)

	// Query retrieves instances by query criteria (for backward compatibility)
	Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error)

	// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
	// Using LatestVersionOnly option to control whether to return all versions or just the latest
	QueryWithOptions(ctx context.Context, options *QueryOptions) ([]*core.Instance, int, error)

	// Archive marks an instance (all versions) as archived at the current time
	Archive(ctx context.Context, id string) error

	// Unarchive removes the archived status from an instance (all versions)
	Unarchive(ctx context.Context, id string) error

	// ArchiveMany marks multiple instances (all versions) as archived
	ArchiveMany(ctx context.Context, ids []string) error
}
