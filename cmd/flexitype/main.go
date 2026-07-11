// The flexitype standalone service: the composition root wiring the
// PostgreSQL pool, usecase factory, event hooks, service-account auth and
// the REST API behind OpenTelemetry tracing, health endpoints and graceful
// shutdown (docs in README.md).
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/pubsub/v2"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/zkrebbekx/flexitype"
	"github.com/zkrebbekx/flexitype/application/outbox"
	"github.com/zkrebbekx/flexitype/application/webhook"
	"github.com/zkrebbekx/flexitype/infrastructure/gcppubsub"
	"github.com/zkrebbekx/flexitype/pkg/config"
	"github.com/zkrebbekx/flexitype/pkg/events"
	"github.com/zkrebbekx/flexitype/pkg/health"
	"github.com/zkrebbekx/flexitype/pkg/logger"
	"github.com/zkrebbekx/flexitype/pkg/serviceaccount"
	"github.com/zkrebbekx/flexitype/pkg/shutdown"
	"github.com/zkrebbekx/flexitype/pkg/telemetry"
)

// version is the service's reported version (health + traces).
const version = "1.0.0"

func main() {
	log := logger.New(logger.Config{
		Level:  os.Getenv("FLEXITYPE_LOG_LEVEL"),
		Format: os.Getenv("FLEXITYPE_LOG_FORMAT"),
	})
	if err := run(log); err != nil {
		log.Fatal().Err(err).Msg("application error")
	}
}

func run(log *logger.Logger) error {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Tracing first so the handlers below pick up the global provider.
	// No-op unless OTEL_EXPORTER_OTLP_ENDPOINT is set; export is batched
	// and never blocks serving.
	otelShutdown, err := telemetry.Init(ctx, "flexitype", version)
	if err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}

	pool, err := connectDB(cfg.Database)
	if err != nil {
		return err
	}

	// Client hooks for the standalone service come from the environment:
	// a signed webhook endpoint is the zero-code integration; embedded
	// deployments register richer hooks (pub/sub, funcs) in Go. With the
	// outbox on, the env webhook becomes a managed subscription instead
	// (retries, backoff, dead-lettering) — see the bootstrap below.
	var opts []flexitype.Option
	envWebhookURL := os.Getenv("FLEXITYPE_WEBHOOK_URL")
	if envWebhookURL != "" && !cfg.EnableOutbox {
		opts = append(opts, flexitype.WithWebhook("env-webhook", events.WebhookConfig{
			URL:    envWebhookURL,
			Secret: os.Getenv("FLEXITYPE_WEBHOOK_SECRET"),
		}))
		log.Info().Str("url", envWebhookURL).Msg("event webhook registered")
	}
	// Google Cloud Pub/Sub: every event publishes to one topic with
	// filterable attributes. Prefer this over raw webhooks when consumers
	// already live on GCP — Pub/Sub brings its own consumer groups,
	// replay and dead-letter topics.
	var pubsubClient *pubsub.Client
	if cfg.PubSubProject != "" {
		pubsubClient, err = pubsub.NewClient(ctx, cfg.PubSubProject)
		if err != nil {
			return fmt.Errorf("connect pub/sub: %w", err)
		}
		publisher := pubsubClient.Publisher(cfg.PubSubTopic)
		var pubsubOpts []gcppubsub.Option
		if cfg.PubSubOrdering {
			publisher.EnableMessageOrdering = true
			pubsubOpts = append(pubsubOpts, gcppubsub.WithOrderingKey(gcppubsub.PerAggregate))
		}
		opts = append(opts, flexitype.WithHandler(gcppubsub.New("gcp-pubsub", publisher, pubsubOpts...)))
		log.Info().Str("project", cfg.PubSubProject).Str("topic", cfg.PubSubTopic).
			Bool("ordering", cfg.PubSubOrdering).Msg("gcp pub/sub publisher registered")
	}

	opts = append(opts, flexitype.WithRollbackObserver(func(_ context.Context, err error) {
		log.Warn().Err(err).Msg("unit of work rolled back")
	}))
	if !cfg.EnableSearch {
		opts = append(opts, flexitype.WithoutSearch())
		log.Info().Msg("search feature disabled")
	}
	if !cfg.EnableActivity {
		opts = append(opts, flexitype.WithoutActivityLog())
		log.Info().Msg("activity history disabled")
	}
	if cfg.EnableOutbox {
		opts = append(opts, flexitype.WithOutbox(outbox.WithErrorObserver(func(err error) {
			log.Error().Err(err).Msg("outbox relay error")
		})))
		opts = append(opts, flexitype.WithDeliveryWorker(webhook.WithWorkerErrorObserver(func(err error) {
			log.Error().Err(err).Msg("webhook delivery worker error")
		})))
		if cfg.EventRetention > 0 {
			opts = append(opts, flexitype.WithEventRetention(cfg.EventRetention))
		}
		if cfg.WebhookAllowPrivate {
			opts = append(opts, flexitype.WithWebhookAllowPrivate())
		}
		log.Info().Msg("transactional outbox enabled; event delivery API active")
	}
	if cfg.EnableSearchIndex {
		opts = append(opts, flexitype.WithSearchIndex())
		log.Info().Msg("search index enabled")
	}

	svc := flexitype.New(pool, opts...)

	if cfg.MigrateOnStart {
		if err := svc.Migrate(ctx); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		log.Info().Msg("schema migrations applied")
	}

	if envWebhookURL != "" && cfg.EnableOutbox {
		if err := svc.EnsureWebhookSubscription(ctx, "env-webhook", envWebhookURL,
			os.Getenv("FLEXITYPE_WEBHOOK_SECRET")); err != nil {
			return fmt.Errorf("bootstrap env webhook subscription: %w", err)
		}
		log.Info().Str("url", envWebhookURL).Msg("event webhook subscription ensured")
	}

	// Service accounts: no file means development mode with auth disabled.
	var accounts *serviceaccount.Store
	if cfg.ServiceAccountsPath != "" {
		accounts, err = serviceaccount.LoadFile(cfg.ServiceAccountsPath)
		if err != nil {
			return fmt.Errorf("load service accounts: %w", err)
		}
		log.Info().Str("path", cfg.ServiceAccountsPath).Msg("service accounts loaded")
	} else {
		log.Warn().Msg("no service accounts configured; authentication disabled")
	}

	healthChecker := health.NewService("flexitype", version)
	healthChecker.RegisterCheckFunc("database", func(ctx context.Context) error {
		return pool.PingContext(ctx)
	})

	handler := svc.APIHandler(flexitype.APIConfig{
		Logger:   log,
		Health:   healthChecker,
		Accounts: accounts,
	})

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	shutdownHandler := shutdown.New(shutdown.Config{Logger: log, Timeout: cfg.ShutdownTimeout})
	// Drain the server first (90), flush buffered spans while the pool is
	// still up (50), close the database last (10).
	shutdownHandler.RegisterTask(shutdown.Task{
		Name:     "http-server",
		Priority: 90,
		Handler:  server.Shutdown,
	})
	shutdownHandler.RegisterTask(shutdown.Task{
		Name:     "telemetry",
		Priority: 50,
		Handler:  otelShutdown,
	})
	shutdownHandler.RegisterTask(shutdown.Task{
		Name:     "database",
		Priority: 10,
		Handler:  func(context.Context) error { return pool.Close() },
	})
	if pubsubClient != nil {
		// After the relay stops (60): no publishes remain in flight.
		shutdownHandler.RegisterTask(shutdown.Task{
			Name:     "pubsub",
			Priority: 40,
			Handler:  func(context.Context) error { return pubsubClient.Close() },
		})
	}

	// The outbox relay, delivery worker and pruner drain committed events
	// to hooks. On shutdown (priority 60, before pub/sub at 40 and the
	// pool at 10) we cancel their context and wait for them to fully stop,
	// so no publish or query fires against an already-closed client.
	relayCtx, relayCancel := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		svc.RunOutboxRelay(relayCtx)
	}()
	shutdownHandler.RegisterTask(shutdown.Task{
		Name:     "outbox-relay",
		Priority: 60,
		Handler: func(ctx context.Context) error {
			relayCancel()
			select {
			case <-relayDone:
			case <-ctx.Done():
			}
			return nil
		},
	})

	errCh := make(chan error, 1)
	go func() {
		log.Info().Int("port", cfg.Port).Msg("flexitype listening")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	serveCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		if err := <-errCh; err != nil {
			log.Error().Err(err).Msg("server error")
			cancel()
		}
	}()

	shutdownHandler.Wait(serveCtx)
	return nil
}

// connectDB opens and verifies the PostgreSQL pool.
func connectDB(cfg config.Database) (*sqlx.DB, error) {
	pool, err := sqlx.Connect("postgres", cfg.DSN())
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	pool.SetMaxOpenConns(cfg.MaxOpenConns)
	pool.SetMaxIdleConns(cfg.MaxIdleConns)
	pool.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	return pool, nil
}
