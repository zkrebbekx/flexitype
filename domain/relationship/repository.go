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

// LinkSide selects which endpoint of a link is the "self" entity whose opposite
// endpoints a windowed page returns — i.e. the direction a GraphQL relationship
// field faces.
type LinkSide int

const (
	// ParentSide means the self entities sit on the parent side; the paged
	// opposite is the child endpoint (an entity-is-parent relationship field).
	ParentSide LinkSide = iota
	// ChildSide means the self entities sit on the child side; the paged
	// opposite is the parent endpoint (an entity-is-child relationship field).
	ChildSide
	// EitherSide means a symmetric relationship — self may sit on either side;
	// the paged opposite is whichever endpoint is not self.
	EitherSide
)

// LinkWindow specifies one relationship field's per-self windowed child load:
// the definition, the direction, and the keyset page applied PER self entity.
// It backs the GraphQL nested-connection resolver, which knows each field's
// definition and direction from the schema.
type LinkWindow struct {
	TenantID     valueobjects.TenantID
	DefinitionID valueobjects.RelationshipDefinitionID
	Side         LinkSide
	Page         db.Page
}

// LinkPage is one self entity's keyset page of opposite endpoints, ordered by
// opposite entity id ascending (the same ordering top-level connections page
// on). HasMore reports whether a further page exists — the repository
// over-fetches one row (Page.Limit+1) to decide, then trims it. Total is the
// full per-self fan-out (independent of the cursor) and is set only when the
// page requested it (Page.WantTotal).
type LinkPage struct {
	Others  []valueobjects.EntityID
	HasMore bool
	Total   *int
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
	// entities to their relationships (e.g. the entity inspector).
	ListByEntities(ctx context.Context, tenant valueobjects.TenantID, entityIDs []valueobjects.EntityID) ([]*Relationship, error)

	// WindowedLinks returns, for each self entity, a keyset page of the
	// opposite endpoints of ONE relationship definition in one direction. The
	// page is applied PER self via a row-number window, so each self reads at
	// most Page.Limit+1 links regardless of its total fan-out — the no-N+1,
	// no-full-materialization path backing GraphQL nested relationship
	// connections. Sibling selves collapse into one windowed query, and the
	// definition/direction filter is pushed into SQL rather than applied after
	// loading every link. The returned map omits selves with no matching links
	// (unless a total was requested, in which case they carry a zero total).
	WindowedLinks(ctx context.Context, w LinkWindow, selves []valueobjects.EntityID) (map[valueobjects.EntityID]LinkPage, error)

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
