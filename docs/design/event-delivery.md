# Event delivery to external services (standalone mode)

Status: implemented (phases 1 and 2)
Audience: flexitype maintainers; service teams consuming flexitype events

## Problem

Embedded consumers hook the dispatcher in Go (`WithPublisher`,
`WithWebhook`, `WithHandlerFunc`) and get exactly the semantics they wire.
Standalone mode has no equivalent story: one static webhook URL from an
environment variable, no subscription management, no retry policy, no
dead-lettering, no replay. Other services — usually not Go, usually
replicated — need:

1. **Simple onboarding.** One HTTP handler and one secret, or one polling
   loop. No SDK, no broker account, no schema registry.
2. **Live updates.** Milliseconds in the happy path, not poll-interval
   latency.
3. **Production-grade delivery.** Durable, at-least-once, retried with
   backoff, dead-lettered with redrive, observable.
4. **Replica safety on both sides.** flexitype runs N replicas; each
   consumer runs M replicas. No event may be *processed* more than once
   per consuming service (beyond the unavoidable at-least-once window),
   and no event may be lost.

## What exists today (and its gaps)

The transactional outbox (design: `search-indexing.md`) already gives a
durable, ordered event log: envelopes are written in the same transaction
as the change, and relays claim rows with `FOR UPDATE SKIP LOCKED`, so
concurrent flexitype replicas never claim the same row. That part is
sound and stays.

Gaps for external delivery:

| Gap | Consequence |
| --- | --- |
| One `dispatched_at` per envelope, shared by every hook | A failing webhook re-delivers to *all* hooks (duplicates for the healthy ones); one dead consumer holds the row pending forever |
| Failed rows retry on every relay pass, no backoff, no attempt cap | A down consumer generates a request every 2 s indefinitely; the oldest 100 failing rows starve newer events (head-of-line blocking) |
| Dispatch runs inside the claim transaction | Outbound HTTP with a DB transaction open; a slow consumer pins row locks and connections |
| Webhook endpoint is a single env var | No self-service subscription, no event-type filtering, no per-tenant isolation, no secret rotation |
| No replay | A consumer that lost data cannot re-read history; new consumers cannot backfill |

## Delivery models considered

### A. Managed webhook subscriptions (push) — recommended core

Consumers register an HTTPS endpoint (URL + secret + event-type filter);
flexitype delivers each matching envelope as a signed POST, retries with
exponential backoff, dead-letters after a cap, and exposes delivery state
over the API.

- *Onboarding*: one HTTP handler + verify `X-Flexitype-Signature`
  (helper exists: `events.VerifySignature`). Nothing else.
- *Liveness*: relay nudge keeps happy-path latency in milliseconds.
- *Consumer replicas*: the consumer's own load balancer routes each
  delivery to exactly one replica — the M-replica problem disappears for
  push. Retries can duplicate; consumers dedupe on envelope `id`.
- *Producer replicas*: per-delivery rows claimed with SKIP LOCKED —
  same proven pattern as today.

### B. Cursor-based event feed + SSE tail (pull) — recommended companion

`GET /api/v1/events?after=<cursor>` pages the outbox in order;
`GET /api/v1/events/stream` (SSE, `Last-Event-ID` resume) tails it live.
Named server-side cursors with compare-and-swap commits give a consuming
*service* (not each replica) a single logical read position.

- *Onboarding*: `curl` is a working consumer. Good for polyglot stacks,
  backfills, audits, and rebuilding projections.
- *Consumer replicas*: the CAS cursor means two replicas polling
  concurrently cannot both advance; the loser re-reads and its work is
  deduped by envelope id. (Optionally a cursor lease reduces wasted work;
  correctness never depends on it.)
- This is also the **replay** story for webhook consumers: subscriptions
  give liveness, the feed gives history.

### C. Bring-your-own-broker adapters (NATS / Kafka / SNS via config)

Env-configured publishers in the standalone binary. Brokers already solve
consumer groups and replay — but this makes flexitype's operational story
"first deploy a broker", drags driver dependencies into the binary, and
every consumer needs broker credentials. Embedded mode already covers the
teams that want this (`WithPublisher`). Mostly **deferred** — with one
exception: **Google Cloud Pub/Sub ships as a first-class adapter**
(`infrastructure/gcppubsub`, `FLEXITYPE_PUBSUB_PROJECT`/`_TOPIC`/
`_ORDERING`). It is the preferred lane for GCP-resident consumers: one
message per envelope, filterable attributes, optional per-aggregate
ordering keys, and publishing runs inside the outbox expansion so a
broker outage leaves the envelope pending and retried — the same
at-least-once story as every other lane. Other brokers remain
demand-driven.

### D. Direct database tailing (logical decoding / consumers reading the outbox table)

Rejected: couples consumers to flexitype's schema and Postgres
credentials, bypasses tenancy and auth, and makes every migration a
breaking change.

## Recommended architecture

Keep the outbox as the single durable log. Split *what happens after
commit* into two lanes:

```
                     ┌──────────────────────────────────────────────┐
 unit of work ──────▶│ flexitype_event_outbox  (durable, ordered)   │
 (same tx as change) └──────────┬───────────────────────────────────┘
                                │ claim (lease) ▸ dispatch (no lock)
                                │ ▸ finalize (advisory-locked, stamps feed_seq)
              ┌─────────────────┼──────────────────────────────┐
              ▼                 ▼                              ▼
   in-process hooks    flexitype_webhook_delivery     events feed / SSE
   (search indexer,    one row per (envelope ×        (reads by feed_seq;
   embedded handlers)  subscription); claimed         named CAS cursors)
                       SKIP LOCKED by delivery
                       workers, backoff + DLQ
```

### 1. Expansion: one sequencer, horizontally scaled delivery

A relay pass runs three steps and holds the sequencer lock only for the
last, DB-only one:

1. **Claim** — a single `UPDATE … RETURNING` leases a batch of pending
   rows to this relay (`claimed_by`/`claimed_at`), skipping rows another
   relay holds (`FOR UPDATE SKIP LOCKED`) and reclaiming leases older than
   the TTL (a crashed relay). No lock, no dispatch.
2. **Dispatch** — the in-process handlers (search indexer, embedded
   `WithPublisher`/`WithHandler` hooks) run for the batch **outside any
   transaction or lock**. A slow or failing handler therefore never pins a
   database connection or blocks other relays.
3. **Finalize** — under `pg_advisory_xact_lock` (blocking, since the batch
   is already dispatched and its outcome must be recorded), stamp a
   gap-free `feed_seq` on each success in claim order, insert one
   `webhook_delivery` row per active matching subscription, and mark the
   envelope dispatched. Failures have their attempt counted and their
   lease cleared so a later pass retries them. This step is pure
   row-shuffling with **no network I/O**.

Serializing only finalize keeps `feed_seq` assigned in commit order — feed
cursors stay gap-safe (no "row with a smaller id commits later and is
skipped forever") — while moving handler I/O off the critical section.
Finalize re-reads the still-pending rows, so a batch that was
double-claimed after a lease expiry is expanded at most once (no duplicate
`feed_seq`, no duplicate delivery rows). With multiple relays, `feed_seq`
order can differ from insert order but remains monotonic and gap-free;
with a single relay it is identical to insert order.

### 2. Delivery workers: claim → HTTP outside any tx → record

Every flexitype replica runs delivery workers that:

1. claim due deliveries: `WHERE status='pending' AND next_attempt_at <=
   now() ... FOR UPDATE SKIP LOCKED`, set `status='inflight'` with a
   lease deadline, **commit** (short tx);
2. POST the envelope (no transaction open) with a per-request timeout;
3. record the outcome in a second short tx: success → `delivered`;
   failure → `pending` with `attempts+1`, `next_attempt_at = now() +
   backoff(attempts)` (exponential, jittered, capped — e.g. 1s, 4s, 16s,
   … 15m), until `max_attempts` (default 25 ≈ 3 days) → `dead`.

Crash between (2) and (3) leaves an `inflight` row whose lease expires; a
reaper returns it to `pending`. That is the at-least-once window —
irreducible without consumer-side idempotency, hence the envelope-id
dedupe contract below. Per-subscription failures never touch other
subscriptions or newer envelopes for healthy ones: head-of-line blocking
is gone because retry scheduling is per-row.

Ordering: deliveries for one subscription are claimed in `feed_seq` order
and a subscription is delivered by at most one worker at a time
(per-subscription claim grouping), so consumers observe per-subscription
order except across retries — the documented guarantee is **ordered
per subscription in the happy path, at-least-once always, dedupe and
reorder tolerance by envelope id + `occurred_at`**. Consumers needing
strict per-aggregate ordering compare `occurred_at`/envelope ULID and
discard stale updates — the same rule the search indexer already applies
(rebuild-from-truth idempotency).

### 3. Subscription management API

```
POST   /api/v1/webhook-subscriptions        {name, url, secret, event_types[], active}
GET    /api/v1/webhook-subscriptions
PATCH  /api/v1/webhook-subscriptions/{id}   (rotate secret: two active secrets, staged)
DELETE /api/v1/webhook-subscriptions/{id}
GET    /api/v1/webhook-subscriptions/{id}/deliveries?status=dead|pending&cursor=
POST   /api/v1/webhook-deliveries/{id}/redeliver
```

Tenant-scoped like every other resource; service-account auth applies.
Subscriptions live in the DB (`flexitype_webhook_subscription`), so all
replicas see the same set — no config drift between replicas. Secret
rotation keeps `secret` + `previous_secret` both signing-valid for a
grace window.

### 4. Events feed + cursors

```
GET  /api/v1/events?after=<feed_seq>&types=a,b&limit=100     → ordered page + next cursor
GET  /api/v1/events/stream?after=<feed_seq>                  → SSE; id: <feed_seq> per event
PUT  /api/v1/event-cursors/{consumer}                        {position, expected_position}  → 409 on CAS miss
GET  /api/v1/event-cursors/{consumer}
```

Feed reads only expanded rows (`feed_seq IS NOT NULL`) ordered by
`feed_seq` — gap-free by construction (§1). SSE is the live-updates path
for UIs and single-consumer services; replicated services poll the feed
and commit the shared CAS cursor. Retention: a background job prunes
delivered outbox rows past `FLEXITYPE_EVENT_RETENTION` (default 7 days);
the feed 410s cursors older than retention so consumers know to
re-baseline (reindex-style) instead of silently missing events.

### 5. The consumer contract (documented, with copy-paste snippets)

- Return 2xx fast; process async. Anything else (or timeout) is a retry.
- Verify `X-Flexitype-Signature` (HMAC-SHA256, existing helper) and
  reject stale `X-Flexitype-Timestamp` (> 5 min) to block replays.
- **Idempotency**: `INSERT INTO processed_events(id) ... ON CONFLICT DO
  NOTHING` guards processing — envelope `id` is the dedupe key across
  retries, redrives, and feed re-reads. This single rule is what makes
  "many flexitype replicas × many consumer replicas" safe end-to-end.
- Headers: `X-Flexitype-Event` (type), `X-Flexitype-Delivery` (delivery
  id, unique per attempt), envelope in the body — same wire format as
  embedded hooks and the feed. One schema everywhere.

### Duplication analysis (the matrix that matters)

| Scenario | Mechanism | Outcome |
| --- | --- | --- |
| N flexitype replicas expanding | advisory-locked sequencer | one expander at a time; others skip |
| N flexitype replicas delivering | SKIP LOCKED delivery claims | each delivery attempt owned by one worker |
| flexitype crash mid-delivery | inflight lease + reaper | redelivered → consumer dedupes by envelope id |
| M consumer replicas (webhook) | consumer's own LB | one replica per attempt |
| M consumer replicas (feed) | CAS cursor commit | one logical reader; losers re-read, dedupe by id |
| Redrive / replay | same envelope id | consumer dedupe absorbs it |

Exactly-once *processing* is achieved in the only place it can be: the
consumer's own transaction, keyed by envelope id. Everything upstream is
at-least-once by design and honest about it.

### Observability & operations

- Metrics: outbox depth, expansion lag (now − oldest pending), per-
  subscription delivery lag / attempt histogram / dead count, feed cursor
  lag per named consumer.
- `GET /readyz` unchanged; a `warn` health check when dead deliveries > 0
  or expansion lag exceeds a threshold.
- Console (later phase): subscriptions screen with delivery log, dead-
  letter redrive button, per-subscription lag sparkline — same pattern as
  the activity screen.

## Phasing

1. **Outbox rework + webhook subscriptions** (schema `000005`): expansion
   with `feed_seq`, `flexitype_webhook_subscription` +
   `flexitype_webhook_delivery`, delivery workers with backoff/DLQ/lease
   reaper, subscription CRUD + redrive API, consumer-contract docs.
   Existing env-var webhook becomes a bootstrap-time subscription upsert
   (backwards compatible).
2. **Events feed + SSE + CAS cursors + retention.**
3. **Broker adapters (config-driven), console screens.**

Phase 1 alone meets the stated requirements; phase 2 adds replay and the
pull option; phase 3 is demand-driven.
