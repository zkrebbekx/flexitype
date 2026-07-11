package attribute

import (
	"context"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Filter narrows List queries.
type Filter struct {
	TenantID         valueobjects.TenantID
	TypeDefinitionID valueobjects.TypeDefinitionID
	InternalNames    []string
	DataTypes        []valueobjects.DataType
	IncludeArchived  bool
}

// Repository is the persistence port for attribute definitions. Reads are
// dataloader-batched (including per-type-definition pagination); writes run
// on a transaction-bound repository from WithTx.
type Repository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.QueryExecer) Repository

	// Get loads one definition by ID (batched).
	Get(ctx context.Context, id valueobjects.AttributeDefinitionID) (*Definition, error)

	// GetMany loads many definitions in one round trip, preserving order.
	GetMany(ctx context.Context, ids []valueobjects.AttributeDefinitionID) ([]*Definition, error)

	// GetForUpdate loads one definition with a row lock. Only valid on a
	// transaction-bound repository.
	GetForUpdate(ctx context.Context, id valueobjects.AttributeDefinitionID) (*Definition, error)

	// GetByInternalName loads one definition by type definition + machine
	// name.
	GetByInternalName(ctx context.Context, typeDefID valueobjects.TypeDefinitionID, internalName string) (*Definition, error)

	// ListByTypeDefinition returns one page of a type definition's
	// attributes plus the total count. Pages for different parents batch
	// into a single windowed query.
	ListByTypeDefinition(ctx context.Context, typeDefID valueobjects.TypeDefinitionID, page db.Page) ([]*Definition, int, error)

	// List returns a page of definitions and the total count for the
	// filter (batched per filter+page).
	List(ctx context.Context, filter Filter, page db.Page) ([]*Definition, int, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, a *Definition) error
}
