package relationship

import (
	"context"

	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// DefinitionFilter narrows definition List queries.
type DefinitionFilter struct {
	TenantID valueobjects.TenantID
	// TypeDefinitionIDs matches definitions whose parent or child side is
	// any of the given types (callers pass a type plus its ancestors so
	// inherited relationship types surface on subtypes).
	TypeDefinitionIDs []valueobjects.TypeDefinitionID
	IncludeArchived   bool
}

// DefinitionRepository is the persistence port for relationship
// definitions.
type DefinitionRepository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.QueryExecer) DefinitionRepository

	// Get loads one definition by ID (batched).
	Get(ctx context.Context, id valueobjects.RelationshipDefinitionID) (*Definition, error)

	// GetForUpdate loads one definition with a row lock (transaction only).
	GetForUpdate(ctx context.Context, id valueobjects.RelationshipDefinitionID) (*Definition, error)

	// GetByInternalName loads one definition by tenant + machine name.
	GetByInternalName(ctx context.Context, tenant valueobjects.TenantID, internalName string) (*Definition, error)

	// List returns a page of definitions and the total count for the
	// filter.
	List(ctx context.Context, filter DefinitionFilter, page db.Page) ([]*Definition, int, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, d *Definition) error
}

// Filter narrows relationship List queries.
type Filter struct {
	TenantID        valueobjects.TenantID
	DefinitionID    valueobjects.RelationshipDefinitionID
	ParentEntityID  valueobjects.EntityID
	ChildEntityID   valueobjects.EntityID
	IncludeArchived bool
}

// EntityLinksKey batches "every live link touching one entity" loads.
type EntityLinksKey struct {
	TenantID valueobjects.TenantID
	EntityID valueobjects.EntityID
}

// Repository is the persistence port for relationship instances.
type Repository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.QueryExecer) Repository

	// Get loads one relationship by ID (batched).
	Get(ctx context.Context, id valueobjects.RelationshipID) (*Relationship, error)

	// GetForUpdate loads one relationship with a row lock (transaction
	// only).
	GetForUpdate(ctx context.Context, id valueobjects.RelationshipID) (*Relationship, error)

	// FindLive returns the live link for (definition, parent, child), or
	// nil. Used inside link transactions to enforce one live link per pair.
	FindLive(ctx context.Context, defID valueobjects.RelationshipDefinitionID, parent, child valueobjects.EntityID) (*Relationship, error)

	// ListByEntity loads every live link where the entity appears on
	// either side (batched across entities — the inspector hot path).
	ListByEntity(ctx context.Context, key EntityLinksKey) ([]*Relationship, error)

	// ListByEntities loads every live link touching any of the given
	// entities, in one query — the no-N+1 path for fanning out a set of
	// entities to their relationships (e.g. the GraphQL resolver).
	ListByEntities(ctx context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*Relationship, error)

	// CountLiveLinks returns how many live links of a definition have the
	// entity as parent and as child. Used under the definition lock to
	// enforce cardinality.
	CountLiveLinks(ctx context.Context, defID valueobjects.RelationshipDefinitionID, entity valueobjects.EntityID) (asParent, asChild int, err error)

	// List returns a page of relationships and the total count for the
	// filter.
	List(ctx context.Context, filter Filter, page db.Page) ([]*Relationship, int, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, r *Relationship) error

	// PurgeEntity HARD-deletes every link touching an entity — as parent OR
	// child, including already-archived links — for the right-to-erasure
	// primitive. It returns the number of links deleted. Only valid on a
	// transaction-bound repository.
	PurgeEntity(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) (int, error)

	// PurgeTenant HARD-deletes every link of a tenant (including archived
	// links), returning the row count. Only valid on a transaction-bound
	// repository.
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error)
}
