// Package flexitype is the embedding facade: everything needed to run
// flexitype inside your own Go service — usecases, storage, migrations,
// domain events — wired through one constructor with hook options for your
// pub/sub, webhooks or plain functions. For the standalone service, see
// cmd/flexitype.
package flexitype

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/computed"
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
	webhookAllowPrivate bool
	searchIndex         bool
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

// WithEventRetention sets how long expanded events stay readable in the
// feed before pruning (default 7 days). Only meaningful with WithOutbox.
func WithEventRetention(d time.Duration) Option {
	return func(o *options) { o.retention = d }
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
	cfg := application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: newRepos,
		Dispatcher:      o.dispatcher,
		Projections:     projections,
		ActivityLog:     postgres.NewActivityLog(pool),
		OnRollback:      o.onRollback,
		OnDispatchError: o.onDispatch,
		OnCleanupError:  o.onCleanup,
		Features:        o.features,
		SavedViews:      postgres.NewSavedViewStore(pool),
		MatchRules:      postgres.NewMatchStore(pool),
		Revisions:       postgres.NewRevisionStore(pool),
		ChangeSets:      postgres.NewChangeSetStore(pool),
		UnitFamilies:    postgres.NewUnitFamilyStore(pool),
		SearchStore:     searchStore, // may be nil; enables entity-data erasure of the projection
	}
	if o.outbox {
		store := postgres.NewOutboxStore(transactor)
		policy := webhook.URLPolicy{AllowPrivate: o.webhookAllowPrivate}
		workerOpts := append([]webhook.WorkerOption{
			webhook.WithHTTPClient(safedial.NewClient(safedial.Options{
				AllowPrivate: o.webhookAllowPrivate, Timeout: 10 * time.Second,
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
		pruner = feed.NewPruner(feedStore, retention, o.onBgError)

		cfg.Outbox = store
		cfg.OutboxNudge = relay.Nudge
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
	projections.Register(materializer, events.WithEventTypes(computed.EventTypes()...))

	// The GraphQL schema mirrors the live type definitions; an internal-projection
	// subscriber invalidates a tenant's cached schema when its definitions change.
	// This is a fast-path hint only: correctness is backed by the persisted
	// schema_version (issue #192), so routing it off the external dispatcher keeps
	// cross-replica invalidation intact.
	graphqlEngine := gql.NewEngine(gql.WithErrorObserver(o.onBgError))
	projections.Register(graphqlEngine, events.WithEventTypes(graphqlEngine.EventTypes()...))

	return &Service{
		pool:         pool,
		transactor:   transactor,
		dispatcher:   o.dispatcher,
		factory:      factory,
		relay:        relay,
		indexer:      indexer,
		materializer: materializer,
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
		Features:        o.features,
		SavedViews:      savedViews,
		MatchRules:      matchRules,
		Revisions:       revisions,
		ChangeSets:      changesets,
		UnitFamilies:    unitFamilies,
		BlobStore:       o.blobs,
		SearchStore:     searchStore, // may be nil; enables entity-data erasure of the projection
	})
	materializer := computed.NewMaterializer(factory)
	materializer.OnSchemaChange(schemaChangeRecomputer(materializer, o.onBgError))
	projections.Register(materializer, events.WithEventTypes(computed.EventTypes()...))
	graphqlEngine := gql.NewEngine(gql.WithErrorObserver(o.onBgError))
	projections.Register(graphqlEngine, events.WithEventTypes(graphqlEngine.EventTypes()...))
	return &Service{
		transactor:   transactor,
		dispatcher:   o.dispatcher,
		factory:      factory,
		indexer:      indexer,
		materializer: materializer,
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
	ctx = uow.WithSystemAccess(ctx)
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
	ctx = uow.WithSystemAccess(ctx) // a scheduler tick has no principal
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cs := s.factory.New(ctx).ChangeSets()
			if cs != nil {
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
	return s.indexer.Reindex(uow.WithSystemAccess(ctx), tenant)
}

// RecomputeComputed re-materializes every entity's computed attributes for a
// tenant — the recovery counterpart to ReindexSearch. Internal projections are
// maintained in the originating request's post-commit (issue #211), so a
// process crash between commit and that post-commit can leave a computed value
// stale; this rebuilds them all. Returns the number of entities recomputed.
func (s *Service) RecomputeComputed(ctx context.Context, tenant valueobjects.TenantID) (int, error) {
	return s.materializer.RecomputeTenant(uow.WithSystemAccess(ctx), tenant)
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
func (s *Service) Interactors(ctx context.Context) *application.Interactors {
	return s.factory.New(ctx)
}

// Factory exposes the underlying usecase factory for advanced wiring.
func (s *Service) Factory() application.Factory { return s.factory }

// GraphQLEngine exposes the read-only GraphQL engine, for embedders that build
// their own API handler (e.g. the WASM playground).
func (s *Service) GraphQLEngine() *gql.Engine { return s.graphql }

// Dispatcher exposes the event dispatcher, for inspection and for
// registering hooks before the service starts serving.
//
// Register during composition, not while traffic flows: Register mutates
// the handler slice that Dispatch reads, and neither is synchronised.
func (s *Service) Dispatcher() *events.Dispatcher { return s.dispatcher }

// APIConfig configures the mountable REST API for embedded deployments.
type APIConfig struct {
	Logger *logger.Logger
	Health *health.Service
	// Accounts authenticates bearer tokens; nil disables auth (development).
	Accounts serviceaccount.Authenticator
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
func (s *Service) APIHandler(cfg APIConfig) http.Handler {
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
	server := httpapi.ServerConfig{
		Factory:     s.factory,
		Logger:      cfg.Logger,
		Health:      cfg.Health,
		Accounts:    cfg.Accounts,
		Metrics:     cfg.Metrics,
		RateLimiter: cfg.RateLimiter,
		BlobStore:   s.blobs,
		GraphQL:     s.graphql,

		TenantRateLimiter: cfg.TenantRateLimiter,
		DisableConsole:    cfg.DisableConsole,
		MaxImportBytes:    cfg.MaxImportBytes,
		MaxMediaBytes:     cfg.MaxMediaBytes,
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
	return httpapi.NewHandler(server)
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
