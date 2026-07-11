# flexitype

Soft types and attributes for Go: define entity types, typed and constrained
attributes, and attribute dependencies at **runtime** — then attach validated
values to your own domain objects. Inspired by PLM-class flexible attribute
systems, built as a production-grade DDD Go service.

Runs two ways from one codebase:

- **Embedded library** — wire it into your service over your own `*sqlx.DB`
- **Standalone service** — a single binary with a versioned REST API,
  service-account auth, OpenTelemetry and health endpoints

**[▶ Try the playground](https://zkrebbekx.github.io/flexitype/)** — the full
service (usecases, REST API, FQL search, search index) compiled to
WebAssembly, running the admin console entirely in your browser. No backend;
data resets on reload.

## Features

- **Soft types**: `TypeDefinition` → `AttributeDefinition` → `AttributeValue`,
  anchored to *your* entities via an opaque `entity_id`
- **12 data types**: bool, string, integer, float, decimal (arbitrary
  precision), date, time, datetime, enum, url, email, json
- **Constraints**: min/max length, min/max value, RE2 pattern, one-of, plus
  required / multi-valued / unique attribute flags
- **Attribute dependencies**: cascading picklists and conditional validation —
  when a source attribute matches conditions (equals / in / range / pattern /
  dynamic time), the target's allowed values narrow, constraints tighten or
  required flips; resolve the *effective schema* per entity for building UIs
- **Dynamic values**: `now` / `today` / relative-time defaults and conditions
- **Domain events**: aggregates return `[]events.Event`; a dispatcher fans a
  stable JSON envelope out to **your** infrastructure — pub/sub brokers,
  HMAC-signed webhooks, or plain funcs
- **Activity log**: every change audited with JSON before/after descriptors,
  written in the *same transaction* as the change
- **Dataloaders throughout the repositories**: point lookups batch into
  `ANY()` queries, identical filter+page queries deduplicate, per-parent
  pagination collapses into one windowed query
- **Type inheritance**: single-inheritance hierarchies (`MountainBike
  extends Bike extends Product`) — subtypes inherit every attribute,
  constraint and dependency; no shadowing anywhere in a hierarchy; values
  anchor to the entity's declared type; uniqueness applies hierarchy-wide;
  dependencies and relationships work across levels
- **Relationships between types**: user-defined relationship types with a
  parent side and a child side, their own attributes and constraints (the
  full attribute machinery applies to links), definition inheritance, and
  per-link version binding — track the latest parent/child type version or
  pin a specific one
- **FQL, a schema-aware query language**: query entities by attribute
  values and across relationships — `category = "bike" and (min(price) >=
  500 or "sale" in tags) and child(supplied_by) { link.lead_time_days <=
  14 }`. Comparisons, `in`, `range`, `has`, `length`, `min`/`max`/`count`,
  case-insensitive string matching, `and`/`or`/`not` with parentheses,
  `type isa` hierarchy matching. Names bind against the (inherited) schema
  with positioned errors; archived types, attributes and entities are
  invisible. See `docs/design/query-language.md`.
- **Transactional outbox** (optional): event envelopes persist in the same
  transaction as the change and a relay dispatches them with retries —
  at-least-once delivery for every hook (webhooks, pub/sub, the search
  indexer). `FLEXITYPE_OUTBOX=true` or `flexitype.WithOutbox()` +
  `Service.RunOutboxRelay`.
- **Event delivery for other services** (with the outbox): managed webhook
  subscriptions (`/api/v1/webhook-subscriptions`) with signed deliveries,
  exponential backoff, dead-lettering and redrive; plus a cursor-paged
  events feed (`/api/v1/events`), an SSE live tail and named
  compare-and-swap cursors so replicated consumers read as one. Safe with
  any number of flexitype replicas. Design: `docs/design/event-delivery.md`.
- **Search index** (optional): an event-driven projection keeps one
  searchable document per entity, unlocking FQL `matches("free text")` and
  `POST /api/v1/search/reindex`; trigram indexes accelerate
  `contains`/`icontains` everywhere. `FLEXITYPE_FEATURE_SEARCH_INDEX=true`
  or `flexitype.WithSearchIndex()`. Design:
  `docs/design/search-indexing.md`.
- **Feature toggles**: search and activity history switch off per
  deployment (`FLEXITYPE_FEATURE_SEARCH`, `FLEXITYPE_FEATURE_ACTIVITY`, or
  `flexitype.WithoutSearch()` / `WithoutActivityLog()` when embedding);
  the console adapts automatically.
- **Admin console**: a built-in Vue 3 UI at `/` for modelling types,
  attributes, dependencies and relationships, browsing entities with
  dependency-aware value editing, and auditing every change with
  before/after diffs
- **Multi-tenant** from day one; **definition versioning** with values pinned
  to the version they were validated against

## Architecture

```
domain/          Aggregates, value objects, constraints, events, repo ports
application/     Usecases (interactors), common factory, unit of work,
                 activity log contract, actor/tenant context
infrastructure/  PostgreSQL repositories (dataloader-backed), migrations,
                 activity log, embedded migration runner
internal/.../http REST API for the standalone service
pkg/             Reusable primitives: ulid, db (Transactor + commit hooks),
                 dataloader, events (dispatcher + hooks), logger, config,
                 telemetry, health, shutdown, serviceaccount
cmd/flexitype    Composition root for the standalone service
flexitype.go     Embedding facade
```

Every write flows through the **unit of work**: the usecase opens the
transaction, repositories join it (`WithTx`, `GetForUpdate` row locks), and
the common factory registers three commit handlers —

1. **pre-commit** → activity-log rows written inside the transaction
2. **post-commit** → domain events dispatched to your hooks (only after the
   change is durable)
3. **rollback** → observability hook

## Embedded usage

```go
import (
    "github.com/jmoiron/sqlx"
    _ "github.com/lib/pq"

    "github.com/zkrebbekx/flexitype"
    "github.com/zkrebbekx/flexitype/pkg/events"
)

pool, _ := sqlx.Connect("postgres", dsn)

svc := flexitype.New(pool,
    // Route events into your broker (NATS, Kafka, SNS, ...).
    flexitype.WithPublisher("nats", myNATSPublisher, nil),
    // Or deliver signed webhooks.
    flexitype.WithWebhook("billing", events.WebhookConfig{
        URL:    "https://billing.internal/hooks/flexitype",
        Secret: os.Getenv("HOOK_SECRET"),
    }),
    // Or just run a func.
    flexitype.WithHandlerFunc("cache-invalidator", func(ctx context.Context, env events.Envelope) error {
        cache.Invalidate(env.AggregateID)
        return nil
    }, events.WithEventTypes(value.EventUpdated)),
)

_ = svc.Migrate(ctx) // embedded migrations, advisory-locked, idempotent

// One interactor set per request/unit of work (fresh dataloader caches).
interactors := svc.Interactors(ctx)
product, _ := interactors.TypeDefinitions().Create(ctx, typedef.CreateInput{
    InternalName: "product",
    DisplayName:  "Product",
})
```

For tests and prototypes, `flexitype.NewInMemory(...)` takes the same
options and runs the identical usecases over an in-process store — no
database, no migrations. It powers the browser playground and makes a
zero-dependency test double for embedding consumers.

Consumers on other stacks integrate via the standalone service's REST API and
webhooks; every subscriber sees the same envelope:

```json
{
  "id": "01J...",
  "type": "flexitype.attribute_value.updated",
  "aggregate_type": "attribute_value",
  "aggregate_id": "01J...",
  "tenant_id": "acme",
  "actor": "service_account:ci-importer",
  "occurred_at": "2026-07-11T10:00:00Z",
  "recorded_at": "2026-07-11T10:00:00.003Z",
  "schema_version": 1,
  "payload": { "old_value": "SN-100", "new_value": "SN-200", "...": "..." }
}
```

Webhook deliveries carry `X-Flexitype-Signature` (hex HMAC-SHA256 of the
body); verify with `events.VerifySignature`.

## Standalone service

```bash
go build -o flexitype ./cmd/flexitype
FLEXITYPE_DB_HOST=localhost FLEXITYPE_DB_NAME=flexitype ./flexitype
```

Configuration is environment-driven (`FLEXITYPE_PORT`, `FLEXITYPE_DB_*`,
`FLEXITYPE_SERVICE_ACCOUNTS`, `FLEXITYPE_WEBHOOK_URL`/`_SECRET`,
`FLEXITYPE_OUTBOX`, `FLEXITYPE_EVENT_RETENTION`,
`FLEXITYPE_MIGRATE_ON_START`, `FLEXITYPE_LOG_LEVEL`). Tracing follows the
standard `OTEL_EXPORTER_OTLP_ENDPOINT`. Liveness at `/healthz`, readiness
(with a database probe) at `/readyz`.

### Consuming events from another service

With `FLEXITYPE_OUTBOX=true`, other services subscribe over the API — no
broker, no SDK. Register an endpoint:

```bash
curl -X POST /api/v1/webhook-subscriptions -d '{
  "name": "billing",
  "url": "https://billing.internal/hooks/flexitype",
  "secret": "s3cret",
  "event_types": ["flexitype.attribute_value.set", "flexitype.attribute_value.updated"]
}'
```

Every matching envelope arrives as a signed POST, retried with exponential
backoff and dead-lettered (with API redrive) after ~3 days of failures.
The receiving handler needs three things:

1. **Return 2xx fast; process async.** Anything else retries.
2. **Verify the signature** — `events.VerifyRequest(secrets,
   r.Header.Get(events.HeaderTimestamp), body,
   r.Header.Get(events.HeaderSignature), events.DefaultSignatureTolerance,
   time.Now())` checks the HMAC and rejects replays.
3. **Dedupe on the envelope `id`** (`INSERT ... ON CONFLICT DO NOTHING`
   into a processed-events table). Delivery is at-least-once by design;
   this one rule makes N flexitype replicas × M consumer replicas safe.

Pull consumers use the ordered feed instead: `GET /api/v1/events?after=
<cursor>` pages expanded events (`GET /api/v1/events/stream` is the SSE
live tail, resuming via `Last-Event-ID`), and named cursors
(`PUT /api/v1/event-cursors/{consumer}` with `{"position": n, "expected":
m}`) commit progress with compare-and-swap, so replicated consumers read
as one logical consumer. Cursors older than `FLEXITYPE_EVENT_RETENTION`
(default 7 days) get `410 CURSOR_EXPIRED` — re-baseline instead of
silently missing events. Full design: `docs/design/event-delivery.md`.

### Service accounts

Machine-to-machine auth via bearer tokens (`ft_<account>_<secret>`), accounts
declared in a JSON file with SHA-256 secret hashes and `read`/`write`/`admin`
scopes; each account is pinned to a tenant:

```json
[
  {
    "id": "ci",
    "name": "CI Importer",
    "tenant_id": "acme",
    "scopes": ["read", "write"],
    "secret_hash": "<hex sha256 of the secret>"
  }
]
```

No file configured → auth disabled (development mode).

### REST API (v1)

```
GET|POST   /api/v1/type-definitions            PATCH /api/v1/type-definitions/{id}
POST       /api/v1/type-definitions/{id}/archive|restore
GET        /api/v1/type-definitions/{id}/attributes
GET        /api/v1/type-definitions/{id}/effective-attributes
GET        /api/v1/type-definitions/{id}/children
GET|POST   /api/v1/attributes                  PATCH /api/v1/attributes/{id}
POST       /api/v1/attributes/{id}/archive|restore
GET|POST   /api/v1/values                      GET|DELETE /api/v1/values/{id}
GET        /api/v1/entities/{typeDef}/{entity}/values
GET        /api/v1/entities/{typeDef}/{entity}/attributes/{attr}/effective-schema
GET|POST   /api/v1/dependencies                PATCH|DELETE /api/v1/dependencies/{id}
GET        /api/v1/features
GET        /api/v1/query?type=&q=              POST /api/v1/query/validate
GET|POST   /api/v1/relationship-definitions    PATCH /api/v1/relationship-definitions/{id}
POST       /api/v1/relationship-definitions/{id}/archive|restore
GET        /api/v1/relationship-definitions/{id}/attribute-sets
GET|POST   /api/v1/relationships               GET|DELETE /api/v1/relationships/{id}
GET        /api/v1/entities/{typeDef}/{entity}/relationships
GET        /api/v1/activity
GET|POST   /api/v1/webhook-subscriptions       GET|PATCH|DELETE /api/v1/webhook-subscriptions/{id}
GET        /api/v1/webhook-subscriptions/{id}/deliveries?status=
POST       /api/v1/webhook-deliveries/{id}/redeliver
GET        /api/v1/events?after=&types=        GET /api/v1/events/stream (SSE)
GET|PUT    /api/v1/event-cursors/{consumer}
```

Lists paginate with `?limit=` and an opaque `?cursor=`; errors carry stable
machine codes (`VALIDATION`, `NOT_FOUND`, `CONFLICT`, `ARCHIVED`,
`DEPENDENCY_VIOLATION`).

## Example: cascading picklist

```bash
# category: enum(bike, car)      subcategory: enum(mountain, road, sedan, suv)
curl -X POST :8080/api/v1/dependencies -d '{
  "source_attribute_id": "'$CATEGORY'",
  "target_attribute_id": "'$SUBCATEGORY'",
  "conditions": [{"kind": "equals", "value": {"type": "enum", "value": "bike"}}],
  "effect": {"allowed_values": [
    {"type": "enum", "value": "mountain"},
    {"type": "enum", "value": "road"}
  ]}
}'

# With category=bike set on product-9, the UI asks what subcategory may be:
curl :8080/api/v1/entities/$TYPE/product-9/attributes/$SUBCATEGORY/effective-schema
# → {"required":false,"restricted":true,"allowed_values":["mountain","road"], ...}
```

## Admin console

The standalone service ships a built-in admin console at `/` (the API stays
under `/api/v1`). Develop it with the Go service running:

```bash
cd web && npm ci && npm run dev   # http://localhost:5173, proxies /api
```

Production builds embed the console into the binary: `npm run build` in
`web/`, then `go build ./cmd/flexitype`. A committed stub keeps `go build`
working without Node.

## Development

```bash
go build ./...   # everything compiles without a database
go test ./...    # goconvey Given/When/Then suites
go vet ./...
cd web && npm test && npm run build   # console tests + typecheck + bundle
```

Storage is a single polymorphic value table with one typed, indexed column
per storage class — no table-per-type explosion, uniqueness probes stay
index-backed, and entity hydration is one composite-index scan.

## License

MIT
