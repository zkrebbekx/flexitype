// Command storefront is the shopper side of the marketplace example, deployed
// PER MERCHANT.
//
// One instance serves one merchant: it holds that merchant's credential, keeps
// that merchant's catalogue, and refuses anything that is not its merchant. A
// service-account token IS a tenant, so a storefront built this way needs no
// privilege that crosses a tenant boundary — an earlier version of this
// example aggregated every merchant, which needed one.
//
// It keeps its OWN denormalized catalogue in Postgres, fed by flexitype's
// signed webhooks, and answers shoppers from it: a shopper page is read-heavy
// with its own ranking, and a projection is what makes it cheap.
//
// The three moving parts:
//
//   - ingest.go verifies a delivery's HMAC signature and coalesces a burst of
//     value events for one entity into one projection.
//   - projector.go re-reads the entity's whole value set with that merchant's
//     client and overwrites one row. It never applies an event payload, which
//     is what makes it idempotent and order-independent.
//   - backfill.go walks a merchant's products with FQL, for everything that
//     existed before the subscription. It is safe to re-run.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/lib/pq"
)

// Logger is the service's structured logger. A credential is never an
// argument to it: the merchant tokens this service holds are logged by tenant
// name only.
type Logger = *slog.Logger

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("storefront stopped", "error", err)
		os.Exit(1)
	}
}

func run(log Logger) error {
	addr := envOr("STOREFRONT_ADDR", ":9200")
	dsn := os.Getenv("STOREFRONT_DB_DSN")
	if dsn == "" {
		return errors.New("STOREFRONT_DB_DSN is required")
	}
	flexitypeURL := envOr("FLEXITYPE_URL", "http://flexitype:8080")
	// The ONE merchant this storefront serves. A storefront is deployed per
	// merchant, so it holds one credential and answers for one catalog.
	tenant := os.Getenv("STOREFRONT_TENANT")
	if tenant == "" {
		return errors.New("STOREFRONT_TENANT is required: a storefront serves exactly one merchant")
	}
	// The address a SHOPPER's browser reaches flexitype on. A signed image
	// link is redeemed by the browser, so the container-network URL this
	// process uses would not resolve for it. Empty falls back to proxying the
	// bytes through this service.
	mediaBase := envOr("STOREFRONT_MEDIA_PUBLIC_BASE", "")
	internalToken := os.Getenv("MARKETPLACE_INTERNAL_TOKEN")
	if internalToken == "" {
		return errors.New("MARKETPLACE_INTERNAL_TOKEN is required: it gates merchant registration and backfill")
	}
	debounce := envDuration("STOREFRONT_DEBOUNCE", 250*time.Millisecond)

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(envInt("STOREFRONT_DB_MAX_CONNS", 10))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := waitForDB(ctx, db, 60*time.Second); err != nil {
		return err
	}

	store := NewStore(db, envOr("STOREFRONT_DB_SCHEMA", "storefront"))
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	projector := NewProjector(store, flexitypeURL, 30*time.Second)
	debouncer := NewDebouncer(debounce, func(ctx context.Context, key entityKey) error {
		return projector.Project(ctx, key.Tenant, key.TypeID, key.EntityID)
	}, log)
	ingest := NewIngest(tenant, store, debouncer, log)
	api := NewAPI(tenant, store, projector, internalToken, mediaBase, log)

	srv := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(ingest),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("storefront listening", "addr", addr, "flexitype", flexitypeURL, "debounce", debounce.String())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Drain the open debounce windows, so a product written a moment before
	// shutdown still lands in the projection.
	debouncer.Wait()
	log.Info("storefront stopped cleanly")
	return nil
}

// waitForDB blocks until Postgres answers. The compose stack starts every
// service at once, so the first connection normally loses the race.
func waitForDB(ctx context.Context, db *sql.DB, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := db.PingContext(ctx); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("database did not become reachable in time")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil && v >= 0 {
		return v
	}
	return fallback
}
