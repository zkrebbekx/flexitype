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
| `FLEXITYPE_DB_SSLMODE` | `disable` | `libpq` SSL mode (`disable`, `require`, `verify-full`, …). |
| `FLEXITYPE_DB_MAX_OPEN_CONNS` | `25` | Pool max open connections. |
| `FLEXITYPE_DB_MAX_IDLE_CONNS` | `10` | Pool max idle connections. |
| `FLEXITYPE_DB_CONN_MAX_LIFETIME` | `30m` | Max connection lifetime. |

## Authentication

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_SERVICE_ACCOUNTS` | _(unset)_ | Path to the service-account JSON file. Unset ⇒ development mode with auth disabled. |

## Features

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_FEATURE_SEARCH` | `true` | Enable the FQL query surface. |
| `FLEXITYPE_FEATURE_ACTIVITY` | `true` | Enable the audit log (writes and read API). |
| `FLEXITYPE_FEATURE_SEARCH_INDEX` | `false` | Maintain the entity search projection and unlock FQL `matches()`. |

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

## Observability

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_METRICS` | `true` | Serve Prometheus SLIs at `/metrics`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | _(unset)_ | Standard OpenTelemetry OTLP endpoint; unset ⇒ tracing is a no-op. |

Booleans accept `true`/`false` (case-insensitive); durations accept Go
duration strings (`30s`, `7h`, `168h`). Embedded deployments configure the
equivalent behaviour through `flexitype.New` options rather than these
variables — see the README.
