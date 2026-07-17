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
	uow       uow.UnitOfWork
	typeDefs  domaintypedef.Repository
	attrs     domainattribute.Repository
	values    domainvalue.Repository
	deps      domaindependency.Repository
	links     domainrelationship.Repository
	blobs     blobStore
	units     unitStore
	revisions revisionPurger
	search    SearchStore
	now       func() time.Time
}

// revisionPurger hard-deletes an entity's or a tenant's revisions during an
// erasure. Nil disables revision purging (the revision feature is off). It is
// the erasure-facing slice of the revision store; kept local so the value
// package does not import the revision package.
type revisionPurger interface {
	PurgeEntity(ctx context.Context, tenant valueobjects.TenantID, typeDefID, entityID string) (int, error)
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error)
}

// SetRevisionStore installs the revision store an erasure purges. Called once
// at wiring time when the revision feature is enabled.
func (i *Interactor) SetRevisionStore(s revisionPurger) { i.revisions = s }

// SearchStore is the erasure-facing slice of the search-projection port: an
// entity's document is dropped with Remove, a tenant's with PurgeTenant. It is
// declared here (rather than imported from the search package) because the
// search package depends on the application root, which depends on this
// package — importing it would cycle. Nil disables search purging (the index
// is off).
type SearchStore interface {
	Remove(ctx context.Context, tenant valueobjects.TenantID, entityID valueobjects.EntityID) error
	PurgeTenant(ctx context.Context, tenant valueobjects.TenantID) (int, error)
}

// SetSearchStore installs the search projection an erasure purges. Called once
// at wiring time when the search index is enabled.
func (i *Interactor) SetSearchStore(s SearchStore) { i.search = s }

// unitStore resolves the unit family a quantity attribute pins, for
// converting a magnitude to its base unit. Nil disables quantity writes.
type unitStore interface {
	Get(ctx context.Context, tenant valueobjects.TenantID, id ulid.ID) (appunit.Family, error)
}

// SetUnitFamilies installs the unit-family store backing quantity
// attributes. Called once at wiring time.
func (i *Interactor) SetUnitFamilies(s unitStore) { i.units = s }

// blobStore is the subset of the object-storage port the value interactor
// needs for media uploads and archival cleanup. Nil disables media.
type blobStore interface {
	Put(ctx context.Context, key string, r io.Reader, mime string) error
	Delete(ctx context.Context, key string) error
}

// SetBlobStore installs the object store backing media attributes. Called
// once at wiring time when a blob store is configured.
func (i *Interactor) SetBlobStore(s blobStore) { i.blobs = s }

// NewInteractor wires the attribute-value usecases.
func NewInteractor(u uow.UnitOfWork, typeDefs domaintypedef.Repository, attrs domainattribute.Repository, values domainvalue.Repository, deps domaindependency.Repository, links domainrelationship.Repository) *Interactor {
	return &Interactor{uow: u, typeDefs: typeDefs, attrs: attrs, values: values, deps: deps, links: links, now: time.Now}
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

		// Lock the definition: value validity depends on it, so definition
		// updates and value writes serialize.
		def, err := attrs.GetForUpdate(ctx, defID)
		if err != nil {
			return err
		}
		if err := uow.EnsureTenant(ctx, def.TenantID(), "attribute_definition", in.AttributeDefinitionID); err != nil {
			return err
		}

		// Resolve the entity's declared type and prove the attribute is in
		// its inherited schema.
		entityType := def.TypeDefinitionID()
		if in.TypeDefinitionID != "" {
			if entityType, err = valueobjects.ParseTypeDefinitionID(in.TypeDefinitionID); err != nil {
				return domainerrors.NewValidation(err.Error())
			}
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
		if def.DataType() == valueobjects.DataTypeQuantity {
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
			// Uniqueness applies per scope: the same value may exist in a
			// different locale/channel.
			count, err := values.CountByDefinitionAndValue(ctx, defID, scope, v, entityID)
			if err != nil {
				return fmt.Errorf("check uniqueness: %w", err)
			}
			if count > 0 {
				return domainerrors.NewConflict("value already used by another entity",
					"attribute", def.InternalName(), "value", v.String())
			}
		}

		all, err := values.FindByDefinitionAndEntity(ctx, defID, entityID)
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
				i.gcMediaAfterCommit(tx, before.Value)
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

	out := &BatchSetOutput{Items: make([]domainvalue.Snapshot, 0, len(in.Items))}
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		out.Items = out.Items[:0]
		for idx, item := range in.Items {
			s, err := i.setWithin(ctx, tx, c, item)
			if err != nil {
				return &batchItemError{index: idx, err: err}
			}
			out.Items = append(out.Items, s)
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
		links := i.links.WithTx(tx)

		vals, err := values.ListByEntity(ctx, domainvalue.EntityKey{
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

		for _, av := range vals {
			before := av.Snapshot()
			evts, err := av.Remove(i.now())
			if err != nil {
				return err
			}
			if err := values.Save(ctx, av); err != nil {
				return fmt.Errorf("archive value: %w", err)
			}
			i.gcMediaAfterCommit(tx, before.Value)
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

// PurgeReport counts what an erasure permanently removed. It is the audited
// receipt of a hard delete.
type PurgeReport struct {
	// EntityID is set for a per-entity purge; empty for a tenant purge.
	EntityID          string `json:"entity_id,omitempty"`
	ValuesPurged      int    `json:"values_purged"`
	RevisionsPurged   int    `json:"revisions_purged"`
	RelationshipsGone int    `json:"relationships_gone"`
	SearchDocsPurged  int    `json:"search_docs_purged"`
	MediaBlobsPurged  int    `json:"media_blobs_purged"`
}

// PurgeEntity permanently ERASES one entity: it hard-deletes every attribute
// value (already-archived rows included), every revision, and every
// relationship link the entity participates in, then garbage-collects the
// backing blobs of any media values. This is irreversible — unlike
// RemoveEntity, which only archives — and the erasure itself is audited. An
// entity with nothing to erase is reported NotFound.
//
// The value and relationship deletes plus the audit entry are one atomic unit
// of work. The revision and search projections are maintained outside the
// value write transaction in this architecture (revisions are created
// non-transactionally; the search index is an event-driven projection), so
// their purge runs on their own stores within the same call; on the happy path
// every trace is removed, and the deletes are idempotent under retry.
func (i *Interactor) PurgeEntity(ctx context.Context, rawTypeDefID, rawEntityID string) (*PurgeReport, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	entityID, err := valueobjects.ParseEntityID(rawEntityID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	tenant := uow.TenantFromContext(ctx)

	report := &PurgeReport{EntityID: rawEntityID}
	err = i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		*report = PurgeReport{EntityID: rawEntityID}
		values := i.values.WithTx(tx)
		links := i.links.WithTx(tx)

		mediaKeys, vcount, err := values.PurgeEntity(ctx, domainvalue.EntityKey{
			TenantID: tenant, TypeDefinitionID: typeDefID, EntityID: entityID,
		})
		if err != nil {
			return fmt.Errorf("purge entity values: %w", err)
		}
		report.ValuesPurged = vcount

		rcount, err := links.PurgeEntity(ctx, tenant, entityID)
		if err != nil {
			return fmt.Errorf("purge entity relationships: %w", err)
		}
		report.RelationshipsGone = rcount

		if i.revisions != nil {
			n, err := i.revisions.PurgeEntity(ctx, tenant, typeDefID.String(), entityID.String())
			if err != nil {
				return fmt.Errorf("purge entity revisions: %w", err)
			}
			report.RevisionsPurged = n
		}

		// An entity with no values, links or revisions never existed here.
		if report.ValuesPurged == 0 && report.RelationshipsGone == 0 && report.RevisionsPurged == 0 {
			return domainerrors.NewNotFound("entity", rawEntityID)
		}

		// Drop the search document (best effort: the projection is derived and
		// self-heals, and a storage hiccup must not fail the erasure).
		if i.search != nil {
			_ = i.search.Remove(ctx, tenant, entityID)
		}

		c.RecordChange(activity.Change{
			Entity:   domainvalue.AggregateType,
			EntityID: rawEntityID,
			Action:   activity.ActionPurged,
			Before: map[string]any{
				"entity_id":          rawEntityID,
				"type_definition_id": rawTypeDefID,
				"values_purged":      report.ValuesPurged,
				"revisions_purged":   report.RevisionsPurged,
				"relationships_gone": report.RelationshipsGone,
			},
		})

		i.gcBlobsAfterCommit(tx, mediaKeys)
		report.MediaBlobsPurged = len(mediaKeys)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// PurgeTenant permanently ERASES a tenant's entity DATA: it hard-deletes every
// attribute value, revision, relationship link and search document of the
// tenant taken from the context, and garbage-collects the backing media blobs.
//
// It is deliberately scoped to entity data. It does NOT remove the tenant's
// type/attribute DEFINITIONS, unit families, saved views, change-sets, dedup
// rules, webhook subscriptions or the activity log — the schema and control
// plane survive so the tenant can keep operating (or be decommissioned
// separately). The erasure itself is audited.
func (i *Interactor) PurgeTenant(ctx context.Context) (*PurgeReport, error) {
	tenant := uow.TenantFromContext(ctx)
	if tenant.String() == "" {
		return nil, domainerrors.NewValidation("a tenant is required to purge tenant data")
	}

	report := &PurgeReport{}
	err := i.uow.Execute(ctx, func(tx db.Transactor, c *uow.Collector) error {
		*report = PurgeReport{}
		values := i.values.WithTx(tx)
		links := i.links.WithTx(tx)

		mediaKeys, vcount, err := values.PurgeTenant(ctx, tenant)
		if err != nil {
			return fmt.Errorf("purge tenant values: %w", err)
		}
		report.ValuesPurged = vcount

		rcount, err := links.PurgeTenant(ctx, tenant)
		if err != nil {
			return fmt.Errorf("purge tenant relationships: %w", err)
		}
		report.RelationshipsGone = rcount

		if i.revisions != nil {
			n, err := i.revisions.PurgeTenant(ctx, tenant)
			if err != nil {
				return fmt.Errorf("purge tenant revisions: %w", err)
			}
			report.RevisionsPurged = n
		}
		if i.search != nil {
			n, err := i.search.PurgeTenant(ctx, tenant)
			if err != nil {
				return fmt.Errorf("purge tenant search documents: %w", err)
			}
			report.SearchDocsPurged = n
		}

		c.RecordChange(activity.Change{
			Entity:   "tenant",
			EntityID: tenant.String(),
			Action:   activity.ActionPurged,
			Before: map[string]any{
				"tenant_id":          tenant.String(),
				"values_purged":      report.ValuesPurged,
				"revisions_purged":   report.RevisionsPurged,
				"relationships_gone": report.RelationshipsGone,
				"search_docs_purged": report.SearchDocsPurged,
			},
		})

		i.gcBlobsAfterCommit(tx, mediaKeys)
		report.MediaBlobsPurged = len(mediaKeys)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// gcBlobsAfterCommit schedules the backing blobs of purged media values for
// deletion once the surrounding transaction commits (best effort — a storage
// error never fails the erasure; a later sweep can reconcile), mirroring
// gcMediaAfterCommit for the multi-key purge path.
func (i *Interactor) gcBlobsAfterCommit(tx db.Transactor, keys []string) {
	if i.blobs == nil || len(keys) == 0 {
		return
	}
	keys = append([]string(nil), keys...) // capture: the caller may reuse the slice
	tx.OnPostCommit(func(ctx context.Context) error {
		for _, key := range keys {
			if key != "" {
				_ = i.blobs.Delete(ctx, key)
			}
		}
		return nil
	})
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
	values := i.values.WithTx(tx)

	targeting, err := deps.ListByTarget(ctx, def.ID())
	if err != nil {
		return fmt.Errorf("load dependencies: %w", err)
	}
	if len(targeting) == 0 {
		return nil
	}

	entityValues, err := values.ListByEntity(ctx, domainvalue.EntityKey{
		TenantID:         def.TenantID(),
		TypeDefinitionID: entityType,
		EntityID:         entityID,
	})
	if err != nil {
		return fmt.Errorf("load entity values: %w", err)
	}
	sourceValues := make(map[valueobjects.AttributeDefinitionID]valueobjects.Value, len(entityValues))
	for _, av := range entityValues {
		sourceValues[av.AttributeDefinitionID()] = av.Value()
	}

	schema, err := domaindependency.ResolveEffective(def, targeting, sourceValues, i.now())
	if err != nil {
		return fmt.Errorf("resolve effective schema: %w", err)
	}
	return schema.Check(v)
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
	// Deletion lifecycle: once the archival has committed, drop the backing
	// blob of a media value (best effort; storage errors don't fail the API
	// call — a later sweep can reconcile).
	i.gcMedia(ctx, snap.Value)
	return &snap, nil
}

// gcMedia removes the object backing a media value from storage. It is a
// no-op for non-media values or when no blob store is configured.
func (i *Interactor) gcMedia(ctx context.Context, v valueobjects.Value) {
	if i.blobs == nil || v.DataType() != valueobjects.DataTypeMedia {
		return
	}
	if key := v.Media().ObjectKey; key != "" {
		_ = i.blobs.Delete(ctx, key)
	}
}

// gcMediaAfterCommit schedules the blob backing an archived or overwritten
// media value for deletion once the surrounding transaction commits (best
// effort — a storage error never fails the write). Registering on the
// transaction keeps GC correct across every archival path — overwrite, entity
// removal, mutation apply and snapshot restore — not just single-value Remove.
func (i *Interactor) gcMediaAfterCommit(tx db.Transactor, v valueobjects.Value) {
	if i.blobs == nil || v.DataType() != valueobjects.DataTypeMedia {
		return
	}
	key := v.Media().ObjectKey
	if key == "" {
		return
	}
	tx.OnPostCommit(func(ctx context.Context) error {
		_ = i.blobs.Delete(ctx, key)
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

	items, err := i.values.ListByEntity(ctx, domainvalue.EntityKey{
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
	items, err := i.values.ListByEntities(ctx, uow.TenantFromContext(ctx), ids)
	if err != nil {
		return nil, err
	}
	snaps := make([]domainvalue.Snapshot, 0, len(items))
	for _, av := range items {
		snaps = append(snaps, av.Snapshot())
	}
	return i.redactUnreadable(ctx, snaps)
}

// redactUnreadable drops values of attributes the principal may not read.
// Admins (and unauthenticated development) keep everything.
func (i *Interactor) redactUnreadable(ctx context.Context, snaps []domainvalue.Snapshot) ([]domainvalue.Snapshot, error) {
	access := uow.AccessFromContext(ctx)
	if access.Admin {
		return snaps, nil
	}
	ids := make([]valueobjects.AttributeDefinitionID, 0, len(snaps))
	seen := map[string]bool{}
	for _, s := range snaps {
		if id := s.AttributeDefinitionID; !seen[id.String()] {
			seen[id.String()] = true
			ids = append(ids, id)
		}
	}
	defs, err := i.attrs.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	name := make(map[string]string, len(defs))
	for _, d := range defs {
		name[d.ID().String()] = d.InternalName()
	}
	out := snaps[:0]
	for _, s := range snaps {
		if access.CanRead(name[s.AttributeDefinitionID.String()]) {
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

// ListEntities pages the distinct entities holding live values of a type
// definition — the observability entry point for the admin console. With
// includeDescendants, entities of every subtype are included and each row
// carries its declared type.
func (i *Interactor) ListEntities(ctx context.Context, rawTypeDefID string, includeDescendants bool, args db.PageArgs) (*EntityListOutput, error) {
	typeDefID, err := valueobjects.ParseTypeDefinitionID(rawTypeDefID)
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}
	page, err := args.Resolve()
	if err != nil {
		return nil, domainerrors.NewValidation(err.Error())
	}

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

	items, total, err := i.values.ListEntities(ctx, uow.TenantFromContext(ctx), typeIDs, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(e domainvalue.EntitySummary) string {
		return db.EncodeKeyset(e.LastUpdatedAt.UTC().Format(time.RFC3339Nano), e.EntityID.String())
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

	items, total, err := i.values.List(ctx, filter, page)
	if err != nil {
		return nil, err
	}

	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(av *domainvalue.AttributeValue) string {
		return db.EncodeKeyset(av.ID().String())
	})
	out := &ListOutput{
		Items:    make([]domainvalue.Snapshot, 0, len(items)),
		PageInfo: info,
	}
	for _, av := range items {
		out.Items = append(out.Items, av.Snapshot())
	}
	return out, nil
}
