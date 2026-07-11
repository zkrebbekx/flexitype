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

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"

	"github.com/zkrebbekx/flexitype"
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
	// deployments register richer hooks (pub/sub, funcs) in Go.
	var opts []flexitype.Option
	if url := os.Getenv("FLEXITYPE_WEBHOOK_URL"); url != "" {
		opts = append(opts, flexitype.WithWebhook("env-webhook", events.WebhookConfig{
			URL:    url,
			Secret: os.Getenv("FLEXITYPE_WEBHOOK_SECRET"),
		}))
		log.Info().Str("url", url).Msg("event webhook registered")
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

	svc := flexitype.New(pool, opts...)

	if cfg.MigrateOnStart {
		if err := svc.Migrate(ctx); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
		log.Info().Msg("schema migrations applied")
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
