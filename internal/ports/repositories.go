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

	// Attribute filters
	AttributeFilters map[string]interface{}

	// For advanced filtering with multiple values
	NameIn        []string
	DescriptionIn []string
	VersionIn     []int

	// Include archived items in results (defaults to false)
	IncludeArchived bool
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
}

// InstanceRepository defines the interface for instance storage
type InstanceRepository interface {
	// Save persists an instance
	Save(ctx context.Context, instance *core.Instance) error

	// SaveMany persists multiple instances in a single transaction
	SaveMany(ctx context.Context, instances []*core.Instance) error

	// GetByID retrieves an instance by ID
	GetByID(ctx context.Context, id string) (*core.Instance, error)

	// GetByIDs retrieves multiple instances by IDs
	GetByIDs(ctx context.Context, ids []string) ([]*core.Instance, error)

	// Query retrieves instances by query criteria (for backward compatibility)
	Query(ctx context.Context, typeID string, attributeFilters map[string]interface{}) ([]*core.Instance, error)

	// QueryWithOptions retrieves instances with pagination, ordering, and advanced filtering
	QueryWithOptions(ctx context.Context, options *QueryOptions) ([]*core.Instance, int, error)

	// Archive marks an instance as archived at the current time
	Archive(ctx context.Context, id string) error

	// Unarchive removes the archived status from an instance
	Unarchive(ctx context.Context, id string) error

	// ArchiveMany marks multiple instances as archived
	ArchiveMany(ctx context.Context, ids []string) error
}
