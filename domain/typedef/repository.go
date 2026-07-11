package typedef

import (
	"context"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Filter narrows List queries. The zero value lists everything in a tenant.
type Filter struct {
	TenantID        valueobjects.TenantID
	InternalNames   []string
	IncludeArchived bool
	// IncludeAttributeSets includes the hidden relationship attribute-set
	// types; entity listings leave it false.
	IncludeAttributeSets bool
}

// Repository is the persistence port for type definitions. Read paths are
// dataloader-batched; write paths must run on a transaction-bound
// repository obtained through WithTx.
type Repository interface {
	// WithTx returns a repository bound to the given transaction. Reads on
	// the returned repository bypass loader caches so they observe
	// uncommitted writes.
	WithTx(tx db.QueryExecer) Repository

	// Get loads one type definition by ID (batched).
	Get(ctx context.Context, id valueobjects.TypeDefinitionID) (*TypeDefinition, error)

	// GetForUpdate loads one type definition with a row lock. Only valid on
	// a transaction-bound repository.
	GetForUpdate(ctx context.Context, id valueobjects.TypeDefinitionID) (*TypeDefinition, error)

	// GetByInternalName loads one type definition by tenant + machine name.
	GetByInternalName(ctx context.Context, tenant valueobjects.TenantID, internalName string) (*TypeDefinition, error)

	// List returns a page of type definitions and the total count for the
	// filter (batched per filter+page).
	List(ctx context.Context, filter Filter, page db.Page) ([]*TypeDefinition, int, error)

	// ListChildren loads the direct subtypes of a type (batched across
	// parents — hierarchy walks and tree rendering hit this hard).
	ListChildren(ctx context.Context, parentID valueobjects.TypeDefinitionID) ([]*TypeDefinition, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, t *TypeDefinition) error
}
