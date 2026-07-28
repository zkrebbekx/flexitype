// Package value holds the attribute-value usecases, including the Set flow
// that validates values against the definition, its constraints and every
// matched attribute dependency before writing.
package value

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	"github.com/zkrebbekx/flexitype/application/fieldacl"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/ulid"
)

// maxBatchItems caps one batch write so a single request can't hold a
// transaction open unboundedly.
const maxBatchItems = 1000

// Interactor implements the attribute-value usecases.
type Interactor struct {
	uow      uow.UnitOfWork
	typeDefs domaintypedef.Repository
	attrs    domainattribute.Repository
	values   domainvalue.Repository
	// reads is the value read-model port: the paginated lists and projection
	// loads the usecases serve outside a write transaction. It is the same
	// backend struct as values; the in-transaction reads (uniqueness counts,
	// upsert lookups) instead go through values.WithTx(tx) bound to the unit of
	// work, so they observe its uncommitted writes.
	reads appctx.ValueReader
	deps  domaindependency.Repository
	links domainrelationship.Repository
	blobs blobStore
	units unitStore
	now   func() time.Time
	// onCleanupError surfaces a swallowed media-GC cleanup failure (a blob that
	// could not be deleted after an archived, overwritten or rejected-upload
	// value). Nil-safe: wired from the factory like the other observers.
	onCleanupError func(error)
}

// observeCleanup reports a swallowed cleanup failure to the configured
// observer. Nil-safe: without an observer the media-GC failure is simply
// dropped (best effort), so a delete hiccup never fails the surrounding write.
func (i *Interactor) observeCleanup(err error) {
	if i.onCleanupError != nil {
		i.onCleanupError(err)
	}
}

// unitStore resolves the unit family a quantity attribute pins, for
// converting a magnitude to its base unit. Nil disables quantity writes.
type unitStore interface {
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (appunit.Family, error)
}

// blobStore is the subset of the object-storage port the value interactor
// needs for media uploads and archival cleanup. Nil disables media.
type blobStore interface {
	Put(ctx context.Context, key string, r io.Reader, mime string) error
	Delete(ctx context.Context, key string) error
}

// Config carries the value interactor's optional collaborators. Passing them at
// construction (instead of post-construction setters) makes NewInteractor total:
// a constructed interactor is always fully wired, never a representable
// half-wired state. Every field is optional and nil-safe.
type Config struct {
	// Blobs is the object store backing media attributes; nil disables media.
	Blobs blobStore
	// UnitFamilies backs quantity attributes; nil disables quantity writes.
	UnitFamilies unitStore
	// OnCleanupError surfaces a swallowed media-GC cleanup failure. Nil-safe.
	OnCleanupError func(error)
}

// NewInteractor wires the attribute-value usecases. Required collaborators are
// positional; optional ones arrive through cfg so the returned interactor is
// always fully wired.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, values domainvalue.Repository, reads appctx.ValueReader, deps domaindependency.Repository, links domainrelationship.Repository, cfg Config) *Interactor {
	return &Interactor{
		uow:            u,
		typeDefs:       typeDefs,
		attrs:          attrs,
		values:         values,
		reads:          reads,
		deps:           deps,
		links:          links,
		blobs:          cfg.Blobs,
		units:          cfg.UnitFamilies,
		onCleanupError: cfg.OnCleanupError,
		now:            uow.UTCNow,
	}
}

// SetInput holds data for writing one attribute value. Value is the raw
// JSON scalar, decoded against the attribute's data type.
type SetInput struct {
	AttributeDefinitionID string
	EntityID              string
	// TypeDefinitionID is the entity's declared type. Optional: it defaults
	// to the attribute's declaring type, and must be that type or one of
	// its descendants (inherited attributes anchor to the subtype).
	TypeDefinitionID string
	// Locale and Channel scope the value. Allowed only when the attribute
	// is localizable / scopable respectively; the value identity is
	// (entity, attribute, locale, channel).
	Locale  string
	Channel string
	Value   json.RawMessage
	// Internal marks a write from the computed-attribute materializer,
	// which is the only writer allowed to set a read-only computed value.
	Internal bool
	// fromUpload marks the write that UploadMedia performs after storing the
	// bytes. It is unexported deliberately: an API caller cannot set it, so
	// every media write from outside this package must name an object key the
	// tenant already owns, and inherits that key's stored metadata.
	fromUpload bool
}

// Set writes a value for an entity attribute: it locks the definition,
// decodes and validates the value (type, constraints, dependencies,
// uniqueness), then inserts a new value or updates the existing one for
// single-valued attributes.
func (i *Interactor) Set(ctx context.Context, in SetInput) (*domainvalue.Snapshot, error) {
	var snap domainvalue.Snapshot
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		s, err := i.setWithin(ctx, tx, c, in)
		if err != nil {
			return err
		}
		snap = s
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// setWithin performs one value write inside an existing unit of work,
// collecting its events and activity into c. Set and SetBatch share it so a
// batch runs every write in one transaction with identical validation.
func (i *Interactor) setWithin(ctx context.Context, tx db.Transactor, c *uow.Collector, in SetInput) (domainvalue.Snapshot, error) {
	var snap domainvalue.Snapshot
	defID, err := valueobjects.ParseAttributeDefinitionID(in.AttributeDefinitionID)
	if err != nil {
		return snap, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(in.EntityID)
	if err != nil {
		return snap, domainerrors.NewValidation(err.Error())
	}
	if len(in.Value) == 0 || string(in.Value) == "null" {
		return snap, domainerrors.NewValidation("value is required")
	}

	err = func() error {
		attrs := i.attrs.WithTx(tx)
		values := i.values.WithTx(tx)
		reads := values.(appctx.ValueReader) // read port bound to the same tx

		// Lock the definition: value validity depends on it, so definition
		// updates and value writes serialize. During an import the same
		// definition is locked once per chunk transaction and reused across its
		// cells (see importCache), instead of re-locking it for every value.
		def, err := i.lockDefinition(ctx, attrs, defID)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, def.TenantID(), "attribute_definition", in.AttributeDefinitionID); err != nil {
			return err
		}

		// Resolve the entity's declared type and prove the attribute is in
		// its inherited schema.
		//
		// The anchor is an INVARIANT of the entity, not an input to each
		// write. An entity that already holds values keeps the type it was
		// first written under; a caller naming a different one is rejected
		// rather than silently splitting the entity in two.
		//
		// It used to default to the attribute's DECLARING type, so writing an
		// inherited attribute (declared on the parent) and an own attribute
		// (declared on the subtype) produced rows under two anchors. Reads,
		// dependency source loading, completeness scoring, revision capture,
		// facets, the grid and PurgeEntity are all keyed on the anchor, so
		// each saw only half the entity — and PurgeEntity, the right-to-
		// erasure primitive, reported success while permanently missing the
		// rows under the other anchor.
		anchor, anchored, err := values.EntityAnchor(ctx, uow.TenantFromContext(ctx), entityID)
		if err != nil {
			return err
		}
		entityType := def.TypeDefinitionID()
		if anchored {
			entityType = anchor
		}
		if in.TypeDefinitionID != "" {
			supplied, perr := valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID)
			if perr != nil {
				return domainerrors.NewValidation(perr.Error())
			}
			if anchored && !supplied.Equals(anchor) {
				// One anchor change is legitimate: NARROWING. A client that
				// writes an inherited attribute before it names the subtype
				// anchors the entity to the declaring parent, and naming the
				// subtype afterwards should narrow the entity rather than
				// fail. The rows already written move with it, or the entity
				// is split — which is the whole defect. Anything else is a
				// conflict, reported rather than resolved silently.
				typeDefs := i.typeDefs.WithTx(tx)
				suppliedType, terr := typeDefs.Get(ctx, supplied)
				if terr != nil {
					return terr
				}
				narrowing, terr := apptypedef.IsAncestorOrSelf(ctx, typeDefs, suppliedType, anchor)
				if terr != nil {
					return terr
				}
				if !narrowing {
					return domainerrors.NewValidation(
						"the entity is already anchored to an unrelated type",
						"entity", in.EntityID, "anchor", anchor.String(), "supplied", supplied.String())
				}
				if _, rerr := values.ReanchorEntity(ctx, uow.TenantFromContext(ctx), entityID, supplied); rerr != nil {
					return rerr
				}
			}
			entityType = supplied
		}
		if !entityType.Equals(def.TypeDefinitionID()) {
			typeDefs := i.typeDefs.WithTx(tx)
			declared, terr := typeDefs.Get(ctx, entityType)
			if terr != nil {
				return terr
			}
			ok, terr := apptypedef.IsAncestorOrSelf(ctx, typeDefs, declared, def.TypeDefinitionID())
			if terr != nil {
				return terr
			}
			if !ok {
				return domainerrors.NewValidation(
					"the attribute is not part of the entity type's inherited schema",
					"attribute", def.InternalName(), "entity_type", entityType.String())
			}
		}

		// An archived type accepts no new data. Archiving was a pure soft
		// delete with no write-path guard, while the FQL binder excludes
		// archived types from query scope — so a write under an archived type
		// succeeded and the data was then unqueryable, invisible to the very
		// surface an operator would use to find it.
		//
		// The materializer is exempt: it may clear or rewrite a derived value
		// on a type being wound down, and blocking it would strand stale
		// computed values that the archive is meant to retire.
		if !in.Internal {
			typeDefs := i.typeDefs.WithTx(tx)
			entityTypeDef, terr := typeDefs.Get(ctx, entityType)
			if terr != nil {
				return terr
			}
			if entityTypeDef.IsArchived() {
				return domainerrors.NewArchived("type_definition", entityType.String())
			}
		}

		// Computed attributes are read-only: only the materializer (Internal)
		// may write their derived value.
		if def.IsComputed() && !in.Internal {
			return domainerrors.NewValidation("attribute is computed (read-only)", "attribute", def.InternalName())
		}
		// Field-level access control: the principal must be permitted to
		// write this attribute (the materializer writes as the system).
		if !in.Internal && !uow.AccessFromContext(ctx).CanWrite(def.InternalName()) {
			return domainerrors.NewForbidden("not permitted to write this attribute", "attribute", def.InternalName())
		}

		var v valueobjects.Value
		if def.DataType() == valueobjects.DataTypeMedia && !in.fromUpload {
			// A media value is a reference to stored bytes, so a write that did
			// not come from the upload path may only point at an object key
			// this tenant already owns — and it inherits that key's stored
			// metadata rather than declaring its own.
			//
			// Accepting raw media metadata here was a cross-tenant read and a
			// cross-tenant delete. Download authorization asks "does any value
			// row in my tenant reference this key", so a caller who learned
			// another tenant's key — they leak into revision payloads, CSV
			// exports and URLs — could mint that row themselves and then stream
			// the file, or remove the value and have GC delete the other
			// tenant's blob. The declared MIME type and size were also
			// attacker-chosen, so the media constraint's allowlist and the
			// upload path's content sniffing were both bypassable.
			if v, err = i.adoptMediaValue(ctx, values, in.Value); err != nil {
				return err
			}
		} else if def.DataType() == valueobjects.DataTypeQuantity {
			// Quantities convert to the family's base unit; a unit outside the
			// family is rejected (mixing families).
			if v, err = i.quantityValue(ctx, def, in.Value); err != nil {
				return err
			}
		} else if v, err = valueobjects.ParseValue(def.DataType(), in.Value); err != nil {
			return domainerrors.NewValidation(err.Error())
		}

		// Scope is allowed only along the dimensions the attribute enables.
		scope := valueobjects.Scope{Locale: in.Locale, Channel: in.Channel}
		if scope.Locale != "" && !def.Localizable() {
			return domainerrors.NewValidation("attribute is not localizable", "attribute", def.InternalName())
		}
		if scope.Channel != "" && !def.Scopable() {
			return domainerrors.NewValidation("attribute is not scopable", "attribute", def.InternalName())
		}

		if err := i.checkDependencies(ctx, tx, def, entityType, entityID, v); err != nil {
			return err
		}
		if def.Unique() {
			// This is a read followed by a write, which would admit two
			// concurrent writers of the same value under READ COMMITTED. It is
			// safe only because lockDefinition above took a row lock on the
			// attribute definition, in this same transaction, before we got
			// here: every writer of this attribute serializes on that row, so
			// the second writer's count observes the first writer's committed
			// value. Removing or weakening that lock reintroduces the race, and
			// the duplicate it admits is permanent — the check only ever asks
			// whether a NEW value collides, so no later write surfaces it.
			// TestConcurrencyInvariantsPostgres pins the resulting invariant.
			//
			// Uniqueness applies per scope: the same value may exist in a
			// different locale/channel.
			count, err := reads.CountByDefinitionAndValue(ctx, defID, scope, v, entityID)
			if err != nil {
				return fmt.Errorf("check uniqueness: %w", err)
			}
			if count > 0 {
				return domainerrors.NewConflict("value already used by another entity",
					"attribute", def.InternalName(), "value", v.String())
			}
		}

		all, err := i.existingValues(ctx, reads, defID, entityID)
		if err != nil {
			return fmt.Errorf("load existing values: %w", err)
		}
		// Values are scoped: only those in the same (locale, channel) share
		// this write's identity, so an entity holds one value per scope.
		var existing []*domainvalue.AttributeValue
		for _, av := range all {
			if av.Scope().Equals(scope) {
				existing = append(existing, av)
			}
		}

		// Single-valued attributes upsert; multi-valued attributes append
		// unless the exact value is already present.
		if !def.MultiValued() && len(existing) > 0 {
			av := existing[0]
			before := av.Snapshot()
			evts, err := av.UpdateValue(def, v, i.now())
			if err != nil {
				return err
			}
			snap = av.Snapshot()
			if len(evts) == 0 {
				return nil
			}
			if err := values.Save(ctx, av); err != nil {
				return fmt.Errorf("save attribute value: %w", err)
			}
			// A media overwrite replaces the object key in place; GC the blob
			// the old value pointed at (when it actually changed).
			if before.Value.DataType() == valueobjects.DataTypeMedia &&
				before.Value.Media().ObjectKey != snap.Value.Media().ObjectKey {
				i.gcMediaAfterCommit(tx, before.ID, before.Value)
			}
			c.CollectEvents(evts...)
			c.RecordChange(activity.Change{
				Entity:   domainvalue.AggregateType,
				EntityID: av.ID().String(),
				Action:   activity.ActionUpdated,
				Before:   before,
				After:    snap,
			})
			return nil
		}
		for _, av := range existing {
			if av.Value().Equal(v) {
				snap = av.Snapshot()
				return nil
			}
		}

		av, evts, err := domainvalue.New(def, entityType, entityID, scope, v, i.now())
		if err != nil {
			return err
		}
		if err := values.Save(ctx, av); err != nil {
			return fmt.Errorf("save attribute value: %w", err)
		}
		// Keep the import prefetch cache in step with this insert, so a later
		// row in the same chunk touching the same entity sees the value just
		// written (upsert/append decisions stay correct without a re-read).
		i.recordImportedValue(ctx, entityID, av)

		snap = av.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: av.ID().String(),
			Action:   activity.ActionCreated,
			After:    snap,
		})
		return nil
	}()
	return snap, err
}

// batchItemError points at which batch item failed while preserving the
// underlying error (and its domain code) via Unwrap, so the HTTP layer maps
// the status correctly.
type batchItemError struct {
	index int
	err   error
}

func (e *batchItemError) Error() string { return fmt.Sprintf("item %d: %s", e.index, e.err.Error()) }
func (e *batchItemError) Unwrap() error { return e.err }

// BatchSetInput sets several values in one transaction.
type BatchSetInput struct {
	Items []SetInput
}

// BatchSetOutput returns the written snapshots in input order.
type BatchSetOutput struct {
	Items []domainvalue.Snapshot
}

// SetBatch writes many values atomically: either every item is applied and
// its events fire, or the whole batch rolls back. The failing item's error
// (and its domain code) is preserved so callers get the real reason.
func (i *Interactor) SetBatch(ctx context.Context, in BatchSetInput) (*BatchSetOutput, error) {
	if len(in.Items) == 0 {
		return nil, domainerrors.NewValidation("at least one item is required")
	}
	if len(in.Items) > maxBatchItems {
		return nil, domainerrors.NewValidation("batch exceeds the maximum item count", "max", maxBatchItems)
	}

	// Apply in the canonical entity order every multi-entity write uses, so
	// two transactions touching the same entities take the entity-summary
	// rows in the same sequence. See lockorder.go.
	order := canonicalOrder(in.Items, func(it SetInput) string { return it.EntityID })

	out := &BatchSetOutput{Items: make([]domainvalue.Snapshot, len(in.Items))}
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		for _, idx := range order {
			s, err := i.setWithin(ctx, tx, c, in.Items[idx])
			if err != nil {
				return &batchItemError{index: idx, err: err}
			}
			out.Items[idx] = s
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// RemoveEntityOutput reports what an entity removal cascaded.
type RemoveEntityOutput struct {
	EntityID          string
	ValuesRemoved     int
	RelationshipsGone int
}

// RemoveEntity archives every live value of an entity and unlinks every
// live relationship touching it, in one unit of work with one event stream.
// An entity with no values and no links is reported NotFound.
func (i *Interactor) RemoveEntity(ctx context.Context, rawTypeDefID, rawEntityID string) (*RemoveEntityOutput, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	out := &RemoveEntityOutput{EntityID: rawEntityID}
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		out.ValuesRemoved, out.RelationshipsGone = 0, 0
		values := i.values.WithTx(tx)
		reads := values.(appctx.ValueReader) // read port bound to the same tx
		links := i.links.WithTx(tx)

		vals, err := reads.ListByEntity(ctx, domainvalue.EntityKey{
			TenantID: tenant, TypeDefinitionID: typeDefID, EntityID: entityID,
		})
		if err != nil {
			return fmt.Errorf("list entity values: %w", err)
		}
		rels, err := links.ListByEntity(ctx, domainrelationship.EntityLinksKey{
			TenantID: tenant, EntityID: entityID,
		})
		if err != nil {
			return fmt.Errorf("list entity links: %w", err)
		}
		if len(vals) == 0 && len(rels) == 0 {
			return domainerrors.NewNotFound("entity", rawEntityID)
		}

		// A cascade delete removes every value of the entity, so it needs write
		// permission on every attribute it touches. Without this check a
		// principal barred from writing one attribute could still erase that
		// attribute's values by removing the whole entity — the single-value
		// Remove and removeScopedWithin paths already enforce it.
		if err := i.assertWritableValues(ctx, vals); err != nil {
			return err
		}

		for _, av := range vals {
			before := av.Snapshot()
			evts, err := av.Remove(i.now())
			if err != nil {
				return err
			}
			if err := values.Save(ctx, av); err != nil {
				return fmt.Errorf("archive value: %w", err)
			}
			i.gcMediaAfterCommit(tx, before.ID, before.Value)
			c.CollectEvents(evts...)
			c.RecordChange(activity.Change{
				Entity:   domainvalue.AggregateType,
				EntityID: av.ID().String(),
				Action:   activity.ActionRemoved,
				Before:   before,
			})
			out.ValuesRemoved++
		}

		for _, rel := range rels {
			before := rel.Snapshot()
			evts, err := rel.Unlink(i.now())
			if err != nil {
				return err
			}
			if err := links.Save(ctx, rel); err != nil {
				return fmt.Errorf("unlink relationship: %w", err)
			}
			c.CollectEvents(evts...)
			c.RecordChange(activity.Change{
				Entity:   domainrelationship.AggregateType,
				EntityID: rel.ID().String(),
				Action:   activity.ActionRemoved,
				Before:   before,
			})
			out.RelationshipsGone++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// checkDependencies resolves the effective schema for the target attribute
// given the entity's current source values and validates v against it.
func (i *Interactor) checkDependencies(
	ctx context.Context,
	tx db.Transactor,
	def *domainattribute.Definition,
	entityType valueobjects.TypeDefinitionID,
	entityID valueobjects.EntityID,
	v valueobjects.Value,
) error {
	deps := i.deps.WithTx(tx)
	reads := i.values.WithTx(tx).(appctx.ValueReader) // read port bound to the same tx

	targeting, err := i.targetingDependencies(ctx, deps, def.ID())
	if err != nil {
		return fmt.Errorf("load dependencies: %w", err)
	}
	if len(targeting) == 0 {
		return nil
	}

	entityValues, err := reads.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         def.TenantID(),
		TypeDefinitionID: entityType,
		EntityID:         entityID,
	})
	if err != nil {
		return fmt.Errorf("load entity values: %w", err)
	}
	sourceValues := make(map[valueobjects.AttributeDefinitionID][]valueobjects.Value, len(entityValues))
	for _, av := range entityValues {
		sourceValues[av.AttributeDefinitionID()] = append(sourceValues[av.AttributeDefinitionID()], av.Value())
	}

	// The SAME resolver the read paths use, with the caller's context values
	// and the tenant-local day.
	//
	// This path used to call the context-free form: ctxValues was nil, so a
	// condition naming a ContextKey short-circuited to "no match" and the rule
	// never applied — while EffectiveSchema and Completeness, which do pass
	// them, reported the restriction. A write was accepted that the API had
	// just described as forbidden, which is the worst combination for a
	// validation feature: it looks configured and tested. The clock was UTC
	// here and tenant-local there, so a `today` rule near midnight disagreed
	// with itself for several hours a day.
	schema, err := domaindependency.ResolveEffectiveWithContext(
		def, targeting, sourceValues, uow.ContextValuesFromContext(ctx), uow.LocalNow(ctx))
	if err != nil {
		return fmt.Errorf("resolve effective schema: %w", err)
	}
	return schema.Check(v)
}

// lockDefinition loads and row-locks an attribute definition. During an import
// (an importCache on the context) the same definition is locked once per chunk
// transaction and the locked aggregate reused for every cell, so a chunk of
// rows sharing a column no longer re-locks that definition once per row.
// Definitions are only read in the write path, so sharing one aggregate is safe.
func (i *Interactor) lockDefinition(ctx context.Context, attrs domainattribute.Repository, defID valueobjects.AttributeDefinitionID) (*domainattribute.Definition, error) {
	c := importCacheFromContext(ctx)
	if c == nil {
		return attrs.GetForUpdate(ctx, defID)
	}
	if def, ok := c.defs[defID.String()]; ok {
		return def, nil
	}
	def, err := attrs.GetForUpdate(ctx, defID)
	if err != nil {
		return nil, err
	}
	c.defs[defID.String()] = def
	return def, nil
}

// targetingDependencies loads the dependencies whose effect applies to an
// attribute. Definitions and dependencies do not change during an import, so
// the result is memoized on the importCache and fetched at most once per target.
func (i *Interactor) targetingDependencies(ctx context.Context, deps domaindependency.Repository, targetID valueobjects.AttributeDefinitionID) ([]*domaindependency.Dependency, error) {
	c := importCacheFromContext(ctx)
	if c == nil {
		return deps.ListByTarget(ctx, targetID)
	}
	if d, ok := c.deps[targetID.String()]; ok {
		return d, nil
	}
	d, err := deps.ListByTarget(ctx, targetID)
	if err != nil {
		return nil, err
	}
	c.deps[targetID.String()] = d
	return d, nil
}

// existingValues returns the live values one entity holds for a definition.
// During an import it reads the prefetched-and-maintained per-entity value set
// (one ListByEntities per chunk) instead of a FindByDefinitionAndEntity query
// per cell.
func (i *Interactor) existingValues(ctx context.Context, reads appctx.ValueReader, defID valueobjects.AttributeDefinitionID, entityID valueobjects.EntityID) ([]*domainvalue.AttributeValue, error) {
	c := importCacheFromContext(ctx)
	if c == nil || c.existing == nil {
		return reads.FindByDefinitionAndEntity(ctx, defID, entityID)
	}
	var out []*domainvalue.AttributeValue
	for _, av := range c.existing[entityID.String()] {
		if av.AttributeDefinitionID().Equals(defID) {
			out = append(out, av)
		}
	}
	return out, nil
}

// recordImportedValue appends a value written during an import to the prefetch
// cache so subsequent cells of the same entity in the same chunk observe it. It
// is a no-op outside an import.
func (i *Interactor) recordImportedValue(ctx context.Context, entityID valueobjects.EntityID, av *domainvalue.AttributeValue) {
	if c := importCacheFromContext(ctx); c != nil && c.existing != nil {
		key := entityID.String()
		c.existing[key] = append(c.existing[key], av)
	}
}

// Remove archives a stored value.
func (i *Interactor) Remove(ctx context.Context, rawID string) (*domainvalue.Snapshot, error) {
	id, err := valueobjects.ParseAttributeValueID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	var snap domainvalue.Snapshot
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		values := i.values.WithTx(tx)

		av, err := values.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, av.TenantID(), domainvalue.AggregateType, rawID); err != nil {
			return err
		}
		// Field-level ACL: removing a value is a write; a principal that may not
		// write the attribute may not delete its value either.
		def, err := i.attrs.WithTx(tx).Get(ctx, av.AttributeDefinitionID())
		if err != nil {
			return err
		}
		if !uow.AccessFromContext(ctx).CanWrite(def.InternalName()) {
			return domainerrors.NewForbidden("not permitted to write this attribute", "attribute", def.InternalName())
		}
		before := av.Snapshot()

		evts, err := av.Remove(i.now())
		if err != nil {
			return err
		}
		if err := values.Save(ctx, av); err != nil {
			return fmt.Errorf("save attribute value: %w", err)
		}
		// Register the blob GC on the transaction, like every other archival
		// path. Removing it deleted the bytes unconditionally after the call
		// returned, so a key another value still referenced lost its bytes —
		// and a rolled-back removal deleted them anyway.
		i.gcMediaAfterCommit(tx, before.ID, before.Value)

		snap = av.Snapshot()
		c.CollectEvents(evts...)
		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: av.ID().String(),
			Action:   activity.ActionRemoved,
			Before:   before,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &snap, nil
}

// adoptMediaValue resolves a caller-supplied media reference to the value the
// tenant already stores for that object key. It rejects a key the tenant does
// not own OR the caller may not read, and discards the caller's metadata in
// favour of the stored copy.
//
// The READ CHECK is what stops a laundering attack. Ownership alone was a
// tenant check, while the download check granted the bytes if ANY referencing
// attribute was readable — so a principal restricted from `passport_scan`
// needed only write access to some other media attribute: adopt the
// restricted key into `avatar`, then download it. Object keys are not secret;
// the code's own comments note they leak into value payloads, exports and
// revision snapshots, so nothing had to be guessed.
//
// The bytes belong to the attribute that first referenced them, and that
// attribute governs both adoption and download.
func (i *Interactor) adoptMediaValue(ctx context.Context, values domainvalue.Repository, raw json.RawMessage) (valueobjects.Value, error) {
	declared, err := valueobjects.ParseValue(valueobjects.DataTypeMedia, raw)
	if err != nil {
		return valueobjects.Value{}, domainerrors.NewValidation(err.Error())
	}
	key := declared.Media().ObjectKey
	if key == "" {
		return valueobjects.Value{}, domainerrors.NewValidation("media value requires an object key")
	}
	unknownKey := domainerrors.NewValidation(
		"unknown media object key; upload the file through the media endpoint", "object_key", key)

	stored, ok, err := values.MediaValueForKey(ctx, uow.TenantFromContext(ctx), key)
	if err != nil {
		return valueobjects.Value{}, err
	}
	if !ok {
		// The same message whether the key belongs to another tenant or to
		// nobody, so ownership is not probeable from the error.
		return valueobjects.Value{}, unknownKey
	}
	readable, err := fieldacl.New(i.attrs).CanRead(ctx, stored.AttributeDefinitionID)
	if err != nil {
		return valueobjects.Value{}, err
	}
	if !readable {
		// Same error as an unknown key: a caller that may not read the owning
		// attribute learns nothing about whether the key exists.
		return valueobjects.Value{}, unknownKey
	}
	return stored.Value, nil
}

// gcMediaAfterCommit schedules the blob backing an archived or overwritten
// media value for deletion once the surrounding transaction commits (best
// effort — a storage error never fails the write, but is surfaced to the
// cleanup observer). Registering on the transaction keeps GC correct across
// every archival path — overwrite, entity removal, mutation apply and snapshot
// restore — not just single-value Remove.
//
// The delete is reference-counted: a key another value row still references
// keeps its bytes. Without the count, two values sharing an object key meant
// removing either one deleted the blob out from under the other.
func (i *Interactor) gcMediaAfterCommit(tx db.Transactor, valueID valueobjects.AttributeValueID, v valueobjects.Value) {
	if i.blobs == nil || v.DataType() != valueobjects.DataTypeMedia {
		return
	}
	key := v.Media().ObjectKey
	if key == "" {
		return
	}
	tx.OnPostCommit(func(ctx context.Context) error {
		refs, err := i.values.MediaKeyRefCount(ctx, key, valueID)
		if err != nil {
			i.observeCleanup(fmt.Errorf("count media references for %s: %w", key, err))
			return nil // never fail a committed write on a GC bookkeeping error
		}
		if refs > 0 {
			return nil // another value still points at these bytes
		}
		if err := i.blobs.Delete(ctx, key); err != nil {
			i.observeCleanup(fmt.Errorf("gc media blob %s: %w", key, err))
		}
		return nil
	})
}

// Get loads one stored value by ID.
func (i *Interactor) Get(ctx context.Context, rawID string) (*domainvalue.Snapshot, error) {
	id, err := valueobjects.ParseAttributeValueID(rawID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	av, err := i.values.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := uow.EnsureTenant(ctx, av.TenantID(), domainvalue.AggregateType, rawID); err != nil {
		return nil, err
	}
	// Field-level ACL: an unreadable attribute's value is invisible, not leaked
	// — the same contract ListByEntity/facets/the FQL binder enforce.
	def, err := i.attrs.Get(ctx, av.AttributeDefinitionID())
	if err != nil {
		return nil, err
	}
	if !uow.AccessFromContext(ctx).CanRead(def.InternalName()) {
		return nil, domainerrors.NewNotFound(domainvalue.AggregateType, rawID)
	}
	snap := av.Snapshot()
	return &snap, nil
}

// MediaKeyReadable reports whether the caller may download the blob behind
// objectKey. The media download handler calls it before streaming.
//
// Two conditions must hold. The caller's tenant must own a value referencing
// the key — object keys are shared-namespace ULIDs that leak into value
// payloads, exports and revision snapshots, so serving one without an
// ownership check is a cross-tenant file read (IDOR). And the caller must be
// able to read the OWNING attribute: the one whose value first referenced the
// key.
//
// Owning, not "any referencing attribute". Granting the download when any
// attribute referencing the key was readable meant a key could be laundered
// into readability by adopting it into a writable attribute. Adoption now
// requires read access on the owning attribute, which closes the way in; this
// closes the way out, so a row written before that rule cannot be used
// either.
//
// A caller who obtains a key for an attribute it may not read gets the same
// NotFound as for a key that does not exist.
func (i *Interactor) MediaKeyReadable(ctx context.Context, objectKey string) (bool, error) {
	owner, ok, err := i.values.MediaValueForKey(ctx, uow.TenantFromContext(ctx), objectKey)
	if err != nil || !ok {
		return false, err
	}
	return fieldacl.New(i.attrs).CanRead(ctx, owner.AttributeDefinitionID)
}

// ListByEntity loads every live value of one entity — the hydration hot
// path; concurrent calls for different entities batch into one query.
func (i *Interactor) ListByEntity(ctx context.Context, rawTypeDefID, rawEntityID string) ([]domainvalue.Snapshot, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	items, err := i.reads.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         uow.TenantFromContext(ctx),
		TypeDefinitionID: typeDefID,
		EntityID:         entityID,
	})
	if err != nil {
		return nil, err
	}
	snaps := make([]domainvalue.Snapshot, 0, len(items))
	for _, av := range items {
		snaps = append(snaps, av.Snapshot())
	}
	return i.redactUnreadable(ctx, snaps)
}

// ListByEntities loads every live value held by any of the given entities in
// one query, with field-level access control applied. It powers batched
// projections such as the GraphQL resolver, where fanning out per entity
// would be an N+1.
func (i *Interactor) ListByEntities(ctx context.Context, rawEntityIDs []string) ([]domainvalue.Snapshot, error) {
	ids := make([]valueobjects.EntityID, 0, len(rawEntityIDs))
	for _, raw := range rawEntityIDs {
		id, err := valueobjects.ParseEntityID(raw)
		if err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
		ids = append(ids, id)
	}
	items, err := i.reads.ListByEntities(ctx, uow.TenantFromContext(ctx), ids)
	if err != nil {
		return nil, err
	}
	snaps := make([]domainvalue.Snapshot, 0, len(items))
	for _, av := range items {
		snaps = append(snaps, av.Snapshot())
	}
	return i.redactUnreadable(ctx, snaps)
}

// assertWritableValues rejects the operation unless the principal may write
// every attribute the values belong to. It reports the first offending
// attribute by internal name so the caller can see which permission is
// missing.
func (i *Interactor) assertWritableValues(ctx context.Context, vals []*domainvalue.AttributeValue) error {
	access := uow.AccessFromContext(ctx)
	if access.Admin || len(vals) == 0 {
		return nil
	}
	acl := fieldacl.New(i.attrs)
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(vals))
	for _, av := range vals {
		ids = append(ids, av.AttributeDefinitionID())
	}
	writable, err := acl.Writable(ctx, ids)
	if err != nil {
		return err
	}
	names, err := acl.Names(ctx, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if writable[id.String()] {
			continue
		}
		name := names[id.String()]
		if name == "" {
			name = id.String()
		}
		return domainerrors.NewForbidden("attribute " + name + " is not writable by this principal")
	}
	return nil
}

// redactUnreadable drops values of attributes the principal may not read.
// Admins (and unauthenticated development) keep everything.
func (i *Interactor) redactUnreadable(ctx context.Context, snaps []domainvalue.Snapshot) ([]domainvalue.Snapshot, error) {
	if uow.AccessFromContext(ctx).Admin {
		return snaps, nil
	}
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(snaps))
	for _, s := range snaps {
		ids = append(ids, s.AttributeDefinitionID)
	}
	// An attribute the repository cannot resolve is unreadable, not readable:
	// resolving is what proves the policy permits it. The shared resolver
	// applies that rule everywhere, so no surface can drift into treating an
	// unresolvable attribute as unrestricted.
	readable, err := fieldacl.New(i.attrs).Readable(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := snaps[:0]
	for _, s := range snaps {
		if readable[s.AttributeDefinitionID.String()] {
			out = append(out, s)
		}
	}
	return out, nil
}

// EntitySummaryOutput is one entity-browser row.
type EntitySummaryOutput struct {
	EntityID         string    `json:"entity_id"`
	TypeDefinitionID string    `json:"type_definition_id"`
	ValueCount       int       `json:"value_count"`
	LastUpdatedAt    time.Time `json:"last_updated_at"`
}

// EntityListOutput is one page of the entity browser.
type EntityListOutput struct {
	Items    []EntitySummaryOutput
	PageInfo db.PageInfo
}

// ListEntitiesStable is ListEntities with the sweep ordering: every existing
// row exactly once, in id order, rather than newest-first. Use it for a full
// pass — a reindex, an export, a completeness score, a recompute — where a
// missed row is a defect and presentation order does not matter.
func (i *Interactor) ListEntitiesStable(ctx context.Context, rawTypeDefID string, includeDescendants bool, args db.PageArgs) (*EntityListOutput, error) {
	return i.listEntities(ctx, rawTypeDefID, includeDescendants, args, true)
}

// ListEntities pages the distinct entities holding live values of a type
// definition — the observability entry point for the admin console, ordered
// newest-first. With includeDescendants, entities of every subtype are
// included and each row carries its declared type.
//
// The ordering key is mutable, so a page is stable against inserts and
// deletes but not against a write to a row the caller has not reached yet.
// Use ListEntitiesStable for a full sweep.
func (i *Interactor) ListEntities(ctx context.Context, rawTypeDefID string, includeDescendants bool, args db.PageArgs) (*EntityListOutput, error) {
	return i.listEntities(ctx, rawTypeDefID, includeDescendants, args, false)
}

func (i *Interactor) listEntities(ctx context.Context, rawTypeDefID string, includeDescendants bool, args db.PageArgs, stable bool) (*EntityListOutput, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	page, err := args.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	page.Stable = stable

	typeIDs := []valueobjects.TypeDefinitionID{typeDefID}
	if includeDescendants {
		t, err := i.typeDefs.Get(ctx, typeDefID)
		if err != nil {
			return nil, err
		}
		descendants, err := apptypedef.Descendants(ctx, i.typeDefs, t)
		if err != nil {
			return nil, err
		}
		for _, d := range descendants {
			typeIDs = append(typeIDs, d.ID())
		}
	}

	items, total, err := i.reads.ListEntities(ctx, uow.TenantFromContext(ctx), typeIDs, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(e domainvalue.EntitySummary) string {
		if page.Stable {
			// A sweep pages on the immutable key, so its cursor carries only
			// that. Mixing the two shapes would decode as the wrong ordering.
			return db.EncodeKeyset(e.EntityID.String())
		}
		return db.EncodeKeyset(db.KeysetTime(e.LastUpdatedAt), e.EntityID.String())
	})
	out := &EntityListOutput{
		Items:    make([]EntitySummaryOutput, 0, len(items)),
		PageInfo: info,
	}
	for _, e := range items {
		out.Items = append(out.Items, EntitySummaryOutput{
			EntityID:         e.EntityID.String(),
			TypeDefinitionID: e.TypeDefinitionID.String(),
			ValueCount:       e.ValueCount,
			LastUpdatedAt:    e.LastUpdatedAt,
		})
	}
	return out, nil
}

// ListInput holds filter and pagination arguments for List.
type ListInput struct {
	TypeDefinitionID      string
	AttributeDefinitionID string
	EntityID              string
	IncludeArchived       bool
	Page                  db.PageArgs
}

// ListOutput is one page of stored values.
type ListOutput struct {
	Items    []domainvalue.Snapshot
	PageInfo db.PageInfo
}

// List returns a filtered, paginated set of stored values.
func (i *Interactor) List(ctx context.Context, in ListInput) (*ListOutput, error) {
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

	filter := domainvalue.Filter{
		TenantID:        uow.TenantFromContext(ctx),
		IncludeArchived: in.IncludeArchived,
	}
	if in.TypeDefinitionID != "" {
		if filter.TypeDefinitionID, err = valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.AttributeDefinitionID != "" {
		if filter.AttributeDefinitionID, err = valueobjects.ParseAttributeDefinitionID(in.AttributeDefinitionID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}
	if in.EntityID != "" {
		if filter.EntityID, err = valueobjects.ParseEntityID(in.EntityID); err != nil {
			return nil, domainerrors.NewValidation(err.Error())
		}
	}

	items, total, err := i.reads.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(av *domainvalue.AttributeValue) string {
		return db.EncodeKeyset(av.ID().String())
	})
	snaps := make([]domainvalue.Snapshot, 0, len(items))
	for _, av := range items {
		snaps = append(snaps, av.Snapshot())
	}
	// The field ACL applies here exactly as it does on every other value read
	// surface. The caller chooses attribute_definition_id and type_definition_id
	// as query parameters, so without this an unreadable attribute's values are
	// listable tenant-wide by asking for them directly.
	snaps, err = i.redactUnreadable(ctx, snaps)
	if err != nil {
		return nil, err
	}
	return &ListOutput{Items: snaps, PageInfo: info}, nil
}
