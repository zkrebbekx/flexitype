// Package application wires flexitype's usecases behind a request-scoped
// factory. The factory builds fresh dataloader-backed repositories per
// request and a unit of work carrying the standard pre-commit (activity
// log), post-commit (event dispatch) and rollback commit handlers.
package application

import (
	"context"
	"time"

	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apperasure "github.com/zkrebbekx/flexitype/application/erasure"
	appfeed "github.com/zkrebbekx/flexitype/application/feed"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	appsavedview "github.com/zkrebbekx/flexitype/application/savedview"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	appwebhook "github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Repositories is one request-scoped set of domain repositories. It is defined
// in the appctx leaf so the search subpackage can consume it without importing
// this composition root; re-exported here for the facade wiring.
type Repositories = appctx.Repositories

// SchemaVersionReader reports a tenant's persisted schema version (issue #192).
// It lives in the appctx leaf; re-exported here for the facade wiring.
type SchemaVersionReader = appctx.SchemaVersionReader

// ValueReader is the application-owned read-model port over stored attribute
// values. It lives in the appctx leaf; re-exported here for the facade wiring.
type ValueReader = appctx.ValueReader

// Factory creates request-scoped interactor sets.
type Factory interface {
	New(ctx context.Context) *Interactors
}

type interactorsKey struct{}

// WithInteractors stows a request-scoped interactor set on the context.
// The service's HTTP middleware calls this once per request so every
// handler shares one dataloader generation.
func WithInteractors(ctx context.Context, i *Interactors) context.Context {
	return context.WithValue(ctx, interactorsKey{}, i)
}

// FromContext returns the request's interactor set. It panics when the
// middleware did not run — a wiring error, not a runtime condition.
func FromContext(ctx context.Context) *Interactors {
	i, ok := ctx.Value(interactorsKey{}).(*Interactors)
	if !ok {
		panic("application: no interactors on context; is the middleware installed?")
	}
	return i
}

// Interactors groups every usecase for one request.
type Interactors struct {
	typeDefs      *apptypedef.Interactor
	attrs         *appattribute.Interactor
	values        *appvalue.Interactor
	erasure       *apperasure.Interactor
	deps          *appdependency.Interactor
	relationships *apprelationship.Interactor
	query         *appquery.Interactor
	activity      *ActivityInteractor
	webhooks      *appwebhook.Interactor
	feed          *appfeed.Interactor
	schema        *appschema.Interactor
	savedViews    *appsavedview.Interactor
	dedup         *appdedup.Interactor
	revisions     *apprevision.Interactor
	changesets    *appchangeset.Interactor
	units         *appunit.Interactor
	schemaVersion SchemaVersionReader
	features      Features
}

// Features reports the deployment's enabled capabilities.
func (i *Interactors) Features() Features { return i.features }

// SchemaVersion returns the tenant's persisted schema version — the counter the
// GraphQL engine reads to detect a cross-replica definition change (issue #192).
// It returns 0 when no reader is configured (a repository set built without
// one).
func (i *Interactors) SchemaVersion(ctx context.Context) (uint64, error) {
	if i.schemaVersion == nil {
		return 0, nil
	}
	return i.schemaVersion.SchemaVersion(ctx)
}

// TypeDefinitions returns the type-definition usecases.
func (i *Interactors) TypeDefinitions() *apptypedef.Interactor { return i.typeDefs }

// Attributes returns the attribute-definition usecases.
func (i *Interactors) Attributes() *appattribute.Interactor { return i.attrs }

// Values returns the attribute-value usecases.
func (i *Interactors) Values() *appvalue.Interactor { return i.values }

// Erasure returns the right-to-erasure usecases: the irreversible, audited
// hard delete of an entity's or a tenant's data (PurgeEntity / PurgeTenant).
func (i *Interactors) Erasure() *apperasure.Interactor { return i.erasure }

// Dependencies returns the dependency usecases.
func (i *Interactors) Dependencies() *appdependency.Interactor { return i.deps }

// Relationships returns the relationship usecases.
func (i *Interactors) Relationships() *apprelationship.Interactor { return i.relationships }

// Query returns the FQL usecases.
func (i *Interactors) Query() *appquery.Interactor { return i.query }

// Activity returns the audit-log read usecases.
func (i *Interactors) Activity() *ActivityInteractor { return i.activity }

// Webhooks returns the webhook-subscription usecases; nil unless event
// delivery is enabled (requires the outbox).
func (i *Interactors) Webhooks() *appwebhook.Interactor { return i.webhooks }

// Feed returns the events-feed usecases; nil unless event delivery is
// enabled (requires the outbox).
func (i *Interactors) Feed() *appfeed.Interactor { return i.feed }

// Schema returns the schema import/export usecases.
func (i *Interactors) Schema() *appschema.Interactor { return i.schema }

// SavedViews returns the saved-view usecases; nil if the feature is off.
func (i *Interactors) SavedViews() *appsavedview.Interactor { return i.savedViews }

// Dedup returns the duplicate-detection usecases; nil unless a match store
// is configured.
func (i *Interactors) Dedup() *appdedup.Interactor { return i.dedup }

// Revisions returns the entity-revision usecases; nil unless a revision
// store is configured.
func (i *Interactors) Revisions() *apprevision.Interactor { return i.revisions }

// ChangeSets returns the change-management usecases; nil unless a
// change-set store is configured.
func (i *Interactors) ChangeSets() *appchangeset.Interactor { return i.changesets }

// Units returns the unit-family usecases; nil unless a unit store is
// configured.
func (i *Interactors) Units() *appunit.Interactor { return i.units }

// ActivityInteractor serves the audit-log read side.
type ActivityInteractor struct {
	log activity.Log
}

// ActivityListInput holds filter and pagination arguments.
type ActivityListInput struct {
	Entity   string
	EntityID string
	Actor    string
	Page     db.PageArgs
}

// ActivityListOutput is one page of audit entries.
type ActivityListOutput struct {
	Items    []activity.Entry
	PageInfo db.PageInfo
}

// List returns a filtered, paginated slice of the activity log.
func (a *ActivityInteractor) List(ctx context.Context, in ActivityListInput) (*ActivityListOutput, error) {
	if a.log == nil {
		return nil, domainerrors.NewValidation("the activity log is disabled in this deployment")
	}
	page, err := in.Page.Resolve()
	if err != nil {
		return nil, err
	}
	items, total, err := a.log.List(ctx, activity.Filter{
		TenantID: uow.TenantFromContext(ctx),
		Entity:   in.Entity,
		EntityID: in.EntityID,
		Actor:    in.Actor,
	}, page)
	if err != nil {
		return nil, err
	}
	items, info := db.KeysetPage(page, items, db.KeysetTotal(page, total), func(e activity.Entry) string {
		return db.EncodeKeyset(e.OccurredAt.UTC().Format(time.RFC3339Nano), e.ID.String())
	})
	return &ActivityListOutput{
		Items:    items,
		PageInfo: info,
	}, nil
}
