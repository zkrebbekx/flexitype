package dependency

import (
	"context"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Filter narrows List queries.
type Filter struct {
	TenantID          valueobjects.TenantID
	SourceAttributeID valueobjects.AttributeDefinitionID
	TargetAttributeID valueobjects.AttributeDefinitionID
	IncludeArchived   bool
}

// Repository is the persistence port for dependencies. Reads are
// dataloader-batched; writes run on a transaction-bound repository.
type Repository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.Tx) Repository

	// Get loads one dependency by ID (batched).
	Get(ctx context.Context, id valueobjects.DependencyID) (*Dependency, error)

	// GetForUpdate loads one dependency with a row lock. Only valid on a
	// transaction-bound repository.
	GetForUpdate(ctx context.Context, id valueobjects.DependencyID) (*Dependency, error)

	// ListByTarget loads every live dependency whose effect applies to the
	// given attribute (batched across targets — the value-write hot path).
	ListByTarget(ctx context.Context, targetID valueobjects.AttributeDefinitionID) ([]*Dependency, error)

	// ListBySource loads every live dependency conditioned on the given
	// attribute (batched across sources).
	ListBySource(ctx context.Context, sourceID valueobjects.AttributeDefinitionID) ([]*Dependency, error)

	// List returns a page of dependencies and the total count for the
	// filter.
	List(ctx context.Context, filter Filter, page db.Page) ([]*Dependency, int, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, d *Dependency) error
}
