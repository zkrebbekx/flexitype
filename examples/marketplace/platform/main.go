// Command platform is the merchant-facing half of the marketplace example.
//
// It onboards a merchant — creating the flexitype TENANT, a service account
// scoped to it, applying the `ecommerce` starter template, registering the
// storefront's webhook subscription and triggering the first projection
// backfill — and then serves a thin merchant API over that merchant's own
// flexitype client.
//
// The one thing it adds over calling flexitype directly is that it HOLDS the
// service-account token. A browser cannot be given a credential that reads and
// writes a merchant's whole catalog.
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
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"

	"github.com/zkrebbekx/flexitype/client"
)

// Logger is the service's structured logger. A merchant token is never an
// argument to it.
type Logger = *slog.Logger

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("platform stopped", "error", err)
		os.Exit(1)
	}
}

func run(log Logger) error {
	addr := envOr("PLATFORM_ADDR", ":9300")
	dsn := os.Getenv("PLATFORM_DB_DSN")
	if dsn == "" {
		return errors.New("PLATFORM_DB_DSN is required")
	}
	flexitypeURL := envOr("FLEXITYPE_URL", "http://flexitype:8080")
	storefrontURL := envOr("STOREFRONT_URL", "http://storefront:9200")
	hookBase := envOr("STOREFRONT_HOOK_BASE", storefrontURL+"/hook")
	internalToken := os.Getenv("MARKETPLACE_INTERNAL_TOKEN")
	if internalToken == "" {
		return errors.New("MARKETPLACE_INTERNAL_TOKEN is required: it authenticates this service to the storefront")
	}
	apiToken := os.Getenv("PLATFORM_API_TOKEN")
	if apiToken == "" {
		return errors.New("PLATFORM_API_TOKEN is required: it authenticates the merchant console")
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(envInt("PLATFORM_DB_MAX_CONNS", 10))

	if err := waitForDB(ctx, db, 60*time.Second); err != nil {
		return err
	}
	store := NewStore(db, envOr("PLATFORM_DB_SCHEMA", "platform"))
	if err := store.Migrate(ctx); err != nil {
		return err
	}

	adminTokens, err := newAdminTokenSource(ctx, log)
	if err != nil {
		return err
	}
	// The admin credential is presented per request rather than captured once,
	// so a rotated secret is picked up without a restart.
	admin, err := client.New(flexitypeURL,
		client.WithUserAgent("marketplace-platform-admin"),
		client.WithHTTPClient(&http.Client{
			Timeout:   60 * time.Second,
			Transport: &bearerTransport{source: adminTokens},
		}))
	if err != nil {
		return err
	}

	onboarder := NewOnboarder(store, admin, flexitypeURL,
		NewStorefrontClient(storefrontURL, internalToken), hookBase, log)
	api := NewAPI(store, onboarder, apiToken, flexitypeURL, log)

	srv := &http.Server{Addr: addr, Handler: api.Handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("platform listening", "addr", addr, "flexitype", flexitypeURL, "storefront", storefrontURL)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	log.Info("platform stopped cleanly")
	return nil
}

// clientCache holds one flexitype client per merchant.
//
// The cache is keyed by the token as well as the merchant id, so a rotated
// credential builds a new client instead of reusing the revoked one.
type clientCache struct {
	store   *Store
	baseURL string
	mu      sync.Mutex
	clients map[string]*client.Client
}

func newClientCache(store *Store, baseURL string) *clientCache {
	return &clientCache{store: store, baseURL: baseURL, clients: map[string]*client.Client{}}
}

func (c *clientCache) get(m Merchant) (*client.Client, error) {
	key := m.ID + "\x00" + m.Token
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.clients[key]; ok {
		return cl, nil
	}
	cl, err := client.New(c.baseURL, client.WithToken(m.Token), client.WithUserAgent("marketplace-platform"))
	if err != nil {
		return nil, err
	}
	// Drop any client built for an older token of this merchant.
	for existing := range c.clients {
		if strings.HasPrefix(existing, m.ID+"\x00") {
			delete(c.clients, existing)
		}
	}
	c.clients[key] = cl
	return cl, nil
}

// waitForDB blocks until Postgres answers. Compose starts every service at
// once, so the first connection normally loses the race.
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
