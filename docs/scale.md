# Scale: measured numbers and hot-table maintenance

The scale harness (`stress_test.go`, `-tags stress`) has existed since #212
and no measured results were ever published, so an adopter sizing a
deployment had a tool and no numbers.

**It had also been broken since migration 000022.** That migration replaced
the single row-level entity-summary trigger with three statement-level ones,
and the harness still disabled the old name — so it failed at startup with
`trigger "flexitype_entity_summary_maintain" does not exist`. That is the
likeliest reason nothing was published. It now disables `TRIGGER USER`, which
does not go stale when a trigger is renamed.

## What was measured

These are **measurements, not extrapolations**. Numbers from a bigger dataset
belong in this table only when someone runs it on that dataset.

| | |
|---|---|
| Dataset | 200 000 entities, 600 000 value rows, 20 types (depth 1), 100 attributes |
| Postgres | 16.14 (aarch64), container, default configuration |
| Host | Apple M1 Pro, 32 GiB, local Docker |
| Command | `STRESS_ENTITIES=200000 STRESS_TYPES=20 STRESS_ATTRS=100 STRESS_DEPTH=4 go test -tags stress -run TestStress .` |

### Seed

| | |
|---|---|
| Bulk load (COPY, triggers disabled) | **~22 500 value rows/s** |
| Total seed incl. summary backfill + ANALYZE | 31.5 s for 600 000 rows |

### Read paths (p50 / p95)

| Scenario | p50 | p95 |
|---|---|---|
| `ListByEntity` (single entity, warm) | 1 µs | 3 µs |
| `EffectiveAttributes` (7 attributes) | 2 µs | 42 µs |
| Entity listing, first page | 267 µs | 434 µs |
| Entity listing, cursor page 2 | 350 µs | 604 µs |
| FQL equality (`cint_a = 5`, 100 matches) | 2.8 ms | 19.5 ms |
| FQL range (1 000 matches) | 11.2 ms | 16.6 ms |
| FQL presence `has()` (2 000 matches) | 15.3 ms | 36.6 ms |
| FQL compound boolean (50 matches) | 3.1 ms | 5.7 ms |
| FQL `type isa` subtree (10 000 matches) | 5.9 ms | 9.9 ms |
| GraphQL connection, cached schema | 7.7 ms | 9.4 ms |

### Writes

| Scenario | p50 | p95 |
|---|---|---|
| Set (new value, 2 per iteration) | 5.8 ms | 25.8 ms |
| Set (existing value) | 3.0 ms | 4.3 ms |
| RemoveEntity + reseed | 6.5 ms | 9.7 ms |

**The first GraphQL execution cost 171 ms** — that is the per-tenant schema
build over every type, once, and then 7.7 ms from cache. Budget for it after
a deploy or a schema change, or warm it.

Read cost tracks **matched rows**, not table size: the presence query is the
slowest here because it matches the most. Size against your result sets.

## Hot-table maintenance

`flexitype_attribute_value` is one unpartitioned table and every value write
touches it. Nothing here is automatic — flexitype sets no storage parameters
on your behalf.

**Autovacuum.** The defaults scale by table *fraction*, so a large table is
vacuumed less often in relative terms just as it needs it more. Set absolute
thresholds:

```sql
ALTER TABLE flexitype_attribute_value SET (
    autovacuum_vacuum_scale_factor  = 0.02,
    autovacuum_analyze_scale_factor = 0.01,
    autovacuum_vacuum_cost_limit    = 2000
);
```

The same applies to `flexitype_event_outbox` and
`flexitype_webhook_delivery`, which churn hardest: the retention pruner
deletes from the outbox in batches and every delete leaves a dead tuple.

**fillfactor.** A value update writes a new tuple version. Leaving space in
the page lets Postgres keep it on the same page (a HOT update) and skip the
index write:

```sql
ALTER TABLE flexitype_attribute_value SET (fillfactor = 85);
```

It costs space and only pays off on an update-heavy workload. Measure before
and after.

**Bloat.** Watch `pg_stat_user_tables.n_dead_tup` against `n_live_tup` for the
three tables above. A ratio that climbs while autovacuum runs means the
settings are too conservative for your write rate, not that the table needs
rebuilding.

**Partitioning is not provided.** If the value table outgrows one relation,
partition by `tenant_id` (range or hash) — it leads every index and every
query predicate, so partition pruning is effective and no query needs to
change. That is a deployment decision: flexitype's migrations create a plain
table and will not repartition one for you.
