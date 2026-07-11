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
`pg_trgm` GIN on `value_text` makes `contains`/`icontains` indexable;
expression indexes cover hot attributes. Zero new moving parts, zero
staleness. Ceiling: still row-per-value model, still OLTP contention, no
relevance ranking. *Do this first regardless — it's an index migration,
not an architecture.*

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

Sequencing: `pg_trgm` migration (A) now; outbox + `SearchIndexer` port
next (unlocks B/C without churn); option B reference implementation when a
deployment actually hits the wall; C stays an adapter example, never a
core dependency.
