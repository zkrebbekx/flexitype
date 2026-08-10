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

Never set it in a deployment reachable by anything but you.

`FLEXITYPE_DB_ALLOW_PLAINTEXT=true` is a SEPARATE opt-out, and it permits one
thing: `FLEXITYPE_DB_SSLMODE=disable` against a non-loopback host, which is
what a container-network hostname is. Use it when a stack needs plaintext
Postgres and still authenticates every request — the compose quickstart is
exactly that. `FLEXITYPE_DEV_INSECURE` implies it, so an older manifest keeps
working.

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_SERVICE_ACCOUNTS` | _(unset)_ | Path to the service-account JSON file (file mode). |
| `FLEXITYPE_PROVISIONING` | `false` | Enable database-backed auth and the admin-scoped tenant/service-account API. |
| `FLEXITYPE_DEV_INSECURE` | `false` | Run with authentication disabled. It also implies `FLEXITYPE_DB_ALLOW_PLAINTEXT`. Local development only. |
| `FLEXITYPE_DB_ALLOW_PLAINTEXT` | `false` | Permit an unencrypted database connection to a non-loopback host, and nothing else. Authentication is unaffected. |
| `FLEXITYPE_REQUIRE_AUTH` | `true` | Reports that an account source is required. **Setting it to `false` does not disable authentication** — `FLEXITYPE_DEV_INSECURE` is the only opt-out, so a manifest carried over from an older release cannot boot open by omission. |
| `FLEXITYPE_BOOTSTRAP_ADMIN` | `false` | On startup, if no accounts exist, seed a `default` tenant and `bootstrap-admin` admin account. Its token is printed to **stdout once** (never to the structured log) — capture it. |
| `FLEXITYPE_MEDIA_URL_SECRET` | _(unset)_ | Turns on signed, expiring media links. At least 32 characters, and NOT the webhook signing key. Without it, `GET /media/{key}/signed-url` reports the capability as disabled. |
| `FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN` | _(unset)_ | The credential that account takes, decided by the deployment instead of minted by the service. Nothing is printed when it is set. Generate one with `flexitype bootstrap-token`. |
| `FLEXITYPE_AUTH_CACHE_TTL` | `30s` | How long a successful authentication is cached. Rotation, revocation, a role write or delete, and a tenant deactivation evict the affected entries in the process that served the call — the cache is per-process, with no cross-replica signal. A multi-replica deployment therefore converges over this TTL, so it bounds both a change made directly in the database and the tail of a change made through the API. |

Deactivating a tenant (`PATCH /api/v1/tenants/{name}` with `{"active": false}`)
suspends **every service account under it**, in one action. Authentication joins
the tenant's own flag, so this is a real suspension rather than control-plane
metadata.

### There is no cross-tenant credential

A tenant comes from the token. There is no header, no parameter and no scope
that changes that, so no request reads two tenants.

flexitype briefly had a `read_any_tenant` scope for a cross-tenant read model.
It was withdrawn in v1.7.0 and v1.6.0 is retracted. The reasoning is worth
keeping: the scope existed to serve ONE architecture — a service aggregating
every tenant into one read model — and it made the property every other
guarantee rests on conditional. Every later feature would have had to answer
"and what does a cross-tenant reader see?": the field ACL, erasure receipts,
audit attribution, per-tenant rate limits, signed media links.

A read model that needs several tenants has answers that do not cross the
boundary:

- **One consumer per tenant.** A projection service deployed per tenant holds
  one credential and serves one catalogue. `examples/marketplace` is built this
  way: one storefront per merchant, each refusing anything that is not its own.
- **Per-tenant credentials in a secret manager**, if one process genuinely must
  serve several — the concentration is then explicit and auditable rather than
  a scope that reads everything.

### A bootstrap credential a manifest can know

A minted bootstrap token is printed once, and the shipped image is distroless.
An orchestrated stack cannot capture it: every service starts at the same
moment, and the service that needs the admin credential needs it before the log
line exists. Scraping the container log is not an interface.

Set the token instead:

```bash
flexitype bootstrap-token          # ft_01J…_kZ8…  — store this in your secret manager
```

```yaml
environment:
  FLEXITYPE_PROVISIONING: "true"
  FLEXITYPE_BOOTSTRAP_ADMIN: "true"
  FLEXITYPE_BOOTSTRAP_ADMIN_TOKEN: "${FLEXITYPE_ADMIN_TOKEN}"   # from the secret manager
```

Rules the service keeps:

- The token must be one flexitype would have minted: `ft_<ULID>_<secret>`, with
  a secret of at least 32 characters. A hand-written or weak value is refused
  at startup, not at first use.
- It applies on FIRST boot only. A tenant that already has an account is left
  alone, so an environment variable cannot re-key a live deployment.
- It is never logged and never returned. The operator already has it.

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

The standalone API stamps the zone on the request context in its middleware.
An embedder must pass its own context through `Service.Context` first, because
the zone has to travel on the context handed to each interactor METHOD:

```go
ctx = svc.Context(ctx)               // stamps the configured zone
it := svc.Interactors(ctx)
schema, err := it.TypeDefinitions().EffectiveAttributes(ctx, typeID)
```
## Connection string

| Variable | Default | Purpose |
|---|---|---|
| `FLEXITYPE_DB_URL` | — | A complete libpq connection string or URL. **Replaces the six fields below entirely.** |
| `FLEXITYPE_DB_PARAMS` | — | Extra `keyword=value` pairs appended to the rendered form. The TLS guard reads the **rendered DSN**, so a `sslmode`, `host` or `hostaddr` here is evaluated exactly as libpq resolves it (last wins) — the hatch cannot turn the protection off, by accident or otherwise. |

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
  no lock outlives the transaction that took it. The migration runner, which
  spans several transactions and statements that run outside any transaction,
  holds a **lease row** rather than a lock for the same reason. A
  session-scoped lock would
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
| `FLEXITYPE_OUTBOX_MAX_ATTEMPTS` | `25` | Dispatch failures before the relay parks an envelope. A parked envelope waits for an operator redrive. |
| `FLEXITYPE_OUTBOX_RETRY_CEILING` | `15m` | Cap on the exponential backoff between outbox dispatch attempts (1s, 4s, 16s, ... up to this ceiling). |
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
| `FLEXITYPE_FEATURE_GRAPHQL_FEDERATION` | `false` | Serve the GraphQL endpoint as an Apollo-Federation subgraph (`_service`, `_entities`, `@key`). See [federation.md](federation.md). |
| `FLEXITYPE_BLOB_DIR` | _(unset)_ | Directory backing media-attribute uploads (local-disk blob store). Unset disables media uploads. It is checked for writability at start-up: a directory this process cannot write to stops the service instead of failing the first upload. |
| `FLEXITYPE_MEDIA_URL_SECRET` | _(unset)_ | Turns on signed, expiring media links (below). |

### Signed media links

Media bytes are served from `GET /api/v1/media/{key}` behind the same
authentication as everything else, and a token carries one tenant. A public
surface — a storefront, a catalogue page, an email — therefore cannot link to
an image: it has to proxy every request through a service holding a tenant
credential, which carries the whole file through a process with no other reason
to touch it and defeats any CDN in front of it.

Set `FLEXITYPE_MEDIA_URL_SECRET` and an authenticated caller can mint a link
anybody may redeem, for a while:

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
  "$API/media/$OBJECT_KEY/signed-url?ttl_seconds=600"
# {"url":"/media/signed/eyJ2Ijoi…","expires_at":"2026-08-09T12:10:00Z"}

curl -s "$BASE/media/signed/eyJ2Ijoi…" -o photo.png   # no credential at all
```

What the design holds to:

- The signature covers the **tenant, the object key and the expiry together**.
  A signature over the key alone could be replayed against another object; one
  without the expiry could be replayed forever.
- The tenant is read back **out of the verified token**, never from the
  request, so a holder cannot point a valid signature at another tenant's
  object.
- Minting is gated on the **same check the authenticated download makes** — the
  caller's tenant must own a value referencing the key, and the caller must be
  able to read the attribute holding it. A caller who cannot fetch the bytes
  cannot mint a link that lets anyone else fetch them.
- Every redemption failure — malformed, forged, expired, unknown key — is the
  **same 404**. Distinguishing "expired" from "forged" tells a probing holder
  which half to work on.
- Minting is a **GET**, because it changes nothing: it hands back a capability
  to read an object the caller can already read. That also keeps it reachable
  by a read-only credential, including a cross-tenant reader.
- The default lifetime is 15 minutes and the cap is 24 hours. A longer request
  is capped rather than refused.
- The secret must be at least 32 characters, and **must not be the webhook
  signing key**: one leaked secret must not forge both an event and a file
  link.

`GET /media/signed/{token}` is served outside `/api/v1`, and takes no
credential — the signature is the credential.

The redemption response is **publicly cacheable**: `Cache-Control: public,
max-age=<seconds until the token expires>, immutable`. A shared cache is the
point — a CDN can serve the bytes without the origin seeing every hit. The URL
is the capability, so the signature is part of the cache key, and `max-age`
never outlives the token. An object key names immutable bytes, so a cached
response can never be stale. The authenticated `GET /api/v1/media/{key}` stays
`private`, because that route needs a credential and its URL is the same for
every caller.

A CDN sits in front of this route only. If a shared cache must never hold
tenant bytes at all, do not publish the route through one — the header assumes
you meant to.

## Event delivery (with the outbox)

| Variable | Default | Description |
| --- | --- | --- |
| `FLEXITYPE_OUTBOX` | `false` | Enable the transactional outbox, webhook subscriptions and the events feed. |
| `FLEXITYPE_DEAD_LETTER_RETENTION` | `720h` (30d) | How long a **dead** delivery is kept before it stops pinning its envelope. The envelope prune keeps anything a dead delivery references, which is what makes a dead letter redrivable — so without this bound one decommissioned endpoint pinned its envelopes for ever and `FLEXITYPE_EVENT_RETENTION` stopped bounding the outbox or the feed. Far longer than the event retention on purpose. |
| `FLEXITYPE_EVENT_RETENTION` | `168h` (7d) | How long expanded events stay readable in the feed before pruning. |
| `FLEXITYPE_PARKED_RETENTION` | `720h` (30d) | How long a **parked** envelope is kept before the pruner deletes it. A parked envelope is a committed change that exhausted its retry budget and was never delivered. The prune of a parked envelope is deliberate data loss: after it, the event can never be redriven. Keep this bound well past your alerting-and-redrive window. Alert on the `flexitype_outbox_parked` gauge and redrive with `POST /api/v1/admin/outbox/redrive` long before this bound. |
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
| `FLEXITYPE_RATE_LIMIT_RPS` | `50` | Sustained requests per second per **service account**, **per API process** — see the note below. |
| `FLEXITYPE_RATE_LIMIT_BURST` | `200` | Token-bucket ceiling for short bursts, per account. |
| `FLEXITYPE_TENANT_RATE_LIMIT_RPS` | `500` | Sustained requests per second per **tenant**, across all of its accounts, **per API process**. |
| `FLEXITYPE_AUTH_RATE_LIMIT_RPS` | `20` | **Failed** authentications per **client address**; `0` disables. A token is taken before authentication and refunded unless the response is 401, so the bucket bounds bad credentials rather than traffic: an authenticated client is limited by the per-account and per-tenant ceilings, not by this one. Those ceilings key on a resolved principal, so neither can throttle a failed credential — and each failure costs a database round trip and a hash, uncached. Behind a proxy this keys on the proxy, giving a ceiling on aggregate failed authentications rather than a per-client one: `X-Forwarded-For` is deliberately not read, because a header is attacker-supplied and trusting it would let one client spread its attempts across unlimited keys. |
| `FLEXITYPE_AUTH_RATE_LIMIT_BURST` | `40` | Pre-authentication token-bucket ceiling. |
| `FLEXITYPE_TENANT_RATE_LIMIT_BURST` | `2000` | Token-bucket ceiling for short bursts, per tenant. |

> **The ceilings are per process.** Every bucket lives in a process-local map,
> so a tier of N API replicas admits up to N times the configured rate. That is
> not a caveat added late: `FLEXITYPE_RUN_*` exists so operators run several
> replicas, and the numbers above have to be read as per-replica. Divide by the
> replica count for a fleet-wide ceiling, or put a shared limiter at the edge.


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
