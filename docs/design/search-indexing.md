# Search indexing — investigation

**Question**: FQL executes as SQL against the transactional tables today.
Should there be an optional search index written alongside the
transactional database, and what is the right architecture if so?

**TL;DR**: Yes — as an *optional, consumer-pluggable projection* built on
the event dispatcher flexitype already ships, hardened with a transactional
outbox. Do not adopt an external engine as a core dependency, and do not
put one on the synchronous write path. Postgres-native denormalisation is
the first escape valve; an external engine is the second, and both hang
off the same seam.

## Where the transactional path stops scaling

FQL compiles to `EXISTS` subqueries per condition over
`flexitype_attribute_value`, which is exactly what the partial indexes are
shaped for. This is correct-by-construction (queries always see committed
truth) and fine well past tens of millions of value rows for selective
queries. It degrades when:

1. queries are *unselective* (`icontains` over a large tenant — `strpos`
   cannot use a btree; every candidate row is visited),
2. many conditions AND-ed over huge entities multiply subquery cost,
3. search traffic starts competing with transactional writes for the same
   buffers/IO — the classic OLTP/search contention problem,
4. someone asks for ranking, fuzziness or facet counts, which SQL EXISTS
   trees don't express.

## Options considered

**A. Stay pure-transactional, add targeted Postgres indexes.**
Expression indexes cover hot attributes. Zero new moving parts, zero
staleness. Ceiling: still row-per-value model, still OLTP contention, no
relevance ranking.

*Not* `pg_trgm`. This section used to recommend trigram GIN indexes on
`value_text` first, "regardless". Migration 000018 deletes them, and the
reason stands: `contains`/`icontains` compile to `strpos()`, which no
trigram GIN index can serve. They were pure write-path cost — GIN
maintenance on the hottest table in the database — for zero read benefit.
Making those predicates indexable needs the predicate to change first
(see #333), not another index.

**B. Postgres-native read model (denormalised entity documents).** A
`flexitype_entity_search` table — one row per entity with a `jsonb`
document of its values plus a `tsvector` — maintained inside the same
transaction (pre-commit hook) or asynchronously. Same engine, transactional
consistency available, GIN-indexable. Costs: write amplification on every
value write, jsonb documents duplicate the value table, and it still
shares the OLTP instance's resources. A good middle step for single-node
deployments.

**C. External engine (a hosted or self-run document search service) fed by
flexitype events.** Real ranking/fuzziness/facets, search load isolated
from OLTP. Costs: an eventually-consistent second system, index-schema
management per tenant, operational burden that would land on every
embedded consumer if it were mandatory.

**D. CDC (logical replication / WAL tailing) into an indexer.** Robust
delivery without touching the write path, but it couples consumers to
table shapes rather than domain events, needs replication-slot
operations, and duplicates a capability flexitype already has: a stable,
versioned event envelope emitted post-commit.

## Recommendation

Treat the search index as **a projection subscribed to domain events**, and
make the delivery reliable with an **outbox**:

1. **Contract, not engine.** Define a `SearchIndexer` port (index/remove
   entity documents; a document = entity id, type chain, flattened live
   values, link summaries). Ship two reference implementations: a
   Postgres-native one (option B, default-off) and an example adapter for
   an external engine. Consumers pick per deployment — the same philosophy
   as the pub/sub `Publisher` port.
2. **Outbox for correctness.** Post-commit hooks lose events if the
   process dies between commit and dispatch. Add an optional
   `flexitype_event_outbox` table written by the *pre-commit* handler (same
   transaction as the change) and a relay that dispatches and marks rows.
   This upgrades every dispatcher consumer — webhooks and pub/sub included —
   from at-most-once to at-least-once, and the indexer needs exactly that.
   Idempotency comes free: documents are keyed by entity id and rebuilt
   whole on each event.
3. **Query planner chooses the backend.** FQL stays the single query
   language. The planner sends full-text/fuzzy predicates to the index
   when one is configured and verifies candidate ids against the
   transactional store (index as accelerator, database as truth); without
   an index, everything compiles to SQL as today. Staleness is bounded and
   observable (outbox lag metric) instead of silent.
4. **Rebuild path.** A `reindex` command that streams entities through the
   indexer, for bootstrap and disaster recovery.

Sequencing: the outbox and the `SearchIndexer` port first (they unlock B
and C without churn); option B's reference implementation when a deployment
actually hits the wall; C stays an adapter example, never a core dependency.
No trigram migration — see option A for why the one that shipped was
reverted.

## `matches()` and the field ACL

The projection carries **two** vectors per entity: one over the whole
document, and one per attribute.

A principal that reads everything searches the entity-level vector — one row,
one index probe. A principal whose policy hides an attribute searches the
per-attribute vectors, restricted to the names it may read, plus the entity
id (an id is not an attribute and no policy hides it).

The split exists because the entity-level document is a flattening of every
textual value with no attribute identity. A search over it could not be
filtered the way a named condition is, so a principal denied an attribute
recovered its content one word at a time: `contains(internal_notes, …)` was
refused as unknown while `matches("recall")` returned the entity. The first
fix refused `matches()` for any restricted principal, which closed the leak
and removed the feature from the deployments that use field permissions; the
per-attribute vectors restore it.

**Cost.** An entity with N textual attributes carries N+1 index rows instead
of one, and a write rewrites that entity's rows. The restricted query reads a
GIN index and filters by attribute name.

Entities indexed before the split are carried over by the
`000037_entity_search_attr` backfill, which derives the rows from the
document already stored beside them. Until it completes, a restricted
principal finds nothing for an entity not yet carried over — the safe
direction — and an unrestricted principal is unaffected.

## Backend parity of `matches()`

`matches("free text")` is full-text search over the entity's searchable
document. Its exact **tokenization differs between backends**, so it is the one
FQL construct that is *approximate* rather than byte-for-byte identical across
memory and Postgres:

- **Postgres** tokenizes with `to_tsvector('simple', …)` / `plainto_tsquery`.
  Postgres's text-search parser recognizes compound tokens — e.g. an email
  `jane@example.com` is a single token, so `matches("jane")` does **not** match
  it.
- **In-memory** tokenizes by splitting on non-alphanumeric runs, so the same
  document yields `jane`, `example`, `com` and `matches("jane")` **does** match.

Every other FQL construct (comparisons, `in`, `range`, `has`, `contains` /
`icontains` / `iequals`, `length`, `type` / `isa`, decimals, dates, quantities,
NULL three-valued logic) is verified to return identical results on both
backends by the `TestFQLMemoryPostgresParity` corpus. `matches()` is
deliberately excluded from that equality assertion: treat it as best-effort
relevance search, not an exact predicate, and prefer `contains`/`iequals` when
you need deterministic, backend-identical matching.

> Note on read-your-writes: the search projection and computed-attribute
> materialization are maintained **synchronously**, in the writing request's
> post-commit, in both delivery modes. A read issued straight after a write
> reflects it. Projections ride their own dispatcher, separate from the
> delivery bus, so `WithOutbox` does not change this (#211).
>
> This paragraph used to describe an asynchronous window. It no longer
> exists, and a deployment that still polls or retries-on-empty around it is
> carrying a workaround for a race it can never reproduce.
