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

**Authentication is required by default.** The service refuses to boot unless
an account source is configured, or the insecure mode is asked for by name.

Three modes, in precedence order:

1. **Provisioning** (`FLEXITYPE_PROVISIONING=true`) — accounts live in the
   database and are managed at runtime via the admin API. Bearer tokens are
   authenticated against the `flexitype_service_account` table.
2. **File** (`FLEXITYPE_SERVICE_ACCOUNTS=<path>`) — accounts are read from a
   JSON file at startup (static; edit-and-redeploy to change).
3. **Development** (`FLEXITYPE_DEV_INSECURE=true`) — authentication is
   disabled and every request runs as the system actor with admin scope.

Provisioning wins if both it and a file are set.

`FLEXITYPE_DEV_INSECURE` exists so the insecure configuration has to be asked
for by name. Without it, a deployment that set only database variables served
the entire multi-tenant API to anonymous callers with admin access — including
`POST /api/v1/admin/purge`, the irreversible hard delete — while every symptom
of correct operation was present: health checks passed, the console worked, and
the only signal was one warning line at startup. A configuration error must
cause the service to refuse traffic, not to serve it with maximum privilege.

The flag also permits `FLEXITYPE_DB_SSLMODE=disable` against a non-loopback
host, which is what the compose quickstart needs to reach a container-network
hostname. Never set it in a deployment reachable by anything but you.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_SERVICE_ACCOUNTS` | _(unset)_ | Path to the service-account JSON file (file mode). |
| `FLEXITYPE_PROVISIONING` | `false` | Enable database-backed auth and the admin-scoped tenant/service-account API. |
| `FLEXITYPE_DEV_INSECURE` | `false` | Run with authentication disabled, and permit an unencrypted database connection to a non-loopback host. Local development only. |
| `FLEXITYPE_REQUIRE_AUTH` | `true` | Refuse to boot unless an account source (file or provisioning) is configured. `FLEXITYPE_DEV_INSECURE` overrides it. |
| `FLEXITYPE_BOOTSTRAP_ADMIN` | `false` | On startup, if no accounts exist, seed a `default` tenant and `bootstrap-admin` admin account. Its token is printed to **stdout once** (never to the structured log) — capture it. |
| `FLEXITYPE_AUTH_CACHE_TTL` | `30s` | How long a database-backed auth result is cached. Rotation and revocation evict the account's entries immediately, so this bounds only a change made directly in the database. |

Deactivating a tenant (`PATCH /api/v1/tenants/{name}` with `{"active": false}`)
suspends **every service account under it**, in one action. Authentication joins
the tenant's own flag, so this is a real suspension rather than control-plane
metadata.

### Provisioning API

All endpoints require the `admin` scope and return `501` when provisioning
is off. See `api/openapi.yaml` for the full contract.

> **The `admin` scope is a platform-operator (global) privilege, not a
> tenant-admin one.** The provisioning control plane operates *across*
> tenants: an `admin`-scoped account takes the target tenant from the request
> body, so it can create write tokens in any tenant, list every tenant, and
> rotate or revoke any account — effectively a global superuser. Issue `admin`
> tokens only to trusted platform operators, never to per-tenant
> administrators; use `read`/`write`-scoped accounts for tenant users. (A
> future major version may split platform-operator from tenant-admin; until
> then the boundary is operational, not enforced by the scope.)

| Method & path | Purpose |
| --- | --- |
| `POST /api/v1/tenants` | Create a tenant. |
| `GET /api/v1/tenants` | List tenants. |
| `PATCH /api/v1/tenants/{name}` | Activate/deactivate a tenant. |
| `POST /api/v1/service-accounts` | Create an account; token returned **once**. |
| `GET /api/v1/service-accounts?tenant_name=` | List a tenant's accounts (no secrets). |
| `POST /api/v1/service-accounts/{id}/rotate` | Rotate the secret; new token returned once. |
| `DELETE /api/v1/service-accounts/{id}` | Revoke the account. |

## Deployment topology

Every background loop defaults to running in every replica, which is correct
for a single-process deployment and wrong for a fleet: ten API replicas would
each poll the outbox every two seconds, and autoscaling the API on CPU would
autoscale the delivery machinery with it.

These switches let one image serve as both an API tier and a worker tier.
**No leader election is needed** — every loop claims work with a lease and
`FOR UPDATE SKIP LOCKED`, so running one on any number of replicas is safe.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_RUN_RELAY` | `true` | Expand the outbox and dispatch to in-process hooks. |
| `FLEXITYPE_RUN_DELIVERY_WORKER` | `true` | Deliver webhook subscriptions. |
| `FLEXITYPE_RUN_PRUNER` | `true` | Enforce event retention. |
| `FLEXITYPE_RUN_SCHEDULER` | `true` | Publish approved change-sets whose `publish_at` has arrived. |
| `FLEXITYPE_ENABLE_CONSOLE` | `true` | Serve the embedded admin console. With `false`, the SPA is not mounted and an unmatched path returns a JSON 404. |
| `FLEXITYPE_MAX_IMPORT_BYTES` | `16777216` (16 MiB) | Maximum size of a CSV import upload. A larger body is refused before it is read. |
| `FLEXITYPE_MAX_MEDIA_BYTES` | `33554432` (32 MiB) | Maximum size of a media upload. |
| `FLEXITYPE_TIMEZONE` | UTC | IANA zone whose **calendar day** `today` and `now` resolve against in dependency conditions and dynamic defaults. An unknown zone fails at boot. |

`FLEXITYPE_TIMEZONE` changes which day those name, not how anything is
stored: a date value is a calendar date held as midnight UTC either way.
Without it, a deployment outside UTC had a date-boundary rule that was wrong
for part of every day — a condition on "expires before today" flipped at the
wrong hour, and a `today` default recorded yesterday for anything created
after the UTC midnight.

An embedder serving tenants in several zones from one process stamps
`uow.WithTimeZone` per request instead; a per-request zone wins over this
setting.
## Connection string

| Variable | Default | Purpose |
|---|---|---|
| `FLEXITYPE_DB_URL` | — | A complete libpq connection string or URL. **Replaces the six fields below entirely.** |
| `FLEXITYPE_DB_PARAMS` | — | Extra `keyword=value` pairs appended to the rendered form. |

The six-field form (`FLEXITYPE_DB_HOST`, `_PORT`, `_USER`, `_PASSWORD`,
`_NAME`, `_SSLMODE`) covers the common case. It rendered a fixed parameter
list and nothing could add to it, so `sslrootcert` (a private CA),
`application_name` (identifying the connection in `pg_stat_activity`),
`connect_timeout` (so a pod does not hang on an unreachable host) and
`target_session_attrs` were unreachable through documented configuration.

```bash
# One extra parameter, keeping the six-field form:
FLEXITYPE_DB_PARAMS="application_name=flexitype connect_timeout=5"

# Or the whole connection string:
FLEXITYPE_DB_URL="postgres://u:p@db:5432/flexitype?sslmode=verify-full&sslrootcert=/etc/ssl/ca.pem"
```

**A supplied URL goes through the same TLS guard.** `sslmode=disable` against
a non-loopback host is refused whichever setting expressed it — the escape
hatch is not a way to turn that protection off. An unparseable connection
string reads as `disable`, which is the safe direction.

libpq's `PG*` environment variables also work as a fallback, but prefer these:
a `PG*` variable set for one purpose silently affects every libpq client in
the pod.

## Connection poolers

flexitype supports **transaction-mode** pooling (PgBouncer, pgcat, RDS Proxy)
and, obviously, session mode.

That is a contract, not an accident, and CI holds it: the Postgres suites run
once through a direct connection and once through PgBouncer in transaction
mode. What the contract requires of the code:

- every advisory lock is **transaction-scoped** (`pg_advisory_xact_lock`), so
  no lock outlives the transaction that took it. A session-scoped lock would
  be released onto a connection another client then borrows.
- no `LISTEN`/`NOTIFY`, no session-level `SET`, no temporary tables, no
  prepared statements held across transactions.

**Set your driver up for it.** `lib/pq` uses the extended query protocol,
which needs `binary_parameters=yes` in the DSN — or PgBouncer 1.21+ with
prepared-statement support — otherwise a pooled connection reports
"prepared statement does not exist" under load.

Statement-mode pooling is **not** supported: it forbids multi-statement
transactions, and every write here is one.

## Database driver

The module requires `github.com/lib/pq v1.12.3`, but the code uses a narrow,
long-stable slice of it:

| API | Used by |
|---|---|
| `pq.Array`, `pq.StringArray` | relationship and query stores |
| `pq.Error` (code inspection) | constraint-violation mapping |
| `pq.CopyIn` | the stress harness only |

That matters because a host monorepo commonly `replace`s `lib/pq` with a fork
patched for pooler behaviour, and **a `replace` wins over minimal version
selection** — so the build succeeds against whatever the host pinned, without
warning. Any fork providing the three APIs above works;
`TestDriverAPISurface` compiles against exactly them, so a fork that drops one
fails the build here rather than at runtime in the host.

| `FLEXITYPE_RELAY_INTERVAL` | library default | How often the outbox relay looks for undispatched envelopes. |
| `FLEXITYPE_RELAY_BATCH_SIZE` | library default | Envelopes claimed per relay pass. |
| `FLEXITYPE_RELAY_LEASE_TTL` | library default | How long a relay's claim on a batch survives before another relay may reclaim it. |
| `FLEXITYPE_WORKER_INTERVAL` | library default | How often the delivery worker looks for due deliveries. |
| `FLEXITYPE_WORKER_CONCURRENCY` | library default | Deliveries attempted in parallel. |
| `FLEXITYPE_WORKER_MAX_ATTEMPTS` | `25` | Attempts before a delivery goes to the dead-letter queue. |
| `FLEXITYPE_WORKER_HTTP_TIMEOUT` | `10s` | Timeout for one webhook delivery attempt. |

**Set the lease longer than a batch takes to drain.** A relay claims a batch,
then the worker delivers it; if the lease expires first, another relay
reclaims envelopes that are still being worked. The worst case is roughly
`batch_size / worker_concurrency x worker_http_timeout`, so the defaults
(batch 32, concurrency 4, timeout 10s) can take about 80 seconds against a
60-second lease. Raise `FLEXITYPE_RELAY_LEASE_TTL`, lower the batch, or raise
concurrency — the point is that this is now adjustable without rebuilding.

The delivery-attempt timeout is a duration rather than a client on purpose:
the delivery HTTP client is the SSRF guard that refuses private destinations,
and replacing it would remove that guard.

`GET /api/v1/features` reports both ceilings as `max_import_bytes` and
`max_media_bytes`, so a client chunks a bulk load against the deployment's
real limit rather than assuming the default. Size an onboarding job against
that response, not against these numbers.

Both peers hold a whole upload in memory today — the server buffers the
multipart form, and the Go client builds the request body before sending — so
raising these ceilings raises peak memory on both sides. Prefer more, smaller
chunks over one large one.

A typical split:

```yaml
# API tier — scales on request rate
FLEXITYPE_RUN_RELAY: "false"
FLEXITYPE_RUN_DELIVERY_WORKER: "false"
FLEXITYPE_RUN_PRUNER: "false"
FLEXITYPE_RUN_SCHEDULER: "false"

# Worker tier — scales on queue depth, console off
FLEXITYPE_ENABLE_CONSOLE: "false"
```

An unknown path under `/api/` is always a JSON 404, whether or not the console
is mounted. Only non-API paths reach the app shell.

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

Two ceilings, both on by default. A deployment gets the throttled configuration
without needing to know these variables exist; set either rate to `0` to turn
that ceiling off.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_RATE_LIMIT_RPS` | `50` | Sustained requests per second per **service account**. |
| `FLEXITYPE_RATE_LIMIT_BURST` | `200` | Token-bucket ceiling for short bursts, per account. |
| `FLEXITYPE_TENANT_RATE_LIMIT_RPS` | `500` | Sustained requests per second per **tenant**, across all of its accounts. |
| `FLEXITYPE_TENANT_RATE_LIMIT_BURST` | `2000` | Token-bucket ceiling for short bursts, per tenant. |

The per-account limiter alone cannot bound a tenant: a tenant that creates more
service accounts multiplies its effective rate by the account count, because the
per-account buckets have no view of the total. The tenant ceiling is checked
first.

A rejected request gets `429` with `Retry-After` and is counted in
`flexitype_ratelimit_rejected_total`.

`POST /api/v1/search/reindex` and `POST /api/v1/computed/recompute` require the
**admin** scope. Both are tenant-wide, cheap to call and expensive to serve, so
a write-scoped token could repeatedly trigger them — a within-tenant denial of
service that a per-request limiter does not contain.

## Observability

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_METRICS` | `true` | Serve Prometheus SLIs at `/metrics`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(unset)_ | Standard OpenTelemetry OTLP endpoint; unset ⇒ tracing is a no-op. |

Booleans accept `true`/`false` (case-insensitive); durations accept Go
duration strings (`30s`, `7h`, `168h`). Embedded deployments configure the
equivalent behaviour through `flexitype.New` options rather than these
variables — see the README.
