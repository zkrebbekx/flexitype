// Package flexitype is the embedding facade: everything needed to run
// flexitype inside your own Go service — usecases, storage, migrations,
// domain events — wired through one constructor with hook options for your
// pub/sub, webhooks or plain functions. For the standalone service, see
// cmd/flexitype.
package flexitype

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/application/admin"
	"github.com/zkrebbekx/flexitype/application/computed"
	"github.com/zkrebbekx/flexitype/application/feed"
	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/search"
	"github.com/zkrebbekx/flexitype/application/webhook"
	domainerrors "github.com/zkrebbekx/flexitype/domain/errors"
	"github.com/zkrebbekx/flexitype/domain/valueobjects"
	"github.com/zkrebbekx/flexitype/infrastructure/memory"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	httpapi "github.com/zkrebbekx/flexitype/internal/interfaces/http"
	"github.com/zkrebbekx/flexitype/pkg/blob"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/metrics"
	"github.com/zkrebbekx/flexitype/pkg/ratelimit"
	"github.com/zkrebbekx/flexitype/pkg/safedial"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// Service is an embedded flexitype instance.
type Service struct {
	pool       *sqlx.DB
	transactor db.Transactor
	dispatcher *events.Dispatcher
	factory    application.Factory
	relay      *outbox.Relay
	indexer    *search.Indexer
	worker     *webhook.Worker
	pruner     *feed.Pruner
	blobs      blob.Store
}

type options struct {
	dispatcher          *events.Dispatcher
	onRollback          func(ctx context.Context, err error)
	features            application.Features
	outbox              bool
	relayOpts           []outbox.RelayOption
	workerOpts          []webhook.WorkerOption
	retention           time.Duration
	webhookAllowPrivate bool
	searchIndex         bool
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

// WithSearchIndex enables the entity search projection: a dispatcher
// subscriber keeps one searchable document per entity, unlocking FQL
// matches(). Pair with WithOutbox for at-least-once index maintenance.
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

	transactor := db.NewTransactor(pool)
	newRepos := func() application.Repositories { return postgres.NewRepositories(pool) }

	var indexer *search.Indexer
	if o.searchIndex {
		indexer = search.NewIndexer(newRepos, postgres.NewSearchStore(pool))
		o.dispatcher.Register(indexer, events.WithEventTypes(search.EventTypes()...))
	}

	var relay *outbox.Relay
	var worker *webhook.Worker
	var pruner *feed.Pruner
	cfg := application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: newRepos,
		Dispatcher:      o.dispatcher,
		ActivityLog:     postgres.NewActivityLog(pool),
		OnRollback:      o.onRollback,
		Features:        o.features,
		SavedViews:      postgres.NewSavedViewStore(pool),
		MatchRules:      postgres.NewMatchStore(pool),
		Revisions:       postgres.NewRevisionStore(pool),
		ChangeSets:      postgres.NewChangeSetStore(pool),
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
		pruner = feed.NewPruner(feedStore, retention, nil)

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
	// Computed attributes materialize via an event subscriber, so their
	// derived values are ordinary (FQL-queryable) values.
	o.dispatcher.Register(computed.NewMaterializer(factory), events.WithEventTypes(computed.EventTypes()...))

	return &Service{
		pool:       pool,
		transactor: transactor,
		dispatcher: o.dispatcher,
		factory:    factory,
		relay:      relay,
		indexer:    indexer,
		worker:     worker,
		pruner:     pruner,
		blobs:      o.blobs,
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

	store := memory.NewStore()
	newRepos := func() application.Repositories { return store.Repositories() }
	savedViews := memory.NewSavedViewStore()
	matchRules := memory.NewMatchStore()
	revisions := memory.NewRevisionStore()
	changesets := memory.NewChangeSetStore()
	// The playground gets a working, process-local media store by default.
	if o.blobs == nil {
		o.blobs = blob.NewMemoryStore()
	}

	var indexer *search.Indexer
	if o.searchIndex {
		indexer = search.NewIndexer(newRepos, store.SearchStore())
		o.dispatcher.Register(indexer, events.WithEventTypes(search.EventTypes()...))
	}

	transactor := store.Transactor()
	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: newRepos,
		Dispatcher:      o.dispatcher,
		ActivityLog:     store.ActivityLog(),
		OnRollback:      o.onRollback,
		Features:        o.features,
		SavedViews:      savedViews,
		MatchRules:      matchRules,
		Revisions:       revisions,
		ChangeSets:      changesets,
		BlobStore:       o.blobs,
	})
	o.dispatcher.Register(computed.NewMaterializer(factory), events.WithEventTypes(computed.EventTypes()...))
	return &Service{
		transactor: transactor,
		dispatcher: o.dispatcher,
		factory:    factory,
		indexer:    indexer,
		blobs:      o.blobs,
	}
}

// RunOutboxRelay runs the event-delivery machinery until ctx ends: the
// outbox relay (expansion + in-process dispatch), the webhook delivery
// worker and the retention pruner. No-op without WithOutbox. Run it as a
// goroutine next to the server; every replica runs it safely.
func (s *Service) RunOutboxRelay(ctx context.Context) {
	if s.relay == nil {
		return
	}
	// Block until all three loops have observed ctx cancellation and
	// returned, so shutdown can be ordered around this call: the relay,
	// delivery worker and pruner are fully stopped before the pool or
	// broker clients they depend on are closed.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); s.worker.Run(ctx) }()
	go func() { defer wg.Done(); s.pruner.Run(ctx) }()
	s.relay.Run(ctx)
	wg.Wait()
}

// RunChangeSetScheduler publishes approved change-sets whose publish_at has
// arrived, on the given interval, until ctx ends. Run it as a goroutine next
// to the server; every replica runs it safely (a published set is skipped by
// the others). A zero interval defaults to one minute.
func (s *Service) RunChangeSetScheduler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cs := s.factory.New(ctx).ChangeSets()
			if cs != nil {
				_, _ = cs.PublishDue(ctx)
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
	return s.indexer.Reindex(ctx, tenant)
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

// Interactors returns a request-scoped usecase set. Call once per request
// or unit of work so dataloader caches stay request-local.
func (s *Service) Interactors(ctx context.Context) *application.Interactors {
	return s.factory.New(ctx)
}

// Factory exposes the underlying usecase factory for advanced wiring.
func (s *Service) Factory() application.Factory { return s.factory }

// Dispatcher exposes the event dispatcher, e.g. to register hooks after
// construction.
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
func (s *Service) AdminInteractor() *admin.Interactor {
	if s.pool == nil {
		return nil
	}
	return admin.NewInteractor(postgres.NewAdminStore(s.pool))
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

	if existing, err := a.ListAccounts(ctx, tenantName); err == nil && len(existing) > 0 {
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
	}
	if cfg.EnableProvisioning {
		server.Admin = s.AdminInteractor()
	}
	if s.indexer != nil {
		server.Reindex = s.ReindexSearch
	}
	return httpapi.NewHandler(server)
}
