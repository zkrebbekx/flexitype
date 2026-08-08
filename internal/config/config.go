// Package config loads flexitype's service configuration from FLEXITYPE_*
// environment variables — twelve-factor style, no config files required.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"
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

	// DBAllowPlaintext permits an unencrypted database connection to a
	// non-loopback host, and nothing else.
	//
	// It exists because DevInsecure conflated two unrelated decisions. A stack
	// that needs plaintext Postgres over a container network — the compose
	// quickstart connects to a host called "postgres" — had to set the
	// variable that ALSO reads as "authentication off", and whose warning says
	// so. An operator then read a warning that was wrong for that deployment.
	//
	// DevInsecure still implies this one, so no existing manifest breaks.
	DBAllowPlaintext bool

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
	// TimeZone is the IANA zone whose calendar day `today` and `now` resolve
	// against in dependency conditions and dynamic defaults. Empty means UTC,
	// which is what the system did before there was a setting at all.
	TimeZone string

	// Delivery-machinery tuning. The options existed in code and none of
	// them was reachable from a deployment, so the one interaction that
	// actually needs correcting — a lease shorter than the time a batch
	// takes to drain — could not be corrected without rebuilding.
	//
	// Each 0 keeps the library default.
	RelayInterval      time.Duration
	RelayBatchSize     int
	RelayLeaseTTL      time.Duration
	OutboxMaxAttempts  int
	OutboxRetryCeiling time.Duration
	WorkerInterval     time.Duration
	WorkerConcurrency  int
	WorkerMaxAttempts  int
	WorkerHTTPTimeout  time.Duration

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
	// EnableGraphQLFederation exposes the GraphQL endpoint as an
	// Apollo-Federation subgraph (_service, _entities, @key).
	EnableGraphQLFederation bool
	// BlobDir, when set, enables media attribute storage on local disk
	// rooted at this directory.
	BlobDir string
	// EventRetention is how long expanded events stay readable in the
	// events feed before pruning (outbox mode only).
	EventRetention time.Duration
	// DeadLetterRetention bounds how long a dead delivery is kept before it
	// stops pinning its envelope. Far longer than EventRetention: a dead
	// letter is the record of a delivery that never succeeded, and it has to
	// outlive the events it references long enough to be noticed.
	DeadLetterRetention time.Duration
	// ParkedRetention bounds how long a parked envelope is kept before the
	// pruner deletes it. Pruning a parked envelope is deliberate data loss
	// (the event was never delivered), so it is generous by default and must
	// stay well past the alerting-and-redrive window.
	ParkedRetention time.Duration
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
	// BootstrapAdminToken is the credential the first admin account takes,
	// supplied by the deployment instead of minted by the service.
	//
	// The minted token is printed once and the shipped image is distroless, so
	// an orchestrated stack cannot capture it: every service starts at the
	// same moment, and the one that needs the credential needs it before the
	// log line exists. With this set, a manifest can put the same value in its
	// secret manager and in both places that need it.
	//
	// It is never logged. `flexitype bootstrap-token` prints a valid one.
	BootstrapAdminToken string
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
	// AuthRateLimitRPS throttles requests by CLIENT ADDRESS before
	// authentication; 0 disables it.
	//
	// The other two limiters key on a resolved principal and run after the
	// authentication middleware, which short-circuits on a bad credential —
	// so a failed authentication was never throttled. Each costs a database
	// round trip and a hash, and only successful authentications are cached,
	// so an unauthenticated caller could exhaust the connection pool and
	// brute-force tokens with nothing bounding either.
	AuthRateLimitRPS float64
	// AuthRateLimitBurst is the pre-authentication token-bucket ceiling.
	AuthRateLimitBurst int
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

	// URL is a complete libpq connection string or URL. When set it replaces
	// the fields above entirely, which is the escape hatch for any parameter
	// they do not model.
	URL string
	// Params are extra libpq keyword=value pairs appended to the rendered
	// form, for the common case of adding one parameter without restating
	// the whole connection string.
	Params string
}

// DSN renders the lib/pq connection string.
//
// FLEXITYPE_DB_URL wins outright when set, so any libpq parameter is
// reachable — `sslrootcert` for a private CA, `application_name` so the
// connection is identifiable in pg_stat_activity, `connect_timeout` so a
// pod does not hang on an unreachable host, `target_session_attrs` for a
// read-write endpoint. The six-field form rendered a fixed parameter list
// and no setting could add to it, so those were unreachable through
// documented configuration.
//
// FLEXITYPE_DB_PARAMS appends to the six-field form for the common case of
// wanting one extra parameter without restating the whole URL.
func (d Database) DSN() string {
	if d.URL != "" {
		return d.URL
	}
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode)
	if d.Params != "" {
		dsn += " " + d.Params
	}
	return dsn
}

// Load reads configuration from the environment with production-safe
// defaults.
func Load() (Config, error) {
	e := &envReader{}
	cfg := Config{
		Port:                    e.int("FLEXITYPE_PORT", 8080),
		ServiceAccountsPath:     os.Getenv("FLEXITYPE_SERVICE_ACCOUNTS"),
		DevInsecure:             e.bool("FLEXITYPE_DEV_INSECURE", false),
		DBAllowPlaintext:        e.bool("FLEXITYPE_DB_ALLOW_PLAINTEXT", false),
		RequireAuth:             e.bool("FLEXITYPE_REQUIRE_AUTH", true),
		EnableConsole:           e.bool("FLEXITYPE_ENABLE_CONSOLE", true),
		MaxImportBytes:          int64(e.int("FLEXITYPE_MAX_IMPORT_BYTES", 0)),
		MaxMediaBytes:           int64(e.int("FLEXITYPE_MAX_MEDIA_BYTES", 0)),
		TimeZone:                envStr("FLEXITYPE_TIMEZONE", ""),
		RelayInterval:           e.duration("FLEXITYPE_RELAY_INTERVAL", 0),
		RelayBatchSize:          e.int("FLEXITYPE_RELAY_BATCH_SIZE", 0),
		RelayLeaseTTL:           e.duration("FLEXITYPE_RELAY_LEASE_TTL", 0),
		OutboxMaxAttempts:       e.int("FLEXITYPE_OUTBOX_MAX_ATTEMPTS", 0),
		OutboxRetryCeiling:      e.duration("FLEXITYPE_OUTBOX_RETRY_CEILING", 0),
		WorkerInterval:          e.duration("FLEXITYPE_WORKER_INTERVAL", 0),
		WorkerConcurrency:       e.int("FLEXITYPE_WORKER_CONCURRENCY", 0),
		WorkerMaxAttempts:       e.int("FLEXITYPE_WORKER_MAX_ATTEMPTS", 0),
		WorkerHTTPTimeout:       e.duration("FLEXITYPE_WORKER_HTTP_TIMEOUT", 0),
		RunRelay:                e.bool("FLEXITYPE_RUN_RELAY", true),
		RunDeliveryWorker:       e.bool("FLEXITYPE_RUN_DELIVERY_WORKER", true),
		RunPruner:               e.bool("FLEXITYPE_RUN_PRUNER", true),
		RunScheduler:            e.bool("FLEXITYPE_RUN_SCHEDULER", true),
		LogLevel:                envStr("FLEXITYPE_LOG_LEVEL", "info"),
		LogFormat:               envStr("FLEXITYPE_LOG_FORMAT", "json"),
		ShutdownTimeout:         e.duration("FLEXITYPE_SHUTDOWN_TIMEOUT", 30*time.Second),
		MigrateOnStart:          e.bool("FLEXITYPE_MIGRATE_ON_START", true),
		EnableSearch:            e.bool("FLEXITYPE_FEATURE_SEARCH", true),
		EnableActivity:          e.bool("FLEXITYPE_FEATURE_ACTIVITY", true),
		EnableOutbox:            e.bool("FLEXITYPE_OUTBOX", false),
		EnableSearchIndex:       e.bool("FLEXITYPE_FEATURE_SEARCH_INDEX", false),
		EnableGraphQLFederation: e.bool("FLEXITYPE_FEATURE_GRAPHQL_FEDERATION", false),
		BlobDir:                 os.Getenv("FLEXITYPE_BLOB_DIR"),
		EventRetention:          e.duration("FLEXITYPE_EVENT_RETENTION", 7*24*time.Hour),
		DeadLetterRetention:     e.duration("FLEXITYPE_DEAD_LETTER_RETENTION", 30*24*time.Hour),
		ParkedRetention:         e.duration("FLEXITYPE_PARKED_RETENTION", 30*24*time.Hour),
		WebhookAllowPrivate:     e.bool("FLEXITYPE_WEBHOOK_ALLOW_PRIVATE", false),
		EnableMetrics:           e.bool("FLEXITYPE_METRICS", true),
		EnableProvisioning:      e.bool("FLEXITYPE_PROVISIONING", false),
		AuthCacheTTL:            e.duration("FLEXITYPE_AUTH_CACHE_TTL", 30*time.Second),
		BootstrapAdmin:          e.bool("FLEXITYPE_BOOTSTRAP_ADMIN", false),
		BootstrapAdminToken:     os.Getenv("FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN"),
		RateLimitRPS:            e.float("FLEXITYPE_RATE_LIMIT_RPS", 50),
		RateLimitBurst:          e.int("FLEXITYPE_RATE_LIMIT_BURST", 200),
		TenantRateLimitRPS:      e.float("FLEXITYPE_TENANT_RATE_LIMIT_RPS", 500),
		TenantRateLimitBurst:    e.int("FLEXITYPE_TENANT_RATE_LIMIT_BURST", 2000),
		AuthRateLimitRPS:        e.float("FLEXITYPE_AUTH_RATE_LIMIT_RPS", 20),
		AuthRateLimitBurst:      e.int("FLEXITYPE_AUTH_RATE_LIMIT_BURST", 40),
		PubSubProject:           os.Getenv("FLEXITYPE_PUBSUB_PROJECT"),
		PubSubTopic:             envStr("FLEXITYPE_PUBSUB_TOPIC", "flexitype-events"),
		PubSubOrdering:          e.bool("FLEXITYPE_PUBSUB_ORDERING", false),
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
			URL:             os.Getenv("FLEXITYPE_DB_URL"),
			Params:          os.Getenv("FLEXITYPE_DB_PARAMS"),
		},
	}
	// Fail on an unknown zone at boot rather than silently falling back to
	// UTC: a deployment that asked for Australia/Adelaide and got UTC has a
	// day boundary that is wrong by hours, and nothing would say so.
	if cfg.TimeZone != "" {
		if _, err := time.LoadLocation(cfg.TimeZone); err != nil {
			return Config{}, fmt.Errorf("FLEXITYPE_TIMEZONE %q is not a known IANA zone: %w", cfg.TimeZone, err)
		}
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
	// FLEXITYPE_DEV_INSECURE is the ONLY way to boot unauthenticated.
	//
	// FLEXITYPE_REQUIRE_AUTH=false used to be a second, undocumented one: it
	// skipped this check entirely, so a manifest carried over from before the
	// fail-closed default kept booting open while the warning named a
	// variable nobody had set. RequireAuth no longer disables anything; a
	// deployment that set it and has no account source is refused, and told
	// which variable actually expresses that intent.
	cfg.RequireAuth = !cfg.DevInsecure
	if cfg.RequireAuth && cfg.ServiceAccountsPath == "" && !cfg.EnableProvisioning {
		hint := ""
		if raw := os.Getenv("FLEXITYPE_REQUIRE_AUTH"); raw != "" && !parseBoolOrTrue(raw) {
			hint = " (FLEXITYPE_REQUIRE_AUTH=false does not disable authentication)"
		}
		return Config{}, fmt.Errorf(
			"no account source is configured: set FLEXITYPE_SERVICE_ACCOUNTS or FLEXITYPE_PROVISIONING=true. "+
				"To run without authentication — which serves the whole API, including the irreversible "+
				"admin purge, to anonymous callers — set FLEXITYPE_DEV_INSECURE=true explicitly%s", hint)
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
	//
	// A supplied FLEXITYPE_DB_URL goes through the SAME guard. Letting a URL
	// past it would have made the escape hatch a way to turn the protection
	// off by accident: the whole point of the guard is that unencrypted
	// credentials never leave the host, and which setting expressed the
	// connection does not change that.
	// The guard reads the RENDERED DSN, so it sees what libpq will see.
	//
	// It used to read the individual settings and re-parse only when
	// FLEXITYPE_DB_URL was set, which left FLEXITYPE_DB_PARAMS — appended
	// verbatim to the connection string — outside it entirely. libpq resolves
	// duplicate keywords LAST-WINS, so `FLEXITYPE_DB_PARAMS="sslmode=disable"`
	// silently turned TLS off while the guard read the `sslmode=require` it
	// was told about, and `host=` inside the params redirected the connection,
	// with the configured credentials, to another server.
	//
	// FLEXITYPE_DB_ALLOW_PLAINTEXT is the opt-out that means ONLY this.
	// FLEXITYPE_DEV_INSECURE implies it, so a manifest written before the two
	// were separated keeps working — but a deployment that just needs
	// plaintext Postgres no longer has to set the variable that turns
	// authentication off and logs that it did.
	cfg.DBAllowPlaintext = cfg.DBAllowPlaintext || cfg.DevInsecure
	sslMode, host := sslModeAndHostOf(cfg.Database.DSN())
	if sslMode == "disable" && !isLoopbackHost(host) && !cfg.DBAllowPlaintext {
		return Config{}, fmt.Errorf(
			"sslmode=disable is not allowed for non-loopback host %q; use require/verify-full, "+
				"or set FLEXITYPE_DB_ALLOW_PLAINTEXT=true for a local development stack", host)
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

// ParseBoolOrTrueForTest exposes parseBoolOrTrue to the package's external
// test, which cannot reach an unexported function.
func ParseBoolOrTrueForTest(raw string) bool { return parseBoolOrTrue(raw) }

// parseBoolOrTrue reports a boolean environment value, treating anything
// unparseable as true so an unreadable setting is never read as an opt-out.
func parseBoolOrTrue(raw string) bool {
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return true
	}
	return b
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

// sslModeAndHostOf extracts the sslmode and host from a connection string in
// either libpq form: a URL (postgres://…?sslmode=…) or keyword=value pairs.
//
// An unparseable or absent sslmode reads as "disable", which is the safe
// direction: the guard then demands a loopback host rather than assuming the
// connection is encrypted.
func sslModeAndHostOf(dsn string) (sslMode, host string) {
	sslMode = "disable"
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		host = u.Hostname()
		if v := u.Query().Get("sslmode"); v != "" {
			sslMode = v
		}
		if v := u.Query().Get("hostaddr"); v != "" {
			host = v
		}
		return sslMode, host
	}
	// Keyword form. The LAST occurrence of a keyword wins, as in libpq, so a
	// value appended by FLEXITYPE_DB_PARAMS is what the guard evaluates —
	// which is the point: it is also what the connection uses.
	opts := parseKeywordDSN(dsn)
	if v, ok := opts["sslmode"]; ok {
		sslMode = v
	}
	host = opts["host"]
	// hostaddr bypasses name resolution entirely, so it is where the
	// connection actually goes when both are present.
	if v := opts["hostaddr"]; v != "" {
		host = v
	}
	return sslMode, host
}

// parseKeywordDSN parses libpq keyword/value connection options, with the
// LAST occurrence of a keyword winning, as in libpq — so a value appended by
// FLEXITYPE_DB_PARAMS is what the guard evaluates, which is also what the
// connection uses.
//
// It follows libpq's grammar rather than splitting on whitespace: a value may
// be SINGLE-QUOTED, may contain backslash escapes, and may have spaces around
// the '='. Splitting on whitespace and cutting at '=' left the quotes in the
// value, so `sslmode='disable'` was compared as `'disable'`, matched nothing,
// and passed a guard that exists to refuse exactly that — while lib/pq
// honoured the quoted form and connected in cleartext.
func parseKeywordDSN(dsn string) map[string]string {
	opts := map[string]string{}
	r := []rune(dsn)
	i, n := 0, len(r)
	skipSpace := func() {
		for i < n && unicode.IsSpace(r[i]) {
			i++
		}
	}
	for {
		skipSpace()
		if i >= n {
			return opts
		}
		start := i
		for i < n && !unicode.IsSpace(r[i]) && r[i] != '=' {
			i++
		}
		key := string(r[start:i])
		skipSpace()
		if i >= n || r[i] != '=' {
			// A bare token with no '=' is malformed. libpq rejects the whole
			// string; the guard cannot, so it skips the token and keeps
			// reading — the guard must not become more permissive because a
			// string it does not understand appears earlier in the DSN.
			continue
		}
		i++ // consume '='
		skipSpace()
		var b strings.Builder
		if i < n && r[i] == '\'' {
			i++
			for i < n && r[i] != '\'' {
				if r[i] == '\\' && i+1 < n {
					i++
				}
				b.WriteRune(r[i])
				i++
			}
			if i < n {
				i++ // consume the closing quote
			}
		} else {
			for i < n && !unicode.IsSpace(r[i]) {
				if r[i] == '\\' && i+1 < n {
					i++
				}
				b.WriteRune(r[i])
				i++
			}
		}
		if key != "" {
			opts[key] = b.String()
		}
	}
}
