# Configuration reference

The standalone service (`cmd/flexitype`) is configured entirely through
environment variables. Every variable, its default, and its meaning:

## Server

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_PORT` | `8080` | HTTP listen port. |
| `FLEXITYPE_SHUTDOWN_TIMEOUT` | `30s` | Grace period for draining connections and the delivery machinery on shutdown. |
| `FLEXITYPE_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error`. |
| `FLEXITYPE_LOG_FORMAT` | `json` | `json` for machine logs, `console` for human-readable. |
| `FLEXITYPE_MIGRATE_ON_START` | `true` | Apply embedded schema migrations at boot. |

## Database

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_DB_HOST` | `localhost` | PostgreSQL host. |
| `FLEXITYPE_DB_PORT` | `5432` | PostgreSQL port. |
| `FLEXITYPE_DB_USER` | `postgres` | Database user. |
| `FLEXITYPE_DB_PASSWORD` | `postgres` | Database password. |
| `FLEXITYPE_DB_NAME` | `flexitype` | Database name. |
| `FLEXITYPE_DB_SSLMODE` | `disable` | `libpq` SSL mode (`disable`, `require`, `verify-full`, …). `disable` is refused for a non-loopback host — use `require`/`verify-full` in production. |
| `FLEXITYPE_DB_MAX_OPEN_CONNS` | `25` | Pool max open connections. |
| `FLEXITYPE_DB_MAX_IDLE_CONNS` | `10` | Pool max idle connections. |
| `FLEXITYPE_DB_CONN_MAX_LIFETIME` | `30m` | Max connection lifetime. |

## Authentication

Three modes, in precedence order:

1. **Provisioning** (`FLEXITYPE_PROVISIONING=true`) — accounts live in the
   database and are managed at runtime via the admin API. Bearer tokens are
   authenticated against the `flexitype_service_account` table.
2. **File** (`FLEXITYPE_SERVICE_ACCOUNTS=<path>`) — accounts are read from a
   JSON file at startup (static; edit-and-redeploy to change).
3. **Development** (neither set) — authentication is disabled and every
   request runs as the system actor with admin scope. This is opt-out:
   set `FLEXITYPE_REQUIRE_AUTH=true` in production so the service refuses to
   boot unless an account source is configured, rather than silently serving
   the whole multi-tenant API unauthenticated.

Provisioning wins if both it and a file are set.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_SERVICE_ACCOUNTS` | _(unset)_ | Path to the service-account JSON file (file mode). |
| `FLEXITYPE_PROVISIONING` | `false` | Enable database-backed auth and the admin-scoped tenant/service-account API. |
| `FLEXITYPE_REQUIRE_AUTH` | `false` | Refuse to boot unless an account source (file or provisioning) is configured. Set to `true` in production to prevent accidentally running with authentication disabled. |
| `FLEXITYPE_BOOTSTRAP_ADMIN` | `false` | On startup, if no accounts exist, seed a `default` tenant and `bootstrap-admin` admin account. Its token is logged **once** — capture it. |
| `FLEXITYPE_AUTH_CACHE_TTL` | `30s` | How long a database-backed auth result is cached. Bounds how quickly a revoked or rotated credential stops working. |

### Provisioning API

All endpoints require the `admin` scope and return `501` when provisioning
is off. See `api/openapi.yaml` for the full contract.

| Method & path | Purpose |
| --- | --- |
| `POST /api/v1/tenants` | Create a tenant. |
| `GET /api/v1/tenants` | List tenants. |
| `PATCH /api/v1/tenants/{name}` | Activate/deactivate a tenant. |
| `POST /api/v1/service-accounts` | Create an account; token returned **once**. |
| `GET /api/v1/service-accounts?tenant_name=` | List a tenant's accounts (no secrets). |
| `POST /api/v1/service-accounts/{id}/rotate` | Rotate the secret; new token returned once. |
| `DELETE /api/v1/service-accounts/{id}` | Revoke the account. |

## Features

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_FEATURE_SEARCH` | `true` | Enable the FQL query surface. |
| `FLEXITYPE_FEATURE_ACTIVITY` | `true` | Enable the audit log (writes and read API). |
| `FLEXITYPE_FEATURE_SEARCH_INDEX` | `false` | Maintain the entity search projection and unlock FQL `matches()`. |
| `FLEXITYPE_BLOB_DIR` | _(unset)_ | Directory backing media-attribute uploads (local-disk blob store). Unset disables media uploads. |

## Event delivery (with the outbox)

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_OUTBOX` | `false` | Enable the transactional outbox, webhook subscriptions and the events feed. |
| `FLEXITYPE_EVENT_RETENTION` | `168h` (7d) | How long expanded events stay readable in the feed before pruning. |
| `FLEXITYPE_WEBHOOK_URL` | _(unset)_ | Bootstrap webhook endpoint. With the outbox on, it is upserted as a managed subscription; otherwise it registers a direct hook. |
| `FLEXITYPE_WEBHOOK_SECRET` | _(unset)_ | HMAC secret for the bootstrap webhook. |
| `FLEXITYPE_WEBHOOK_ALLOW_PRIVATE` | `false` | Allow subscriptions to target private/loopback/link-local hosts over http (on-prem; relaxes the SSRF guard). |

## Google Cloud Pub/Sub

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_PUBSUB_PROJECT` | _(unset)_ | GCP project id; set to publish every event to Pub/Sub. |
| `FLEXITYPE_PUBSUB_TOPIC` | `flexitype-events` | Pub/Sub topic id. |
| `FLEXITYPE_PUBSUB_ORDERING` | `false` | Stamp per-aggregate ordering keys (the topic's subscriptions must enable message ordering). |
| `PUBSUB_EMULATOR_HOST` | _(unset)_ | Standard Pub/Sub emulator override for local development. |

## Rate limiting

Per-service-account throttling with a token bucket. Off by default; set a
rate to enable. A throttled request gets `429` with a `Retry-After` header.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_RATE_LIMIT_RPS` | `0` | Sustained requests per second per account (0 disables rate limiting). |
| `FLEXITYPE_RATE_LIMIT_BURST` | `200` | Token-bucket ceiling — how many requests a single account may burst before throttling. |

When metrics are on, `flexitype_tenant_requests_total{tenant}` counts
authenticated requests and `flexitype_ratelimit_rejected_total{tenant}`
counts throttled ones.

## Observability

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_METRICS` | `true` | Serve Prometheus SLIs at `/metrics`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(unset)_ | Standard OpenTelemetry OTLP endpoint; unset ⇒ tracing is a no-op. |

Booleans accept `true`/`false` (case-insensitive); durations accept Go
duration strings (`30s`, `7h`, `168h`). Embedded deployments configure the
equivalent behaviour through `flexitype.New` options rather than these
variables — see the README.
