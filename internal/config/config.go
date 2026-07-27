// Package config loads flexitype's service configuration from FLEXITYPE_*
// environment variables — twelve-factor style, no config files required.
package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"
)

// Config is the standalone service configuration.
type Config struct {
	// Port the HTTP server listens on.
	Port int
	// Database connection settings.
	Database Database
	// ServiceAccountsPath points at the service-account JSON file. Empty
	// disables authentication (development only).
	ServiceAccountsPath string
	// DevInsecure is the explicit opt-out from the production guards: it turns
	// authentication off, and permits an unencrypted database connection to a
	// non-loopback host.
	//
	// It exists so the insecure configuration has to be asked for by name, in
	// a manifest, rather than being what a deployment gets by forgetting a
	// variable. It reads as deliberate at a glance, which "REQUIRE_AUTH is
	// absent" did not.
	DevInsecure bool

	// RequireAuth refuses to boot unless an account source is configured
	// (a service-account file or provisioning). It turns the default
	// fail-open "no accounts → auth disabled" behaviour into a hard error,
	// so production cannot accidentally serve the API unauthenticated.
	RequireAuth bool
	// LogLevel and LogFormat feed the logger.
	LogLevel  string
	LogFormat string
	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration
	// EnableConsole serves the embedded admin console. Turn it off for an
	// API-only deployment: the SPA is then not mounted at all, and an unknown
	// path returns a JSON 404 rather than the app shell.
	EnableConsole bool
	// MaxImportBytes caps a CSV import upload; 0 uses the server default
	// (16 MiB). Raise it for a bulk onboarding rather than forking.
	MaxImportBytes int64
	// MaxMediaBytes caps a media upload; 0 uses the server default (32 MiB).
	MaxMediaBytes int64

	// RunRelay, RunDeliveryWorker, RunPruner and RunScheduler select which
	// background loops THIS process runs. All default to true, so a
	// single-process deployment is unchanged; set them per replica set to
	// split an API tier from a worker tier.
	//
	// No leader election is needed either way: every loop claims work with a
	// lease and FOR UPDATE SKIP LOCKED, so running one is safe on any number
	// of replicas. The switches exist so ten API replicas do not each poll the
	// outbox, and so the two tiers can be scaled and drained separately.
	RunRelay          bool
	RunDeliveryWorker bool
	RunPruner         bool
	RunScheduler      bool

	// MigrateOnStart applies embedded migrations during boot.
	MigrateOnStart bool
	// EnableSearch toggles the FQL query surface.
	EnableSearch bool
	// EnableActivity toggles the audit log (writes and read API).
	EnableActivity bool
	// EnableOutbox switches event delivery to the transactional outbox.
	EnableOutbox bool
	// EnableSearchIndex maintains the entity search projection and unlocks
	// FQL matches().
	EnableSearchIndex bool
	// BlobDir, when set, enables media attribute storage on local disk
	// rooted at this directory.
	BlobDir string
	// EventRetention is how long expanded events stay readable in the
	// events feed before pruning (outbox mode only).
	EventRetention time.Duration
	// WebhookAllowPrivate lets webhook subscriptions target private hosts
	// (on-prem consumers). Off by default — SSRF guard.
	WebhookAllowPrivate bool
	// EnableMetrics serves Prometheus SLIs at /metrics.
	EnableMetrics bool
	// EnableProvisioning turns on the admin-scoped tenant/service-account
	// API and database-backed authentication.
	EnableProvisioning bool
	// AuthCacheTTL bounds how long a database-backed auth result is cached
	// (and thus how quickly revocation takes effect).
	AuthCacheTTL time.Duration
	// BootstrapAdmin seeds a first admin account when provisioning is on
	// and the account store is empty.
	BootstrapAdmin bool
	// RateLimitRPS is the sustained per-account request rate; 0 disables
	// rate limiting.
	RateLimitRPS float64
	// RateLimitBurst is the token-bucket ceiling for short bursts.
	RateLimitBurst int
	// TenantRateLimitRPS caps a tenant's aggregate request rate across all of
	// its service accounts; 0 disables the aggregate ceiling.
	//
	// The per-account limiter alone cannot bound a tenant: a tenant that
	// creates more accounts multiplies its effective rate by the account
	// count, and the per-account buckets have no view of the total.
	TenantRateLimitRPS float64
	// TenantRateLimitBurst is the aggregate token-bucket ceiling.
	TenantRateLimitBurst int
	// PubSubProject, when set, publishes every event to Google Cloud
	// Pub/Sub in addition to any webhook subscriptions.
	PubSubProject string
	// PubSubTopic is the Pub/Sub topic id events publish to.
	PubSubTopic string
	// PubSubOrdering stamps per-aggregate ordering keys (the topic's
	// subscriptions must enable message ordering to benefit).
	PubSubOrdering bool
}

// Database holds PostgreSQL pool settings.
type Database struct {
	Host            string
	Port            int
	User            string
	Password        string
	Name            string
	SSLMode         string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// DSN renders the lib/pq connection string.
func (d Database) DSN() string {
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode)
}

// Load reads configuration from the environment with production-safe
// defaults.
func Load() (Config, error) {
	e := &envReader{}
	cfg := Config{
		Port:                 e.int("FLEXITYPE_PORT", 8080),
		ServiceAccountsPath:  os.Getenv("FLEXITYPE_SERVICE_ACCOUNTS"),
		DevInsecure:          e.bool("FLEXITYPE_DEV_INSECURE", false),
		RequireAuth:          e.bool("FLEXITYPE_REQUIRE_AUTH", true),
		EnableConsole:        e.bool("FLEXITYPE_ENABLE_CONSOLE", true),
		MaxImportBytes:       int64(e.int("FLEXITYPE_MAX_IMPORT_BYTES", 0)),
		MaxMediaBytes:        int64(e.int("FLEXITYPE_MAX_MEDIA_BYTES", 0)),
		RunRelay:             e.bool("FLEXITYPE_RUN_RELAY", true),
		RunDeliveryWorker:    e.bool("FLEXITYPE_RUN_DELIVERY_WORKER", true),
		RunPruner:            e.bool("FLEXITYPE_RUN_PRUNER", true),
		RunScheduler:         e.bool("FLEXITYPE_RUN_SCHEDULER", true),
		LogLevel:             envStr("FLEXITYPE_LOG_LEVEL", "info"),
		LogFormat:            envStr("FLEXITYPE_LOG_FORMAT", "json"),
		ShutdownTimeout:      e.duration("FLEXITYPE_SHUTDOWN_TIMEOUT", 30*time.Second),
		MigrateOnStart:       e.bool("FLEXITYPE_MIGRATE_ON_START", true),
		EnableSearch:         e.bool("FLEXITYPE_FEATURE_SEARCH", true),
		EnableActivity:       e.bool("FLEXITYPE_FEATURE_ACTIVITY", true),
		EnableOutbox:         e.bool("FLEXITYPE_OUTBOX", false),
		EnableSearchIndex:    e.bool("FLEXITYPE_FEATURE_SEARCH_INDEX", false),
		BlobDir:              os.Getenv("FLEXITYPE_BLOB_DIR"),
		EventRetention:       e.duration("FLEXITYPE_EVENT_RETENTION", 7*24*time.Hour),
		WebhookAllowPrivate:  e.bool("FLEXITYPE_WEBHOOK_ALLOW_PRIVATE", false),
		EnableMetrics:        e.bool("FLEXITYPE_METRICS", true),
		EnableProvisioning:   e.bool("FLEXITYPE_PROVISIONING", false),
		AuthCacheTTL:         e.duration("FLEXITYPE_AUTH_CACHE_TTL", 30*time.Second),
		BootstrapAdmin:       e.bool("FLEXITYPE_BOOTSTRAP_ADMIN", false),
		RateLimitRPS:         e.float("FLEXITYPE_RATE_LIMIT_RPS", 50),
		RateLimitBurst:       e.int("FLEXITYPE_RATE_LIMIT_BURST", 200),
		TenantRateLimitRPS:   e.float("FLEXITYPE_TENANT_RATE_LIMIT_RPS", 500),
		TenantRateLimitBurst: e.int("FLEXITYPE_TENANT_RATE_LIMIT_BURST", 2000),
		PubSubProject:        os.Getenv("FLEXITYPE_PUBSUB_PROJECT"),
		PubSubTopic:          envStr("FLEXITYPE_PUBSUB_TOPIC", "flexitype-events"),
		PubSubOrdering:       e.bool("FLEXITYPE_PUBSUB_ORDERING", false),
		Database: Database{
			Host:            envStr("FLEXITYPE_DB_HOST", "localhost"),
			Port:            e.int("FLEXITYPE_DB_PORT", 5432),
			User:            envStr("FLEXITYPE_DB_USER", "postgres"),
			Password:        envStr("FLEXITYPE_DB_PASSWORD", "postgres"),
			Name:            envStr("FLEXITYPE_DB_NAME", "flexitype"),
			SSLMode:         envStr("FLEXITYPE_DB_SSLMODE", "disable"),
			MaxOpenConns:    e.int("FLEXITYPE_DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns:    e.int("FLEXITYPE_DB_MAX_IDLE_CONNS", 10),
			ConnMaxLifetime: e.duration("FLEXITYPE_DB_CONN_MAX_LIFETIME", 30*time.Minute),
		},
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return Config{}, fmt.Errorf("invalid FLEXITYPE_PORT %d", cfg.Port)
	}
	// Authentication is required by default, and the opt-out is a variable of
	// its own rather than RequireAuth=false. The failure this prevents is an
	// omission, not a mistake: a deployment that forgets one variable served
	// the entire multi-tenant API to anonymous callers with admin access —
	// including POST /api/v1/admin/purge, the irreversible hard delete — while
	// every symptom of correct operation was present. A configuration error
	// must cause the service to refuse traffic, not to serve it with maximum
	// privilege.
	if len(e.errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(e.errs...))
	}
	if cfg.DevInsecure {
		cfg.RequireAuth = false
	}
	if cfg.RequireAuth && cfg.ServiceAccountsPath == "" && !cfg.EnableProvisioning {
		return Config{}, fmt.Errorf(
			"no account source is configured: set FLEXITYPE_SERVICE_ACCOUNTS or FLEXITYPE_PROVISIONING=true. " +
				"To run without authentication — which serves the whole API, including the irreversible " +
				"admin purge, to anonymous callers — set FLEXITYPE_DEV_INSECURE=true explicitly")
	}
	// Unencrypted database traffic is only tolerated to a loopback host (local
	// dev / a sidecar). A non-loopback host with sslmode=disable would send
	// credentials and data in the clear, so refuse it.
	//
	// The dev opt-out exists because a container-network hostname is not
	// loopback but is also not production — the compose quickstart connects to
	// a host called "postgres", which this guard refused, so the documented
	// first-touch experience crash-looped on config validation. Treating
	// RFC1918 or container hostnames as loopback would have been too broad.
	if cfg.Database.SSLMode == "disable" && !isLoopbackHost(cfg.Database.Host) && !cfg.DevInsecure {
		return Config{}, fmt.Errorf(
			"FLEXITYPE_DB_SSLMODE=disable is not allowed for non-loopback host %q; use require/verify-full, "+
				"or set FLEXITYPE_DEV_INSECURE=true for a local development stack", cfg.Database.Host)
	}
	if len(e.errs) > 0 {
		return Config{}, fmt.Errorf("invalid configuration: %w", errors.Join(e.errs...))
	}
	return cfg, nil
}

func envStr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// envReader reads typed environment variables, collecting parse errors so a
// malformed value fails startup loudly instead of silently reverting to the
// default — which could, for example, leave the outbox off (losing durability)
// when the operator typed FLEXITYPE_OUTBOX=ture.
type envReader struct{ errs []error }

func (e *envReader) int(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("invalid %s=%q: must be an integer", key, v))
		return fallback
	}
	return n
}

func (e *envReader) bool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("invalid %s=%q: must be a boolean (true/false)", key, v))
		return fallback
	}
	return b
}

func (e *envReader) float(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("invalid %s=%q: must be a number", key, v))
		return fallback
	}
	return f
}

func (e *envReader) duration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		e.errs = append(e.errs, fmt.Errorf("invalid %s=%q: must be a duration (e.g. 30s, 5m)", key, v))
		return fallback
	}
	return d
}

// isLoopbackHost reports whether host is a loopback address or "localhost".
func isLoopbackHost(host string) bool {
	if host == "localhost" || host == "" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
