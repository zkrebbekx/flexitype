// Package flexitype is the embedding facade: everything needed to run
// flexitype inside your own Go service — usecases, storage, migrations,
// domain events — wired through one constructor with hook options for your
// pub/sub, webhooks or plain functions. For the standalone service, see
// cmd/flexitype.
package flexitype

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/changeset"
	"github.com/zkrebbekx/flexitype/application/computed"
	"github.com/zkrebbekx/flexitype/application/erasure"
	"github.com/zkrebbekx/flexitype/application/feed"
	"github.com/zkrebbekx/flexitype/application/gql"
	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/search"
	"github.com/zkrebbekx/flexitype/application/uow"
	"github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	httpapi "github.com/zkrebbekx/flexitype/internal/interfaces/http"
	"github.com/zkrebbekx/flexitype/internal/safedial"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// Service is an embedded flexitype instance.
type Service struct {
	pool         *sqlx.DB
	transactor   db.Transactor
	dispatcher   *events.Dispatcher
	factory      application.Factory
	relay        *outbox.Relay
	indexer      *search.Indexer
	materializer *computed.Materializer
	timeZone     *time.Location
	clock        func() time.Time
	worker       *webhook.Worker
	pruner       *feed.Pruner
	blobs        blob.Store
	graphql      *gql.Engine
	onBgError    func(err error)
}

type options struct {
	dispatcher          *events.Dispatcher
	onRollback          func(ctx context.Context, err error)
	onDispatch          func(ctx context.Context, err error)
	onCleanup           func(err error)
	onBgError           func(err error)
	features            application.Features
	outbox              bool
	relayOpts           []outbox.RelayOption
	workerOpts          []webhook.WorkerOption
	retention           time.Duration
	deadLetterRetention time.Duration
	parkedRetention     time.Duration
	outboxMaxAttempts   int
	outboxRetryCeiling  time.Duration
	webhookAllowPrivate bool
	webhookTimeout      time.Duration
	timeZone            *time.Location
	clock               func() time.Time
	searchIndex         bool
	gqlFederation       bool
	failClosedACL       bool
	blobs               blob.Store
}

// WithBlobStore backs media attribute values with an object store (local
// disk, S3-compatible, …). Without it, media uploads return a validation
// error.
func WithBlobStore(s blob.Store) Option {
	return func(o *options) { o.blobs = s }
}

// Option customises an embedded Service.
type Option func(*options)

// WithHandler registers a dispatcher hook: any events.Handler your
// infrastructure provides.
func WithHandler(h events.Handler, opts ...events.RegisterOption) Option {
	return func(o *options) { o.dispatcher.Register(h, opts...) }
}

// WithHandlerFunc registers a plain function hook.
func WithHandlerFunc(name string, fn func(ctx context.Context, env events.Envelope) error, opts ...events.RegisterOption) Option {
	return func(o *options) { o.dispatcher.RegisterFunc(name, fn, opts...) }
}

// WithPublisher routes events into your pub/sub broker (NATS, Kafka, SNS,
// ...). topicFn may be nil to use the event type as the topic.
func WithPublisher(name string, pub events.Publisher, topicFn events.TopicFunc) Option {
	return func(o *options) { o.dispatcher.Register(events.NewPublisherHandler(name, pub, topicFn)) }
}

// WithWebhook delivers events as signed JSON POSTs to a receiving endpoint.
func WithWebhook(name string, cfg events.WebhookConfig, opts ...events.RegisterOption) Option {
	return func(o *options) { o.dispatcher.Register(events.NewWebhookHandler(name, cfg), opts...) }
}

// WithRollbackObserver observes rolled-back units of work.
func WithRollbackObserver(fn func(ctx context.Context, err error)) Option {
	return func(o *options) { o.onRollback = fn }
}

// WithDispatchObserver observes synchronous post-commit event-dispatch
// failures. In the default (non-outbox) mode the write is already durable when
// subscribers run, so a subscriber error is reported here instead of failing
// the request. Use WithOutbox for at-least-once delivery guarantees.
func WithDispatchObserver(fn func(ctx context.Context, err error)) Option {
	return func(o *options) { o.onDispatch = fn }
}

// WithBackgroundErrorObserver observes errors from the background schedulers
// (the change-set publisher and the events-feed pruner), which would otherwise
// be dropped silently. Use it to log or meter them.
func WithBackgroundErrorObserver(fn func(err error)) Option {
	return func(o *options) { o.onBgError = fn }
}

// WithCleanupObserver observes swallowed post-erasure cleanup failures — a
// media-blob GC or search-projection removal that could not be completed after
// a committed erasure. These are best-effort by design (they must not undo a
// durable erasure), so use this to log or meter them rather than lose them.
// Media-blob failures are additionally reported in PurgeReport.MediaBlobsFailed
// / UnpurgedBlobKeys.
func WithCleanupObserver(fn func(err error)) Option {
	return func(o *options) { o.onCleanup = fn }
}

// WithFailClosedACL inverts the field-ACL default: a context that carries no
// uow.Access policy denies every attribute instead of granting admin.
//
// The standalone service always stamps a policy from the authenticated
// service account, so this option is for embedders. In library mode the host
// is responsible for stamping the policy on every request, and nothing
// otherwise enforces that it did — a background job or a new resolver that
// forgets silently runs with full field access. With this option it fails
// instead.
//
// Stamp uow.WithAccess on every request path, and uow.WithSystemAccess on
// host-owned background work that legitimately has no principal. The setting
// applies to the whole process and cannot be undone; see
// uow.RequireAccessPolicy.
func WithFailClosedACL() Option {
	return func(o *options) { o.failClosedACL = true }
}

// WithGraphQLFederation exposes the GraphQL endpoint as an Apollo-Federation
// subgraph: `_service { sdl }`, `_entities(representations:)`, and
// `@key(fields: "entityId")` on every entity type.
//
// Without it the endpoint is a standalone schema that a federated gateway
// cannot compose at all. With it, a gateway resolves an entity this service
// holds attributes for from the entity id another subgraph already owns,
// which is the natural modelling for an attribute service.
//
// It is off by default: a federated schema carries three fields no standalone
// client asks for, and `_entities` is a batch read a non-federated deployment
// has no reason to expose.
func WithGraphQLFederation() Option {
	return func(o *options) { o.gqlFederation = true }
}

// WithoutSearch disables the FQL query surface for this deployment.
func WithoutSearch() Option {
	return func(o *options) { o.features.DisableSearch = true }
}

// WithoutActivityLog disables the audit log entirely: no pre-commit
// writes, no read API.
func WithoutActivityLog() Option {
	return func(o *options) { o.features.DisableActivity = true }
}

// WithOutbox upgrades event delivery to at-least-once: envelopes persist
// in the same transaction as the change and a relay dispatches them with
// retries. It also unlocks the standalone-consumer surface — webhook
// subscriptions and the events feed. Run the delivery machinery with
// Service.RunOutboxRelay.
func WithOutbox(opts ...outbox.RelayOption) Option {
	return func(o *options) {
		o.outbox = true
		o.relayOpts = opts
	}
}

// WithDeliveryWorker customises the webhook delivery worker (attempt cap,
// concurrency, HTTP client). Only meaningful with WithOutbox.
func WithDeliveryWorker(opts ...webhook.WorkerOption) Option {
	return func(o *options) { o.workerOpts = opts }
}

// WithTimeZone sets the calendar day that `today` and `now` resolve against
// in dependency conditions and dynamic defaults. Default UTC.
//
// It changes which day those name, not how anything is stored: a date value
// is a calendar date held as midnight UTC either way. Without it, a tenant
// operating outside UTC had a date-boundary rule that was wrong for part of
// every day — a condition on "expires before today" flipped at the wrong
// hour, and a `today` default recorded yesterday for anything created after
// the UTC midnight.
//
// Per-request override: stamp uow.WithTimeZone on the context, which is how
// an embedder serves tenants in different zones from one process.
func WithTimeZone(loc *time.Location) Option {
	return func(o *options) { o.timeZone = loc }
}

// WithClock pins the instant that `today` and `now` resolve against, for
// tests and simulations. Production deployments do not set it.
//
// It exists because a `today` rule is a function of the wall clock, so a test
// that asserts on its outcome is only correct during part of the day. A
// pinned clock makes the outcome a constant. It rides the context exactly as
// WithTimeZone does — Service.Context and the API middleware stamp it — so it
// reaches every entry point the zone reaches.
//
// Scope: calendar evaluation only (dependency rules, dynamic defaults).
// Stored timestamps (created_at, updated_at) keep the wall clock, so a pinned
// clock cannot backdate an audit trail.
//
// Per-request override: stamp uow.WithClock on the context.
func WithClock(now func() time.Time) Option {
	return func(o *options) { o.clock = now }
}

// WithWebhookTimeout bounds one webhook delivery attempt (default 10s).
//
// It is a duration rather than an *http.Client on purpose: the delivery
// client is the SSRF guard, and supplying a client would replace that guard
// without saying so.
func WithWebhookTimeout(d time.Duration) Option {
	return func(o *options) { o.webhookTimeout = d }
}

// WithEventRetention sets how long expanded events stay readable in the
// feed before pruning (default 7 days). Only meaningful with WithOutbox.
func WithEventRetention(d time.Duration) Option {
	return func(o *options) { o.retention = d }
}

// WithDeadLetterRetention bounds how long a DEAD delivery is kept (default 30
// days). Only meaningful with WithOutbox.
//
// The envelope prune keeps anything a dead delivery references, which is what
// makes a dead letter redrivable — but nothing else deleted a dead row, so one
// decommissioned endpoint pinned its envelopes for ever and the event
// retention stopped bounding the outbox or the feed at all. This is where that
// bound lives. It is far longer than the event retention on purpose: a dead
// letter has to outlive the events it references long enough for an operator
// to notice it.
func WithDeadLetterRetention(d time.Duration) Option {
	return func(o *options) { o.deadLetterRetention = d }
}

// WithParkedRetention bounds how long a PARKED envelope is kept before the
// pruner deletes it (default 30 days). Only meaningful with WithOutbox.
//
// A parked envelope is a committed change that exhausted its retry budget and
// was never delivered. Pruning it is DELIBERATE data loss — after the prune
// the event can never be redriven — so keep this well past the window in
// which the flexitype_outbox_parked gauge is alerted on and the envelopes are
// redriven (POST /admin/outbox/redrive). The bound exists because a parked
// envelope has no feed_seq, so the event retention never reached it and one
// poisonous event type could grow the outbox for ever.
func WithParkedRetention(d time.Duration) Option {
	return func(o *options) { o.parkedRetention = d }
}

// WithOutboxMaxAttempts sets how many dispatch failures park an envelope
// (default 25). Only meaningful with WithOutbox.
//
// With the default retry ceiling the default budget spans roughly 5h45m, so
// a downstream outage longer than that parks every envelope written during
// it. Size the budget to the longest outage the deployment should ride out
// unattended; parked envelopes then need an operator redrive.
func WithOutboxMaxAttempts(n int) Option {
	return func(o *options) { o.outboxMaxAttempts = n }
}

// WithOutboxRetryCeiling caps the exponential backoff between outbox
// dispatch attempts (default 15 minutes). Only meaningful with WithOutbox.
// Together with WithOutboxMaxAttempts it sets the retry window: attempts
// back off 1s, 4s, 16s, ... up to this ceiling.
func WithOutboxRetryCeiling(d time.Duration) Option {
	return func(o *options) { o.outboxRetryCeiling = d }
}

// WithWebhookAllowPrivate lets webhook subscriptions target private,
// loopback and link-local hosts over http — for on-prem deployments whose
// consumers live on internal networks. Off by default (SSRF guard).
func WithWebhookAllowPrivate() Option {
	return func(o *options) { o.webhookAllowPrivate = true }
}

// WithSearchIndex enables the entity search projection: an internal-projection
// subscriber keeps one searchable document per entity, unlocking FQL matches().
// The index is maintained synchronously in the writing request (read-your-writes)
// in both delivery modes, so it stays fresh independent of WithOutbox (#211).
func WithSearchIndex() Option {
	return func(o *options) {
		o.searchIndex = true
		o.features.SearchIndex = true
	}
}

// New wires an embedded flexitype over your connection pool. The pool is
// shared, never owned: closing it remains your call.
func New(pool *sqlx.DB, opts ...Option) *Service {
	o := &options{dispatcher: events.NewDispatcher()}
	for _, opt := range opts {
		opt(o)
	}
	if o.failClosedACL {
		uow.RequireAccessPolicy()
	}

	transactor := db.NewTransactor(pool)
	newRepos := func() application.Repositories { return postgres.NewRepositories(pool) }

	// Internal projections ride their OWN dispatcher, maintained synchronously in
	// the originating unit of work (see application/uow) in BOTH delivery modes —
	// never the external o.dispatcher the relay drains. This keeps computed-
	// attribute, search-index and GraphQL-cache consistency (and read-your-writes)
	// identical whether or not WithOutbox is enabled (issue #211); o.dispatcher is
	// reserved for external consumer hooks (webhooks, pub/sub, funcs).
	projections := events.NewDispatcher()

	var indexer *search.Indexer
	var searchStore search.DocumentStore
	if o.searchIndex {
		searchStore = postgres.NewSearchStore(pool)
		indexer = search.NewIndexer(newRepos, searchStore)
		projections.Register(indexer, events.WithEventTypes(search.EventTypes()...))
	}

	var relay *outbox.Relay
	var worker *webhook.Worker
	var pruner *feed.Pruner
	residualErasers := []erasure.ResidualEraser{
		postgres.NewChangeSetEraser(),
		postgres.NewOutboxEraser(),
		postgres.NewActivityEraser(),
	}
	cfg := application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: newRepos,
		Dispatcher:      o.dispatcher,
		Projections:     projections,
		ActivityLog:     postgres.NewActivityLog(pool),
		OnRollback:      o.onRollback,
		OnDispatchError: o.onDispatch,
		OnCleanupError:  o.onCleanup,
		// Erasure has to reach the records that copied the values it deletes:
		// the event log (which the feed serves until retention prunes it, and
		// forever for rows never expanded) and the activity log (kept on
		// purpose, so the erasure stays provable).
		ErasureResiduals: residualErasers,
		Features:         o.features,
		SavedViews:       postgres.NewSavedViewStore(pool),
		MatchRules:       postgres.NewMatchStore(pool),
		Revisions:        postgres.NewRevisionStore(pool),
		ChangeSets:       postgres.NewChangeSetStore(pool),
		UnitFamilies:     postgres.NewUnitFamilyStore(pool),
		SearchStore:      searchStore, // may be nil; enables entity-data erasure of the projection
	}
	if o.outbox {
		var storeOpts []postgres.OutboxStoreOption
		if o.outboxMaxAttempts > 0 {
			storeOpts = append(storeOpts, postgres.WithOutboxMaxAttempts(o.outboxMaxAttempts))
		}
		if o.outboxRetryCeiling > 0 {
			storeOpts = append(storeOpts, postgres.WithOutboxRetryCeiling(o.outboxRetryCeiling))
		}
		store := postgres.NewOutboxStore(transactor, storeOpts...)
		policy := webhook.URLPolicy{AllowPrivate: o.webhookAllowPrivate}
		// The delivery client is built here, not supplied, because it is the
		// SSRF guard: safedial refuses private and link-local destinations
		// unless the deployment opted in by name. A caller-supplied
		// http.Client would replace that guard silently, so the tunable is
		// the TIMEOUT rather than the client.
		timeout := o.webhookTimeout
		if timeout <= 0 {
			timeout = 10 * time.Second
		}
		workerOpts := append([]webhook.WorkerOption{
			webhook.WithHTTPClient(safedial.NewClient(safedial.Options{
				AllowPrivate: o.webhookAllowPrivate, Timeout: timeout,
			})),
		}, o.workerOpts...)
		worker = webhook.NewWorker(postgres.NewDeliveryStore(pool), workerOpts...)
		relay = outbox.NewRelay(store, o.dispatcher,
			append([]outbox.RelayOption{outbox.WithAfterExpand(worker.Nudge)}, o.relayOpts...)...)

		retention := o.retention
		if retention <= 0 {
			retention = 7 * 24 * time.Hour
		}
		feedStore := postgres.NewFeedStore(pool)
		pruner = feed.NewPruner(feedStore, retention, o.onBgError).
			WithDeadLetterRetention(o.deadLetterRetention).
			WithParkedRetention(o.parkedRetention)

		cfg.Outbox = store
		cfg.OutboxNudge = relay.Nudge
		// The postgres outbox adapter always implements the recovery
		// surface; the assertion is backed by a compile-time check in the
		// adapter, so it cannot fail at runtime.
		cfg.OutboxOps = store.(outbox.OpsStore)
		cfg.Subscriptions = postgres.NewSubscriptionStore(pool)
		cfg.WebhookURLPolicy = policy
		cfg.Deliveries = postgres.NewDeliveryStore(pool)
		cfg.FeedStore = feedStore
		cfg.CursorStore = postgres.NewCursorStore(pool)
		cfg.Features.EventDelivery = true
	}

	cfg.BlobStore = o.blobs

	factory := application.NewFactory(cfg)
	// Computed attributes materialize via an internal-projection subscriber, so
	// their derived values are ordinary (FQL-queryable) values — maintained in
	// the writing request regardless of the outbox.
	materializer := computed.NewMaterializer(factory)
	materializer.OnSchemaChange(schemaChangeRecomputer(materializer, o.onBgError))
	materializer.OnFormulaError(o.onBgError)
	projections.Register(materializer, events.WithEventTypes(computed.EventTypes()...))

	// The GraphQL schema mirrors the live type definitions; an internal-projection
	// subscriber invalidates a tenant's cached schema when its definitions change.
	// This is a fast-path hint only: correctness is backed by the persisted
	// schema_version (issue #192), so routing it off the external dispatcher keeps
	// cross-replica invalidation intact.
	graphqlEngine := gql.NewEngine(gqlOptions(o)...)
	projections.Register(graphqlEngine, events.WithEventTypes(graphqlEngine.EventTypes()...))

	return &Service{
		pool:         pool,
		transactor:   transactor,
		dispatcher:   o.dispatcher,
		factory:      factory,
		relay:        relay,
		indexer:      indexer,
		materializer: materializer,
		timeZone:     o.timeZone,
		clock:        o.clock,
		worker:       worker,
		pruner:       pruner,
		blobs:        o.blobs,
		graphql:      graphqlEngine,
		onBgError:    o.onBgError,
	}
}

// NewInMemory wires flexitype over the in-memory store: no database, no
// migrations. Same usecases, same API, same hooks — it powers the browser
// playground and makes a zero-dependency test double for embedding
// consumers. Data lives for the process only; WithOutbox is ignored
// (direct dispatch is already synchronous and in-process).
func NewInMemory(opts ...Option) *Service {
	o := &options{dispatcher: events.NewDispatcher()}
	for _, opt := range opts {
		opt(o)
	}
	if o.failClosedACL {
		uow.RequireAccessPolicy()
	}

	store := memory.NewStore()
	newRepos := func() application.Repositories { return store.Repositories() }
	savedViews := memory.NewSavedViewStore()
	matchRules := memory.NewMatchStore()
	revisions := memory.NewRevisionStore()
	changesets := memory.NewChangeSetStore()
	unitFamilies := memory.NewUnitFamilyStore()
	// The playground gets a working, process-local media store by default.
	if o.blobs == nil {
		o.blobs = blob.NewMemoryStore()
	}

	// Internal projections ride their own dispatcher (see New), maintained
	// synchronously in the originating unit of work. The in-memory service is
	// always direct-dispatch (WithOutbox is ignored), but keeping the split
	// matches the database path and reserves o.dispatcher for external hooks.
	projections := events.NewDispatcher()

	var indexer *search.Indexer
	var searchStore search.DocumentStore
	if o.searchIndex {
		searchStore = store.SearchStore()
		indexer = search.NewIndexer(newRepos, searchStore)
		projections.Register(indexer, events.WithEventTypes(search.EventTypes()...))
	}

	transactor := store.Transactor()
	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: newRepos,
		Dispatcher:      o.dispatcher,
		Projections:     projections,
		ActivityLog:     store.ActivityLog(),
		OnRollback:      o.onRollback,
		OnDispatchError: o.onDispatch,
		OnCleanupError:  o.onCleanup,
		// The activity log copies the values an erasure deletes, and it
		// survives the erasure on purpose so the erasure stays provable, so
		// its entries are redacted. There is no in-memory event log to
		// redact: this backend direct-dispatches and keeps no outbox.
		ErasureResiduals: residualErasersInMemory(store, changesets),
		Features:         o.features,
		SavedViews:       savedViews,
		MatchRules:       matchRules,
		Revisions:        revisions,
		ChangeSets:       changesets,
		UnitFamilies:     unitFamilies,
		BlobStore:        o.blobs,
		SearchStore:      searchStore, // may be nil; enables entity-data erasure of the projection
	})
	materializer := computed.NewMaterializer(factory)
	materializer.OnSchemaChange(schemaChangeRecomputer(materializer, o.onBgError))
	materializer.OnFormulaError(o.onBgError)
	projections.Register(materializer, events.WithEventTypes(computed.EventTypes()...))
	graphqlEngine := gql.NewEngine(gqlOptions(o)...)
	projections.Register(graphqlEngine, events.WithEventTypes(graphqlEngine.EventTypes()...))
	return &Service{
		transactor:   transactor,
		dispatcher:   o.dispatcher,
		factory:      factory,
		indexer:      indexer,
		materializer: materializer,
		timeZone:     o.timeZone,
		clock:        o.clock,
		blobs:        o.blobs,
		graphql:      graphqlEngine,
		onBgError:    o.onBgError,
	}
}

// RunOutboxRelay runs the event-delivery machinery until ctx ends: the
// outbox relay (expansion + in-process dispatch), the webhook delivery
// worker and the retention pruner. No-op without WithOutbox. Run it as a
// goroutine next to the server; every replica runs it safely.
func (s *Service) RunOutboxRelay(ctx context.Context, loops ...DeliveryLoops) {
	if s.relay == nil {
		return
	}
	run := AllDeliveryLoops()
	if len(loops) > 0 {
		run = loops[0]
	}
	// The delivery machinery has no principal, so it stamps the system policy
	// explicitly rather than inheriting the default — WithFailClosedACL
	// inverts that default to deny-all.
	ctx = s.Context(uow.WithSystemAccess(ctx))
	// Block until every selected loop has observed ctx cancellation and
	// returned, so shutdown can be ordered around this call: the relay,
	// delivery worker and pruner are fully stopped before the pool or
	// broker clients they depend on are closed.
	var wg sync.WaitGroup
	if run.Worker {
		wg.Add(1)
		go func() { defer wg.Done(); s.worker.Run(ctx) }()
	}
	if run.Pruner {
		wg.Add(1)
		go func() { defer wg.Done(); s.pruner.Run(ctx) }()
	}
	if run.Relay {
		s.relay.Run(ctx)
	} else {
		<-ctx.Done()
	}
	wg.Wait()
}

// DeliveryLoops selects which delivery loops a process runs, so an API tier
// and a worker tier can be scaled, autoscaled and drained separately from one
// image.
//
// No leader election is involved: every loop claims work with a lease and
// FOR UPDATE SKIP LOCKED, so running one on any number of replicas is safe.
// The switches exist because ten API replicas polling the outbox every two
// seconds is load that a scaling decision made for request traffic should not
// create.
type DeliveryLoops struct {
	// Relay expands the outbox and dispatches to in-process hooks.
	Relay bool
	// Worker delivers webhook subscriptions.
	Worker bool
	// Pruner enforces event retention.
	Pruner bool
}

// AllDeliveryLoops runs everything — the single-process default.
func AllDeliveryLoops() DeliveryLoops {
	return DeliveryLoops{Relay: true, Worker: true, Pruner: true}
}

// RunChangeSetScheduler publishes approved change-sets whose publish_at has
// arrived, on the given interval, until ctx ends. Run it as a goroutine next
// to the server; every replica runs it safely (a published set is skipped by
// the others). A zero interval defaults to one minute.
func (s *Service) RunChangeSetScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	// A scheduler tick has no principal, and it evaluates the same rules a
	// request does, so it takes the deployment's zone too.
	ctx = s.Context(uow.WithSystemAccess(ctx))
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cs := s.factory.New(ctx).ChangeSets()
			if cs != nil {
				// Per-set failures used to be swallowed by a bare continue
				// inside PublishDue, so a set that could never publish
				// retried for ever with nothing reported. They now reach the
				// same observer as the tick's own error.
				if s.onBgError != nil {
					cs.OnPublishFailure(func(set changeset.ChangeSet, err error) {
						s.onBgError(fmt.Errorf("publish scheduled change-set %s (%s): %w",
							set.ID, set.Name, err))
					})
				}
				if _, err := cs.PublishDue(ctx); err != nil && s.onBgError != nil {
					s.onBgError(err)
				}
			}
		}
	}
}

// EnsureWebhookSubscription upserts a webhook subscription by name — the
// bootstrap path for environment-configured endpoints. Errors unless
// WithOutbox is enabled.
func (s *Service) EnsureWebhookSubscription(ctx context.Context, name, url, secret string, eventTypes ...string) error {
	i := s.factory.New(ctx)
	if i.Webhooks() == nil {
		return domainerrors.NewValidation("webhook subscriptions require the outbox; enable it with WithOutbox")
	}
	_, err := i.Webhooks().Ensure(ctx, webhook.CreateInput{
		Name:       name,
		URL:        url,
		Secret:     secret,
		EventTypes: eventTypes,
	})
	return err
}

// ReindexSearch rebuilds every entity search document for a tenant.
// Errors unless WithSearchIndex is enabled.
func (s *Service) ReindexSearch(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	if s.indexer == nil {
		return 0, domainerrors.NewValidation("the search index is disabled; enable it with WithSearchIndex")
	}
	return s.indexer.Reindex(s.Context(uow.WithSystemAccess(ctx)), tenant)
}

// RecomputeComputed re-materializes every entity's computed attributes for a
// tenant — the recovery counterpart to ReindexSearch. Internal projections are
// maintained in the originating request's post-commit (issue #211), so a
// process crash between commit and that post-commit can leave a computed value
// stale; this rebuilds them all. Returns the number of entities recomputed.
func (s *Service) RecomputeComputed(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	return s.materializer.RecomputeTenant(s.Context(uow.WithSystemAccess(ctx)), tenant)
}

// Migrate applies flexitype's embedded schema migrations. Safe to call on
// every startup; concurrent callers serialize on an advisory lock. No-op
// for in-memory services.
func (s *Service) Migrate(ctx context.Context) error {
	if s.pool == nil {
		return nil
	}
	return postgres.Migrate(ctx, s.transactor)
}

// SchemaDrift reports migration versions the database has applied that this
// binary does not carry — the schema is newer than this build.
//
// A rolling deploy makes that state normal for a while: the first new pod
// migrates while the previous generation keeps serving. flexitype supports it
// (each release's migrations stay compatible with the previous binary, see
// docs/upgrades.md), but an operator should be able to see a mixed-version
// fleet rather than infer it. It returns nothing for an in-memory service and
// nothing when the schema matches.
func (s *Service) SchemaDrift(ctx context.Context) ([]int, error) {
	if s.pool == nil {
		return nil, nil
	}
	return postgres.UnknownSchemaVersions(ctx, s.pool)
}

// Interactors returns a request-scoped usecase set. Call once per request
// or unit of work so dataloader caches stay request-local.
//
// PASS THE CONTEXT THROUGH Context FIRST when the deployment sets a time
// zone. An interactor set carries no context of its own — every method takes
// one from its caller — so a zone stamped here would reach nothing:
//
//	ctx = svc.Context(ctx)
//	it := svc.Interactors(ctx)
//	schema, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
func (s *Service) Interactors(ctx context.Context) *application.Interactors {
	return s.factory.New(s.Context(ctx))
}

// Context returns ctx with the service-wide defaults stamped on it: the
// deployment's time zone, when one is configured.
//
// It exists because those defaults have to travel on the context the CALLER
// passes to each interactor method. Stamping them inside Interactors derived
// a context that was then discarded, so FLEXITYPE_TIMEZONE never reached rule
// evaluation and every `today`/`now` dependency rule and dynamic default
// resolved in UTC — the read and write paths agreeing only because both were
// wrong. The API stamps the same defaults in its middleware, which owns the
// request context.
//
// A caller that already chose a zone keeps it, which is how a host serves
// tenants in different zones from one process.
func (s *Service) Context(ctx context.Context) context.Context {
	if s.timeZone != nil && !uow.HasTimeZone(ctx) {
		ctx = uow.WithTimeZone(ctx, s.timeZone)
	}
	if s.clock != nil && !uow.HasClock(ctx) {
		ctx = uow.WithClock(ctx, s.clock)
	}
	return ctx
}

// Factory exposes the underlying usecase factory for advanced wiring.
func (s *Service) Factory() application.Factory { return s.factory }

// GraphQLEngine exposes the read-only GraphQL engine, for embedders that build
// their own API handler (e.g. the WASM playground).
func (s *Service) GraphQLEngine() *gql.Engine { return s.graphql }

// Dispatcher exposes the event dispatcher, for inspection and for
// registering hooks.
//
// Late registration is safe: the dispatcher copies its handler slice on
// write under an RWMutex, so a Register concurrent with a Dispatch cannot
// race. (This comment previously said the opposite, and had done since
// before the copy-on-write change.)
func (s *Service) Dispatcher() *events.Dispatcher { return s.dispatcher }

// APIConfig configures the mountable REST API for embedded deployments.
type APIConfig struct {
	Logger *logger.Logger
	Health *health.Service
	// Accounts authenticates bearer tokens.
	//
	// NIL SERVES THE ENTIRE API TO UNAUTHENTICATED CALLERS, including the
	// irreversible POST /admin/purge. It is a development convenience and
	// nothing else, so it must be opted into by name with AllowAnonymous.
	// Without that, APIHandler panics and NewAPIHandler returns an error —
	// mirroring the standalone binary, which refuses to boot in this state.
	Accounts serviceaccount.Authenticator
	// AllowAnonymous opts a deployment into serving the whole API without
	// authentication. It exists so that state cannot be reached by omission.
	//
	// The standalone binary has FLEXITYPE_DEV_INSECURE for the same purpose.
	// Library mode had no equivalent: the fail-closed default was added to
	// internal/config, which only governs the binary, so an embedder who read
	// the release note about authentication becoming fail-closed would
	// reasonably have assumed it applied to them.
	AllowAnonymous bool
	// Metrics, when set, records HTTP SLIs and serves /metrics. With the
	// outbox on, delivery-depth gauges are registered automatically.
	Metrics *metrics.Metrics
	// EnableProvisioning turns on the admin-scoped tenant/service-account
	// API (database-backed only).
	EnableProvisioning bool
	// RateLimiter, when set, throttles API requests per service account
	// (429 + Retry-After). Build one with ratelimit.New.
	RateLimiter *ratelimit.Limiter
	// TenantRateLimiter, when set, caps a tenant's aggregate request rate
	// across all of its service accounts.
	TenantRateLimiter *ratelimit.Limiter
	// AuthRateLimiter, when set, throttles by client address BEFORE
	// authentication. The other two limiters key on a resolved principal, so
	// neither can throttle a FAILED authentication — and each of those costs
	// a database round trip and a hash, uncached, so an unauthenticated
	// caller could exhaust the pool and brute-force tokens unthrottled.
	//
	// Behind a proxy this keys on the proxy, giving a ceiling on aggregate
	// unauthenticated traffic rather than a per-client one. It deliberately
	// does not read X-Forwarded-For: a header is attacker-supplied, so
	// trusting it would let one client spread its attempts across unlimited
	// keys.
	AuthRateLimiter *ratelimit.Limiter
	// DisableConsole omits the admin-console SPA, for an API-only deployment.
	// An unmatched path then returns a JSON 404 like any other API error.
	DisableConsole bool
	// MaxImportBytes caps a CSV import upload; 0 uses the 16 MiB default.
	// GET /features reports the effective value, so a client can chunk a
	// bulk load against the real ceiling instead of guessing.
	MaxImportBytes int64
	// MaxMediaBytes caps a media upload; 0 uses the 32 MiB default.
	MaxMediaBytes int64
}

// NewAccountLookup returns a database-backed authenticator over this
// service's pool, with a short success cache so revocation propagates
// within ttl. nil for in-memory services.
func (s *Service) NewAccountLookup(ttl time.Duration) serviceaccount.Authenticator {
	if s.pool == nil {
		return nil
	}
	return serviceaccount.NewCachingAuthenticator(postgres.NewAccountLookup(s.pool), ttl)
}

// AdminInteractor returns the provisioning usecases over this service's
// pool, or nil for in-memory services.
//
// opts are passed through; APIHandler wires admin.WithAuthCache when the
// deployment authenticates through a caching authenticator, so a rotation or a
// revocation takes effect at once rather than at the end of the cache TTL.
func (s *Service) AdminInteractor(opts ...admin.Option) *admin.Interactor {
	if s.pool == nil {
		return nil
	}
	return admin.NewInteractor(postgres.NewAdminStore(s.pool), opts...)
}

// BootstrapAdmin seeds the provisioning tables with a tenant and an
// admin-scoped service account when no accounts exist yet, returning the
// one-time token so an operator can call the admin API. It is idempotent:
// once any account exists it returns an empty token and does nothing. This
// is the only way to get the first credential into a database-backed
// deployment.
func (s *Service) BootstrapAdmin(ctx context.Context, tenantName, accountName string) (string, error) {
	if s.pool == nil {
		return "", domainerrors.NewValidation("provisioning requires a database-backed service")
	}
	a := s.AdminInteractor()

	// Fail closed: a transient error on the existence check must NOT fall
	// through to minting a fresh admin credential — that would defeat the
	// documented idempotency and hand out a superuser token on a blip.
	existing, err := a.ListAccounts(ctx, tenantName)
	if err != nil {
		return "", fmt.Errorf("check existing accounts: %w", err)
	}
	if len(existing) > 0 {
		return "", nil // already bootstrapped
	}
	if _, err := a.CreateTenant(ctx, tenantName); err != nil && !domainerrors.IsConflict(err) {
		return "", err
	}
	out, err := a.CreateAccount(ctx, admin.CreateAccountInput{
		TenantName: tenantName,
		Name:       accountName,
		Scopes:     []string{"admin"},
	})
	if err != nil {
		return "", err
	}
	return out.Token, nil
}

// APIHandler returns flexitype's versioned REST API as an http.Handler you
// can mount in your own router.
//
// It PANICS when the configuration would serve the API to unauthenticated
// callers without an explicit opt-in — that is a composition-time
// misconfiguration, so it fails at startup rather than per request. Use
// NewAPIHandler to handle it as an error instead.
func (s *Service) APIHandler(cfg APIConfig) http.Handler {
	h, err := s.NewAPIHandler(cfg)
	if err != nil {
		panic(err)
	}
	return h
}

// NewAPIHandler is APIHandler with the configuration check reported as an
// error rather than a panic.
func (s *Service) NewAPIHandler(cfg APIConfig) (http.Handler, error) {
	if cfg.Accounts == nil && !cfg.AllowAnonymous {
		return nil, errors.New(
			"flexitype: APIConfig.Accounts is nil, which serves the whole API — including the " +
				"irreversible POST /admin/purge — to unauthenticated callers. Set Accounts, or set " +
				"AllowAnonymous to opt in explicitly")
	}
	if cfg.Logger == nil {
		cfg.Logger = logger.New(logger.Config{})
	}
	if cfg.Health == nil {
		cfg.Health = health.NewService("flexitype", "embedded")
	}
	if cfg.Metrics != nil && s.pool != nil && s.worker != nil {
		// Delivery-depth gauges are only meaningful when the outbox tables
		// exist (outbox enabled over a real pool).
		cfg.Metrics.RegisterDeliveryCollector(postgres.NewDeliveryStats(s.pool))
	}
	if cfg.Accounts == nil {
		cfg.Logger.Warn().Msg(
			"authentication is DISABLED (APIConfig.AllowAnonymous): the whole API, including POST /admin/purge, is served to anonymous callers")
	}
	server := httpapi.ServerConfig{
		Factory:     s.factory,
		Logger:      cfg.Logger,
		Health:      cfg.Health,
		Accounts:    cfg.Accounts,
		Metrics:     cfg.Metrics,
		RateLimiter: cfg.RateLimiter,
		BlobStore:   s.blobs,
		GraphQL:     s.graphql,

		AuthRateLimiter:   cfg.AuthRateLimiter,
		TenantRateLimiter: cfg.TenantRateLimiter,
		DisableConsole:    cfg.DisableConsole,
		MaxImportBytes:    cfg.MaxImportBytes,
		MaxMediaBytes:     cfg.MaxMediaBytes,
		TimeZone:          s.timeZone,
		Clock:             s.clock,
	}
	if cfg.EnableProvisioning {
		var adminOpts []admin.Option
		if inv, ok := cfg.Accounts.(serviceaccount.Invalidator); ok {
			adminOpts = append(adminOpts, admin.WithAuthCache(inv))
		}
		server.Admin = s.AdminInteractor(adminOpts...)
	}
	// Reindex needs the search index; recompute does not. Gating both on
	// the indexer made the maintenance recompute endpoint 404 for every
	// deployment that runs computed attributes without search.
	if s.indexer != nil {
		server.Reindex = s.ReindexSearch
	}
	server.RecomputeComputed = s.RecomputeComputed
	return httpapi.NewHandler(server), nil
}

// schemaChangeRecomputer rebuilds a type's materialized computed values after
// a schema change, in the background.
//
// Editing a formula used to converge only when each entity happened to be
// written again, or when an operator remembered to run the tenant-wide
// recompute — an unbounded, operator-dependent staleness window in which the
// old values stayed queryable and the correction appeared to have been
// applied. Adding a computed attribute to a populated type had the same shape.
//
// It runs off the request goroutine deliberately: a schema edit on a type with
// millions of entities must not hold the editing request open. The cost is
// that convergence is shortly after the edit rather than at it, and that a
// process restart mid-rebuild leaves the remainder to the next write or to
// RecomputeComputed. Errors go to the background-error sink.
func schemaChangeRecomputer(m *computed.Materializer, onErr func(error)) func(valueobjects.TenantID, string) {
	return func(tenant valueobjects.TenantID, typeID string) {
		go func() {
			// Let the burst settle first. A schema change is normally
			// followed immediately by writes — an import, a seeding script,
			// the console applying a template — and a rebuild that overlaps
			// them recomputes entities whose inputs are still arriving. It
			// cannot corrupt them (a rebuild never clears), but it is wasted
			// work racing the write path that is already recomputing each
			// entity correctly.
			time.Sleep(schemaRecomputeSettle)

			// Detached from the request: the rebuild outlives the response,
			// and a cancelled request must not abandon it half-done.
			ctx, cancel := context.WithTimeout(
				uow.WithSystemAccess(context.WithoutCancel(context.Background())),
				schemaRecomputeTimeout)
			defer cancel()
			if _, err := m.RecomputeType(ctx, tenant, typeID); err != nil && onErr != nil {
				onErr(fmt.Errorf("recompute computed attributes for type %s: %w", typeID, err))
			}
		}()
	}
}

// schemaRecomputeTimeout bounds one background rebuild, so a pathological
// type cannot pin a goroutine and a connection for ever.
const schemaRecomputeTimeout = 30 * time.Minute

// schemaRecomputeSettle delays a rebuild so it does not race the writes that
// normally follow a schema change. Convergence is already "shortly after",
// not "at", the edit; this makes the shortly-after land after the burst.
const schemaRecomputeSettle = 500 * time.Millisecond

// gqlOptions assembles the GraphQL engine's options from the facade options,
// so both constructors configure the engine identically.
func gqlOptions(o *options) []gql.EngineOption {
	opts := []gql.EngineOption{gql.WithErrorObserver(o.onBgError)}
	if o.gqlFederation {
		opts = append(opts, gql.WithFederation())
	}
	return opts
}

// residualErasersInMemory lists the in-memory stores that copy a value and so
// must be redacted by an erasure. There is no in-memory event log: this
// backend direct-dispatches and keeps no outbox.
func residualErasersInMemory(store *memory.Store, changesets changeset.Store) []erasure.ResidualEraser {
	// The activity log copies the values an erasure deletes and survives the
	// erasure on purpose, so the erasure stays provable — which is why its
	// entries are redacted rather than deleted.
	out := []erasure.ResidualEraser{store.NewActivityEraser()}
	if eraser := memory.ChangeSetEraserFor(changesets); eraser != nil {
		out = append(out, eraser)
	}
	return out
}
