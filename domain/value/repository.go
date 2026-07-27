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

// Repository is the aggregate persistence port for attribute values: the true
// persistence operations on the AttributeValue aggregate — point loads, the
// upsert, and the erasure primitives. The read-model queries (paginated lists,
// entity summaries, upsert/uniqueness probes) live on the application-owned
// value read port, which the same backend struct also satisfies. Writes run on
// a transaction-bound repository from WithTx.
type Repository interface {
	// WithTx returns a repository bound to the given transaction.
	WithTx(tx db.Tx) Repository

	// Get loads one value by ID (batched).
	Get(ctx context.Context, id valueobjects.AttributeValueID) (*AttributeValue, error)

	// GetForUpdate loads one value with a row lock. Only valid on a
	// transaction-bound repository.
	GetForUpdate(ctx context.Context, id valueobjects.AttributeValueID) (*AttributeValue, error)

	// Save upserts the aggregate.
	Save(ctx context.Context, av *AttributeValue) error

	// PurgeEntity HARD-deletes every stored value of one entity, including
	// already-archived rows — the right-to-erasure primitive. It returns the
	// object keys of any media values removed so the caller can garbage-collect
	// the backing blobs, and the number of rows deleted. Only valid on a
	// transaction-bound repository.
	PurgeEntity(ctx context.Context, key EntityKey) (purgedMediaKeys []string, count int, err error)

	// PurgeTenant HARD-deletes every stored value of a tenant (all types,
	// entities and archived rows), returning purged media object keys and the
	// row count. Only valid on a transaction-bound repository.
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (purgedMediaKeys []string, count int, err error)

	// MediaKeyAttributes returns the distinct attribute definitions of every
	// value row in the tenant, live or archived, that references the object
	// key. An empty result means the tenant holds no such value.
	//
	// The media download handler needs both facts this returns. Ownership
	// comes first: media object keys are fresh per-upload ULIDs in a shared
	// blob namespace, so streaming a key without confirming the tenant owns it
	// is a cross-tenant file read (IDOR). The attribute identity comes second:
	// a blob is only reachable through an attribute the principal may read, so
	// the download path applies the same field ACL the value reads apply.
	// Archived rows count for both: their blobs may still exist and remain the
	// owning tenant's.
	MediaKeyAttributes(ctx context.Context, tenant valueobjects.TenantID, objectKey string) ([]valueobjects.AttributeDefinitionID, error)
}
