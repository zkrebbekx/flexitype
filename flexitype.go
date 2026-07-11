// Package flexitype is the embedding facade: everything needed to run
// flexitype inside your own Go service — usecases, storage, migrations,
// domain events — wired through one constructor with hook options for your
// pub/sub, webhooks or plain functions. For the standalone service, see
// cmd/flexitype.
package flexitype

import (
	"context"
	"net/http"

	"github.com/jmoiron/sqlx"

	"github.com/zkrebbekx/flexitype/application"
	"github.com/zkrebbekx/flexitype/infrastructure/postgres"
	httpapi "github.com/zkrebbekx/flexitype/internal/interfaces/http"
	"github.com/zkrebbekx/flexitype/pkg/db"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
)

// Service is an embedded flexitype instance.
type Service struct {
	pool       *sqlx.DB
	transactor db.Transactor
	dispatcher *events.Dispatcher
	factory    application.Factory
}

type options struct {
	dispatcher *events.Dispatcher
	onRollback func(ctx context.Context, err error)
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

// New wires an embedded flexitype over your connection pool. The pool is
// shared, never owned: closing it remains your call.
func New(pool *sqlx.DB, opts ...Option) *Service {
	o := &options{dispatcher: events.NewDispatcher()}
	for _, opt := range opts {
		opt(o)
	}

	transactor := db.NewTransactor(pool)
	factory := application.NewFactory(application.FactoryConfig{
		Transactor:      transactor,
		NewRepositories: func() application.Repositories { return postgres.NewRepositories(pool) },
		Dispatcher:      o.dispatcher,
		ActivityLog:     postgres.NewActivityLog(pool),
		OnRollback:      o.onRollback,
	})

	return &Service{
		pool:       pool,
		transactor: transactor,
		dispatcher: o.dispatcher,
		factory:    factory,
	}
}

// Migrate applies flexitype's embedded schema migrations. Safe to call on
// every startup; concurrent callers serialize on an advisory lock.
func (s *Service) Migrate(ctx context.Context) error {
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
	Logger   *logger.Logger
	Health   *health.Service
	Accounts *serviceaccount.Store // nil disables auth
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
	return httpapi.NewHandler(httpapi.ServerConfig{
		Factory:  s.factory,
		Logger:   cfg.Logger,
		Health:   cfg.Health,
		Accounts: cfg.Accounts,
	})
}
