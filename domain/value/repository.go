package value

import (
	"context"
	"time"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// EntityKey identifies one consumer entity within a tenant. It is the batch
// key for entity-scoped loads.
type EntityKey struct {
	TenantID         valueobjects.TenantID
	TypeDefinitionID valueobjects.TypeDefinitionID
	EntityID         valueobjects.EntityID
}

// Filter narrows List queries.
type Filter struct {
	TenantID              valueobjects.TenantID
	TypeDefinitionID      valueobjects.TypeDefinitionID
	AttributeDefinitionID valueobjects.AttributeDefinitionID
	EntityID              valueobjects.EntityID
	IncludeArchived       bool
}

// EntitySummary is one row of the entity browser: a distinct entity with
// its live value count and most recent change.
type EntitySummary struct {
	EntityID valueobjects.EntityID
	// TypeDefinitionID is the entity's declared type — a descendant of the
	// queried type when browsing includes subtypes.
	TypeDefinitionID valueobjects.TypeDefinitionID
	ValueCount       int
	LastUpdatedAt    time.Time
}

// Repository is the persistence port for attribute values. Reads are
// dataloader-batched; writes run on a transaction-bound repository.
type Repository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.QueryExecer) Repository

	// Get loads one value by ID (batched).
	Get(ctx context.Context, id valueobjects.AttributeValueID) (*AttributeValue, error)

	// GetForUpdate loads one value with a row lock. Only valid on a
	// transaction-bound repository.
	GetForUpdate(ctx context.Context, id valueobjects.AttributeValueID) (*AttributeValue, error)

	// ListByEntity loads every live value of one entity. Loads for
	// different entities batch into one query — the hot path for
	// hydrating consumer objects.
	ListByEntity(ctx context.Context, key EntityKey) ([]*AttributeValue, error)

	// ListByDefinition returns one page of a definition's values plus the
	// total count (pages batch across definitions).
	ListByDefinition(ctx context.Context, defID valueobjects.AttributeDefinitionID, page db.Page) ([]*AttributeValue, int, error)

	// FindByDefinitionAndEntity returns the live values one entity holds
	// for one attribute. Used inside write transactions for multi-value
	// and upsert decisions.
	FindByDefinitionAndEntity(ctx context.Context, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*AttributeValue, error)

	// CountByDefinitionAndValue counts live values of a definition equal to
	// v, excluding entity excludeEntity. Used to enforce unique attributes
	// inside write transactions.
	CountByDefinitionAndValue(ctx context.Context, defID valueobjects.AttributeDefinitionID, v valueobjects.Value, excludeEntity valueobjects.EntityID) (int, error)

	// List returns a page of values and the total count for the filter.
	List(ctx context.Context, filter Filter, page db.Page) ([]*AttributeValue, int, error)

	// ListEntities returns a page of distinct entities holding live values
	// of any of the given type definitions (a type plus, optionally, its
	// descendants), most recently changed first, plus the total
	// distinct-entity count.
	ListEntities(ctx context.Context, tenant valueobjects.TenantID, typeDefIDs []valueobjects.TypeDefinitionID, page db.Page) ([]EntitySummary, int, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, av *AttributeValue) error
}
