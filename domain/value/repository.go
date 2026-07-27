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

	// EntityAnchor returns the type definition an entity's values are already
	// anchored to, and whether the entity has any values at all. Archived rows
	// count: an entity whose values were all removed keeps its anchor, so
	// re-adding a value does not silently move it.
	//
	// The anchor is an invariant of the entity, not an input to each write.
	// It was caller-supplied per write and defaulted to the attribute's
	// DECLARING type, so writing an inherited attribute and an own attribute
	// produced rows under two different anchors — and reads, dependency
	// evaluation, completeness, revision capture and PurgeEntity are all keyed
	// on it, so each saw only part of the entity.
	EntityAnchor(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) (valueobjects.TypeDefinitionID, bool, error)

	// ReanchorEntity moves every value row of one entity, archived rows
	// included, onto a new type anchor, returning the number moved. Only
	// valid on a transaction-bound repository.
	//
	// It exists for the one legitimate anchor change: narrowing. A client that
	// writes an inherited attribute before it names the subtype anchors the
	// entity to the declaring parent; naming the subtype afterwards should
	// narrow the entity rather than fail, and the rows already written have to
	// come with it or the entity is split — which is the defect the anchor
	// invariant exists to prevent.
	ReanchorEntity(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID, to valueobjects.TypeDefinitionID) (int, error)

	// MediaValueForKey returns the media value the tenant already stores for an
	// object key, and whether one exists. Archived rows count.
	//
	// It is how a media write that did not come from the upload path is
	// validated: the caller supplies an object key, and the metadata that
	// travels with it — MIME type, size, checksum — is taken from the stored
	// value rather than from the caller. Trusting caller-supplied metadata
	// meant the media constraint's type allowlist and the upload path's
	// content sniffing could both be stated away.
	MediaValueForKey(ctx context.Context, tenant valueobjects.TenantID, objectKey string) (valueobjects.Value, bool, error)

	// MediaKeyRefCount counts the value rows, in any tenant and including
	// archived ones, that reference an object key other than excludeValueID.
	//
	// Blob GC consults it before deleting: a key referenced by another row must
	// not have its bytes removed out from under that row.
	MediaKeyRefCount(ctx context.Context, objectKey string, excludeValueID valueobjects.AttributeValueID) (int, error)

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

	// AttributeDataShape reports what stored data would be orphaned or
	// invalidated by a structural change to an attribute definition.
	//
	// The structural flags — multi-valued, unique, localizable, scopable,
	// computed — used to be replaced with no check against data already
	// written, and each flip left rows the new schema cannot express or
	// reach. It counts live rows only: archived rows are already unreachable
	// and are not the caller's problem.
	AttributeDataShape(ctx context.Context, tenant valueobjects.TenantID, attrID valueobjects.AttributeDefinitionID) (DataShape, error)
}

// DataShape summarises the stored values of one attribute, for the checks a
// structural schema change has to pass.
type DataShape struct {
	// LiveValues counts unarchived rows.
	LiveValues int
	// EntitiesWithMany counts entities holding more than one live row —
	// exactly the rows that a multi-valued→single flip would strand, because
	// a single-valued write updates the first row and never reaches the rest.
	EntitiesWithMany int
	// ScopedValues counts live rows carrying a locale or a channel. Turning
	// localizable or scopable off leaves those rows readable but unwritable:
	// scope is rejected on write, so they can never be updated or removed
	// through the API again.
	ScopedValues int
	// DuplicateValues counts live rows sharing a value with another entity's
	// row — what a false→true unique flip would leave behind, since no
	// backfill check runs and new writers of those same values are refused
	// while the existing duplicates persist.
	DuplicateValues int
}
