// Package erasure owns the right-to-erasure orchestration: the irreversible,
// audited hard delete of an entity's or a tenant's data across attribute
// values, revisions, relationships, the search projection and media blobs. It
// is deliberately separate from the value write usecases — erasure is a
// distinct, cross-store concern with its own atomicity policy (the revision
// purge joins the value transaction; the search-projection removal and media
// blob GC run post-commit with observation) and its own honest PurgeReport that
// never claims a false success. Every collaborator arrives through the
// constructor, so a constructed Interactor is always fully wired.
package erasure

import (
	"context"
	"fmt"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	"github.com/zkrebbekx/flexitype/application/uow"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// blobStore is the delete-only slice of the object-storage port the erasure
// needs to garbage-collect the blobs behind purged media values. Nil disables
// blob GC (the report then lists no media, which is honest: nothing was
// deleted).
type blobStore interface {
	Delete(ctx context.Context, key string) error
}

// Config carries the erasure orchestrator's collaborators. Every field is set
// once at construction; the optional ones (Revisions, Search, Blobs,
// OnCleanupError) may be nil when their feature is off, and the erasure degrades
// honestly rather than nil-derefing.
type Config struct {
	// UnitOfWork runs the atomic value/relationship/revision deletes and the
	// audit entry as one transaction, with the post-commit cleanup hooks.
	UnitOfWork uow.UnitOfWork
	// Values hard-deletes attribute values (returning purged media keys) and is
	// tx-bindable via WithTx.
	Values domainvalue.Repository
	// Links hard-deletes relationship links, tx-bindable via WithTx.
	Links domainrelationship.Repository
	// Revisions is the revision store an erasure purges; nil when the revision
	// feature is off. Transaction-bindable (WithTx) so the revision purge joins
	// the value write's atomic unit of work rather than hard-deleting on a
	// non-tx-bound store that a value-tx rollback could not undo.
	Revisions apprevision.Store
	// Search is the search projection an erasure purges; nil when the search
	// index is off. The port is the canonical appctx.SearchStore.
	Search appctx.SearchStore
	// Blobs backs media-blob GC; nil disables it.
	Blobs blobStore
	// OnCleanupError surfaces a swallowed post-erasure cleanup failure (a blob GC
	// or search-projection removal that could not be completed). Nil-safe.
	OnCleanupError func(error)
}

// Interactor owns the entity- and tenant-level erasure usecases.
type Interactor struct {
	uow       uow.UnitOfWork
	values    domainvalue.Repository
	links     domainrelationship.Repository
	revisions apprevision.Store
	search    appctx.SearchStore
	blobs     blobStore
	// onCleanupError surfaces a swallowed post-erasure cleanup failure (a blob
	// GC or search-projection removal that could not be completed). Nil-safe.
	onCleanupError func(error)
}

// NewInteractor wires the erasure usecases from a total config, so a
// constructed interactor is never half-wired.
func NewInteractor(cfg Config) *Interactor {
	return &Interactor{
		uow:            cfg.UnitOfWork,
		values:         cfg.Values,
		links:          cfg.Links,
		revisions:      cfg.Revisions,
		search:         cfg.Search,
		blobs:          cfg.Blobs,
		onCleanupError: cfg.OnCleanupError,
	}
}

// observeCleanup reports a swallowed cleanup failure to the configured
// observer. Nil-safe: without an observer the failure is still reflected in the
// PurgeReport (MediaBlobsFailed / UnpurgedBlobKeys), so an erasure never
// reports a false success.
func (i *Interactor) observeCleanup(err error) {
	if i.onCleanupError != nil {
		i.onCleanupError(err)
	}
}

// PurgeReport counts what an erasure permanently removed. It is the audited
// receipt of a hard delete, so it reports only CONFIRMED deletions: a media
// blob is counted in MediaBlobsPurged only once storage acknowledges its
// deletion. Blobs whose deletion failed are counted in MediaBlobsFailed and
// their keys listed in UnpurgedBlobKeys, so a right-to-erasure caller sees the
// residual data honestly rather than a false success.
type PurgeReport struct {
	// EntityID is set for a per-entity purge; empty for a tenant purge.
	EntityID          string `json:"entity_id,omitempty"`
	ValuesPurged      int    `json:"values_purged"`
	RevisionsPurged   int    `json:"revisions_purged"`
	RelationshipsGone int    `json:"relationships_gone"`
	SearchDocsPurged  int    `json:"search_docs_purged"`
	// MediaBlobsPurged counts blobs storage confirmed deleted (post-commit).
	MediaBlobsPurged int `json:"media_blobs_purged"`
	// MediaBlobsFailed counts blobs whose deletion failed; UnpurgedBlobKeys
	// lists their still-orphaned object keys for operator reconciliation.
	MediaBlobsFailed int      `json:"media_blobs_failed"`
	UnpurgedBlobKeys []string `json:"unpurged_blob_keys,omitempty"`
}

// PurgeEntity permanently ERASES one entity: it hard-deletes every attribute
// value (already-archived rows included), every revision, and every
// relationship link the entity participates in, then garbage-collects the
// backing blobs of any media values. This is irreversible — unlike
// RemoveEntity, which only archives — and the erasure itself is audited. An
// entity with nothing to erase is reported NotFound.
//
// The value deletes, the relationship deletes, the REVISION purge and the audit
// entry are one atomic unit of work: the revision store joins the transaction
// via WithTx, so a value-tx rollback also un-does the revision purge and never
// hard-deletes an entity's audit trail behind a failed erasure. The search
// projection (a derived, self-healing index maintained by post-commit event
// subscribers everywhere else) and the media-blob GC run POST-COMMIT with
// observation — a storage hiccup must not undo a committed erasure — and any
// failure is surfaced (search: to the cleanup observer; blobs: in the report's
// MediaBlobsFailed / UnpurgedBlobKeys and the observer), never silently
// swallowed. The deletes are idempotent under retry.
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
			// Join the value transaction: a rollback un-does this purge too, so a
			// failed erasure never leaves revisions hard-deleted with no audit.
			n, err := i.revisions.WithTx(tx).PurgeEntity(ctx, tenant, typeDefID.String(), entityID.String())
			if err != nil {
				return fmt.Errorf("purge entity revisions: %w", err)
			}
			report.RevisionsPurged = n
		}

		// An entity with no values, links or revisions never existed here.
		if report.ValuesPurged == 0 && report.RelationshipsGone == 0 && report.RevisionsPurged == 0 {
			return domainerrors.NewNotFound("entity", rawEntityID)
		}

		// Drop the search document post-commit with observation (same policy as
		// PurgeTenant): the projection is derived and self-healing, so a removal
		// failure must not undo the committed erasure, but it is surfaced to the
		// cleanup observer rather than swallowed.
		i.removeSearchDocAfterCommit(tx, tenant, entityID)

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

		i.gcErasedBlobs(tx, mediaKeys, report)
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
			// Join the value transaction (as in PurgeEntity): a rollback un-does
			// the revision purge, keeping the audit trail atomic with the erasure.
			n, err := i.revisions.WithTx(tx).PurgeTenant(ctx, tenant)
			if err != nil {
				return fmt.Errorf("purge tenant revisions: %w", err)
			}
			report.RevisionsPurged = n
		}
		// Purge the search projection post-commit with observation — one policy
		// shared with PurgeEntity (previously this path failed the erasure on a
		// projection error while PurgeEntity swallowed it). The count lands in the
		// report before Execute returns, since post-commit hooks run inside Commit.
		i.purgeTenantSearchAfterCommit(tx, tenant, report)

		c.RecordChange(activity.Change{
			Entity:   "tenant",
			EntityID: tenant.String(),
			Action:   activity.ActionPurged,
			Before: map[string]any{
				"tenant_id":          tenant.String(),
				"values_purged":      report.ValuesPurged,
				"revisions_purged":   report.RevisionsPurged,
				"relationships_gone": report.RelationshipsGone,
			},
		})

		i.gcErasedBlobs(tx, mediaKeys, report)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return report, nil
}

// gcErasedBlobs deletes the backing blobs of purged media values once the
// erasure transaction commits, recording the true outcome on the report. Blob
// GC is post-commit best effort — a storage error must not undo a committed
// erasure — but the report does not lie: it counts only blobs storage confirmed
// deleted (MediaBlobsPurged), tallies the failures (MediaBlobsFailed) and lists
// the still-orphaned keys (UnpurgedBlobKeys) so an operator can reconcile them.
// Post-commit hooks run inside Commit, so these counts are set before the
// erasure call returns. Each failure is also surfaced to the cleanup observer.
func (i *Interactor) gcErasedBlobs(tx db.Transactor, keys []string, report *PurgeReport) {
	if i.blobs == nil || len(keys) == 0 {
		return
	}
	keys = append([]string(nil), keys...) // capture: the caller may reuse the slice
	tx.OnPostCommit(func(ctx context.Context) error {
		for _, key := range keys {
			if key == "" {
				continue
			}
			if err := i.blobs.Delete(ctx, key); err != nil {
				report.MediaBlobsFailed++
				report.UnpurgedBlobKeys = append(report.UnpurgedBlobKeys, key)
				i.observeCleanup(fmt.Errorf("purge media blob %s: %w", key, err))
				continue
			}
			report.MediaBlobsPurged++
		}
		return nil
	})
}

// removeSearchDocAfterCommit drops one entity's search document once the erasure
// commits (post-commit with observation): the search index is a derived,
// self-healing projection, so a removal failure is observed, never fatal to the
// committed erasure.
func (i *Interactor) removeSearchDocAfterCommit(tx db.Transactor, tenant valueobjects.TenantID, entityID valueobjects.EntityID) {
	if i.search == nil {
		return
	}
	tx.OnPostCommit(func(ctx context.Context) error {
		if err := i.search.Remove(ctx, tenant, entityID); err != nil {
			i.observeCleanup(fmt.Errorf("purge search document for entity %s: %w", entityID, err))
		}
		return nil
	})
}

// purgeTenantSearchAfterCommit drops a tenant's search documents once the
// erasure commits, recording the confirmed count on the report and observing a
// failure — the same post-commit-with-observation policy as PurgeEntity.
func (i *Interactor) purgeTenantSearchAfterCommit(tx db.Transactor, tenant valueobjects.TenantID, report *PurgeReport) {
	if i.search == nil {
		return
	}
	tx.OnPostCommit(func(ctx context.Context) error {
		n, err := i.search.PurgeTenant(ctx, tenant)
		if err != nil {
			i.observeCleanup(fmt.Errorf("purge tenant search documents: %w", err))
			return nil
		}
		report.SearchDocsPurged = n
		return nil
	})
}
