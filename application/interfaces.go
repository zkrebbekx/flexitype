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
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
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
	domainattribute "github.com/zkrebbekx/flexitype/domain/attribute"
	domaindependency "github.com/zkrebbekx/flexitype/domain/dependency"
	domainrelationship "github.com/zkrebbekx/flexitype/domain/relationship"
	domaintypedef "github.com/zkrebbekx/flexitype/domain/typedef"
	domainvalue "github.com/zkrebbekx/flexitype/domain/value"
	"github.com/zkrebbekx/flexitype/pkg/db"
)

// Repositories is one request-scoped set of domain repositories. A fresh
// set means fresh dataloader caches, so nothing leaks across requests or
// tenants.
type Repositories struct {
	TypeDefinitions         typedefRepository
	Attributes              attributeRepository
	Values                  valueRepository
	Dependencies            dependencyRepository
	RelationshipDefinitions relationshipDefinitionRepository
	Relationships           relationshipRepository
	Query                   appquery.Repository
}

// Narrow aliases keep the struct readable without re-importing domain
// packages at every call site.
type (
	typedefRepository                = domaintypedef.Repository
	attributeRepository              = domainattribute.Repository
	valueRepository                  = domainvalue.Repository
	dependencyRepository             = domaindependency.Repository
	relationshipDefinitionRepository = domainrelationship.DefinitionRepository
	relationshipRepository           = domainrelationship.Repository
)

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
	features      Features
}

// Features reports the deployment's enabled capabilities.
func (i *Interactors) Features() Features { return i.features }

// TypeDefinitions returns the type-definition usecases.
func (i *Interactors) TypeDefinitions() *apptypedef.Interactor { return i.typeDefs }

// Attributes returns the attribute-definition usecases.
func (i *Interactors) Attributes() *appattribute.Interactor { return i.attrs }

// Values returns the attribute-value usecases.
func (i *Interactors) Values() *appvalue.Interactor { return i.values }

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
