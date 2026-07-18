package application

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/zkrebbekx/flexitype/application/activity"
	"github.com/zkrebbekx/flexitype/application/appctx"
	appattribute "github.com/zkrebbekx/flexitype/application/attribute"
	appchangeset "github.com/zkrebbekx/flexitype/application/changeset"
	appdedup "github.com/zkrebbekx/flexitype/application/dedup"
	appdependency "github.com/zkrebbekx/flexitype/application/dependency"
	apperasure "github.com/zkrebbekx/flexitype/application/erasure"
	"github.com/zkrebbekx/flexitype/application/feed"
	appquery "github.com/zkrebbekx/flexitype/application/query"
	apprelationship "github.com/zkrebbekx/flexitype/application/relationship"
	apprevision "github.com/zkrebbekx/flexitype/application/revision"
	appsavedview "github.com/zkrebbekx/flexitype/application/savedview"
	appschema "github.com/zkrebbekx/flexitype/application/schema"
	apptypedef "github.com/zkrebbekx/flexitype/application/typedef"
	appunit "github.com/zkrebbekx/flexitype/application/unit"
	"github.com/zkrebbekx/flexitype/application/uow"
	appvalue "github.com/zkrebbekx/flexitype/application/value"
	"github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
)

// Features toggles optional capabilities per deployment. Zero value =
// everything enabled.
type Features struct {
	// DisableSearch turns the FQL query surface off.
	DisableSearch bool
	// DisableActivity turns the audit log off entirely: no pre-commit
	// writes, no read API.
	DisableActivity bool
	// SearchIndex enables the entity search projection and FQL matches().
	SearchIndex bool
	// EventDelivery enables webhook subscriptions and the events feed.
	// Requires the outbox (it is the durable log both are built on).
	EventDelivery bool
}

// FactoryConfig carries the factory's composition-time dependencies.
type FactoryConfig struct {
	// Transactor is the pool-level transactor usecases begin transactions
	// on.
	Transactor db.Transactor

	// NewRepositories builds one request-scoped repository set (fresh
	// dataloader caches per request).
	NewRepositories func() Repositories

	// Dispatcher fans domain events out to registered EXTERNAL client hooks
	// (pub/sub, webhooks, funcs) after commit. Under the outbox it is fed by the
	// relay; without it, it dispatches synchronously best-effort.
	Dispatcher *events.Dispatcher

	// Projections dispatches INTERNAL projection maintenance (computed
	// attributes, search index, GraphQL schema-cache invalidation) synchronously
	// in the originating unit of work's post-commit — in BOTH delivery modes, so
	// read-your-writes on computed values and matches() never depends on
	// WithOutbox (issue #211). Reserved for internal projections; external hooks
	// use Dispatcher. Nil leaves internal maintenance unwired (no projections).
	Projections *events.Dispatcher

	// ActivityLog persists audit entries inside the business transaction
	// (pre-commit) and serves the audit read API.
	ActivityLog activity.Log

	// OnRollback observes rolled-back units of work (metrics/logging).
	// Optional.
	OnRollback func(ctx context.Context, err error)

	// OnDispatchError observes a synchronous post-commit event-dispatch
	// failure (non-outbox mode). The write has already committed, so the
	// error is logged/metered here rather than failing the request. Optional.
	OnDispatchError func(ctx context.Context, err error)

	// OnCleanupError observes a swallowed post-erasure cleanup failure — a
	// media-blob GC or search-projection removal that could not be completed
	// after a committed erasure. These are best-effort by design (they must not
	// undo a durable erasure), so the failure is surfaced here for
	// logging/metering rather than lost. Optional.
	OnCleanupError func(error)

	// Now overrides the clock. Optional; defaults to uow.UTCNow. A custom clock
	// MUST return UTC wall-clock time (not a local or monotonic reading): the
	// value is stored in aggregates and compared across the app/DB boundary, so
	// a non-UTC clock can flip ordering-sensitive behavior in other timezones.
	Now func() time.Time

	// Features toggles optional capabilities.
	Features Features

	// Outbox, when set, switches event delivery to at-least-once: the unit
	// of work writes envelopes transactionally and OutboxNudge wakes the
	// relay after commit.
	Outbox      uow.EnvelopeSink
	OutboxNudge func()

	// Subscriptions/Deliveries power webhook-subscription management;
	// FeedStore/CursorStore power the events feed. All require the outbox.
	Subscriptions webhook.SubscriptionStore
	Deliveries    webhook.DeliveryStore
	FeedStore     feed.Store
	CursorStore   feed.CursorStore

	// WebhookURLPolicy governs which subscription URLs are accepted.
	WebhookURLPolicy webhook.URLPolicy

	// SavedViews persists saved entity views; nil disables the feature.
	SavedViews appsavedview.Store

	// MatchRules persists duplicate-detection rules and dismissals; nil
	// disables the feature.
	MatchRules appdedup.Store

	// Revisions persists entity revisions; nil disables the feature.
	Revisions apprevision.Store

	// SearchStore is the entity search projection an erasure purges; nil when
	// the search index is off. Typed as the canonical appctx.SearchStore
	// erasure port (search.DocumentStore satisfies it).
	SearchStore appctx.SearchStore

	// ChangeSets persists change-management drafts; nil disables the feature.
	ChangeSets appchangeset.Store

	// UnitFamilies persists unit families for quantity attributes; nil
	// disables quantity support.
	UnitFamilies appunit.Store

	// BlobStore backs media attribute values; nil disables media uploads.
	BlobStore blob.Store
}

// factory is the common usecase factory: every request gets fresh
// repositories and a unit of work with the standard commit handlers
// registered (pre-commit activity log, post-commit event dispatch,
// rollback observer).
type factory struct {
	cfg FactoryConfig
}

// NewFactory validates the config and builds the usecase factory.
func NewFactory(cfg FactoryConfig) Factory {
	if cfg.Transactor == nil {
		panic("application: FactoryConfig.Transactor is required")
	}
	if cfg.NewRepositories == nil {
		panic("application: FactoryConfig.NewRepositories is required")
	}
	if cfg.Dispatcher == nil {
		cfg.Dispatcher = events.NewDispatcher()
	}
	if cfg.ActivityLog == nil && !cfg.Features.DisableActivity {
		panic("application: FactoryConfig.ActivityLog is required unless activity is disabled")
	}
	// A feature flag whose backing stores are not wired would nil-deref at
	// request time, not boot. Fail closed here with a wiring message so the
	// mistake surfaces on startup. (The flexitype facade wires flag + stores
	// together; this guards direct FactoryConfig callers.)
	if probs := featureWiringErrors(cfg); len(probs) > 0 {
		panic("application: " + strings.Join(probs, "; "))
	}
	if cfg.Now == nil {
		cfg.Now = uow.UTCNow
	}
	return &factory{cfg: cfg}
}

// featureWiringErrors reports any feature flag enabled without the stores its
// interactors dereference. It is the single consistency check between the
// Features booleans and store presence, kept pure so it is unit-testable.
func featureWiringErrors(cfg FactoryConfig) []string {
	var problems []string
	if cfg.Features.EventDelivery {
		var missing []string
		for name, present := range map[string]bool{
			"Outbox":        cfg.Outbox != nil,
			"Subscriptions": cfg.Subscriptions != nil,
			"Deliveries":    cfg.Deliveries != nil,
			"FeedStore":     cfg.FeedStore != nil,
			"CursorStore":   cfg.CursorStore != nil,
		} {
			if !present {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			problems = append(problems,
				fmt.Sprintf("Features.EventDelivery requires the outbox and webhook/feed stores; missing: %s", strings.Join(missing, ", ")))
		}
	}
	return problems
}

// New builds the request-scoped interactor set.
func (f *factory) New(context.Context) *Interactors {
	repos := f.cfg.NewRepositories()

	opts := []uow.Option{uow.WithNow(f.cfg.Now)}
	if f.cfg.OnRollback != nil {
		opts = append(opts, uow.WithRollbackObserver(f.cfg.OnRollback))
	}
	if f.cfg.OnDispatchError != nil {
		opts = append(opts, uow.WithDispatchObserver(f.cfg.OnDispatchError))
	}
	if f.cfg.Projections != nil {
		opts = append(opts, uow.WithProjections(f.cfg.Projections))
	}
	if f.cfg.Outbox != nil {
		opts = append(opts, uow.WithOutbox(f.cfg.Outbox, f.cfg.OutboxNudge))
	}
	activityLog := f.cfg.ActivityLog
	if f.cfg.Features.DisableActivity {
		activityLog = nil // the unit of work skips audit writes entirely
	}
	unit := uow.New(f.cfg.Transactor, f.cfg.Dispatcher, activityLog, opts...)

	i := &Interactors{
		typeDefs: apptypedef.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes),
		attrs:    appattribute.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes, f.cfg.UnitFamilies),
		values: appvalue.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes, repos.Values, repos.ValueReader, repos.Dependencies, repos.Relationships, appvalue.Config{
			Blobs:          f.cfg.BlobStore,
			UnitFamilies:   f.cfg.UnitFamilies,
			OnCleanupError: f.cfg.OnCleanupError,
		}),
		// Erasure owns the cross-store right-to-erasure hard delete: the
		// revision-purge tx-join, the post-commit search-projection removal and
		// the honest media-blob GC. Nil optional stores disable the corresponding
		// step; the interactor is constructed totally, with no post-construction
		// setters to leave it half-wired.
		erasure: apperasure.NewInteractor(apperasure.Config{
			UnitOfWork:     unit,
			Values:         repos.Values,
			Links:          repos.Relationships,
			Revisions:      f.cfg.Revisions,
			Search:         f.cfg.SearchStore,
			Blobs:          f.cfg.BlobStore,
			OnCleanupError: f.cfg.OnCleanupError,
		}),
		deps:          appdependency.NewInteractor(unit, repos.TypeDefinitions, repos.Attributes, repos.ValueReader, repos.Dependencies),
		relationships: apprelationship.NewInteractor(unit, repos.TypeDefinitions, repos.RelationshipDefinitions, repos.Relationships),
		query:         appquery.NewInteractor(repos.TypeDefinitions, repos.Attributes, repos.RelationshipDefinitions, repos.Query, f.cfg.Features.SearchIndex, f.cfg.UnitFamilies),
		activity:      &ActivityInteractor{log: activityLog},
		schemaVersion: repos.SchemaVersions,
		features:      f.cfg.Features,
	}
	if f.cfg.SavedViews != nil {
		i.savedViews = appsavedview.NewInteractor(f.cfg.SavedViews)
	}
	if f.cfg.MatchRules != nil {
		i.dedup = appdedup.NewInteractor(f.cfg.MatchRules, repos.TypeDefinitions, repos.Attributes, repos.ValueReader, f.cfg.Now)
	}
	if f.cfg.Revisions != nil {
		i.revisions = apprevision.NewInteractor(f.cfg.Revisions, repos.TypeDefinitions, repos.Attributes, repos.ValueReader, i.values, f.cfg.Now)
	}
	if f.cfg.ChangeSets != nil {
		i.changesets = appchangeset.NewInteractor(f.cfg.ChangeSets, i.values, f.cfg.Now)
	}
	if f.cfg.UnitFamilies != nil {
		i.units = appunit.NewInteractor(f.cfg.UnitFamilies)
	}
	// Schema orchestrates the aggregate interactors; built after units so
	// bundle export/import and clone carry quantity families.
	i.schema = appschema.NewInteractor(i.typeDefs, i.attrs, i.relationships, i.deps, i.units)
	if f.cfg.Features.EventDelivery {
		i.webhooks = webhook.NewInteractor(unit, f.cfg.Subscriptions, f.cfg.Deliveries, f.cfg.WebhookURLPolicy)
		i.feed = feed.NewInteractor(f.cfg.FeedStore, f.cfg.CursorStore)
	}
	return i
}
