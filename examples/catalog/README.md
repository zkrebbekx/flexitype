# Example: a product catalog (PLM-style)

An end-to-end walkthrough of flexitype using a realistic product catalog:
type inheritance, a unique key, an enum, a cascading dependency, a
symmetric relationship, FQL queries, and a signed webhook consumer. Every
command here is verified against the running stack.

```
product (base)
├── name    string   required, max 200
├── sku     string   unique
├── status  enum     draft | active | discontinued
├── book         (extends product)
│   ├── author  string
│   └── pages   integer
└── electronics  (extends product)
    ├── voltage          integer
    └── warranty_months  integer

dependency:    status = "active"  ⇒  sku becomes required
relationship:  compatible_with    (symmetric, electronics ↔ electronics)
```

## Run it

```bash
cd examples/catalog
docker compose up --build          # flexitype + Postgres + webhook consumer
```

This starts three containers:

- **flexitype** on `:8080` with the outbox and search index on. Because
  `FLEXITYPE_WEBHOOK_URL` points at the consumer, it registers a **managed
  webhook subscription** (retries, backoff, dead-lettering) signed with a
  shared secret.
- **postgres** — the datastore.
- **consumer** — the reference receiver in [`consumer/`](consumer/): it
  verifies the HMAC signature, rejects unsigned or stale requests, dedupes
  redeliveries by event id, and logs each event.

In another terminal, seed the schema and some data:

```bash
./seed.sh                          # imports schema.json, writes values, runs FQL
docker compose logs -f consumer    # watch signed events arrive
```

## What the walkthrough shows

### 1. Modeling with a portable schema bundle

The schema lives in [`schema.json`](schema.json) — types, attributes, the
dependency and the relationship, all keyed by internal name. `seed.sh`
imports it:

```bash
curl -X POST --data-binary @schema.json http://localhost:8080/api/v1/schema/import
```

Import is idempotent, so re-running is safe. This is the same bundle you'd
commit to version control and promote from the playground to production.

### 2. Inheritance

`book` and `electronics` **extend** `product`, so they inherit `name`,
`sku` and `status`. Setting a book's `status` uses the attribute declared
on `product`; a query rooted at `product` sees books and electronics alike.
The seed resolves inherited attributes through the
`…/effective-attributes` endpoint (which returns own **and** inherited
attributes), not `…/attributes` (own only).

### 3. Bulk value writes

A whole product is written in one atomic batch:

```bash
curl -X POST http://localhost:8080/api/v1/values/batch -d '{"items":[ … ]}'
```

Either every value lands (and its events fire) or the batch rolls back.

### 4. Cascading dependency

`status = "active"` makes `sku` required. Inspect an entity's effective
schema to see the rule take effect, or try setting `status` to `active`
with no `sku` and watch it fail validation.

### 5. FQL

Query across the hierarchy — enum, comparison, inheritance:

```bash
# Books over 300 pages
curl -G http://localhost:8080/api/v1/query \
  --data-urlencode 'type=book' --data-urlencode 'q=pages > 300'

# Active products of any subtype
curl -G http://localhost:8080/api/v1/query \
  --data-urlencode 'type=product' --data-urlencode 'q=status = "active"'
```

### 6. Consuming events — webhook and feed

**Webhook** (push): the consumer in [`consumer/`](consumer/) is the
reference implementation. The essentials:

```go
if !events.VerifyRequest(secrets, ts, body, sig, 5*time.Minute, time.Now()) {
    http.Error(w, "invalid signature", http.StatusUnauthorized) // never process unsigned data
    return
}
// ack fast (2xx), dedupe on env.ID (delivery is at-least-once)
```

**Feed** (pull): the same events are also readable over the ordered feed,
which is ideal for a consumer that prefers to poll and track its own
cursor:

```bash
# Read everything after feed position 0
curl 'http://localhost:8080/api/v1/events?after=0'

# Or tail live over SSE (resumable with Last-Event-ID)
curl -N 'http://localhost:8080/api/v1/events/stream?after=0'

# Commit a named cursor (compare-and-swap) so you resume exactly where you left off
curl -X PUT http://localhost:8080/api/v1/event-cursors/my-consumer \
  -d '{"position": 42, "expected": 0}'
```

Pick **webhooks** when you want flexitype to push and manage retries;
pick the **feed** when you want to control consumption rate and replay.
