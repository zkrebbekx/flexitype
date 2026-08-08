-- Float value index + per-attribute value statistics.
--
-- +flexitype:no-transaction
--
-- Two findings from the SaaS-shape load test (stress_saas_test.go, build tag
-- `stress`), run at SAAS_ENTITIES=1_000_000 with its 20 attributes — 20M
-- value rows. The harness defaults to 10M entities (200M rows); the numbers
-- below are from the 1M-entity run.
--
-- 1. value_float was the ONLY storage column without a value index: text,
--    int, time and decimal all have (attribute_definition_id, value_<col>)
--    probes, float does not. Every float comparison — and every QUANTITY
--    comparison, because a quantity's base magnitude lives in value_float —
--    could therefore never be driven from the value side. Measured on 1M
--    entities: the match set for a 0.09%-selective float predicate loads in
--    under 2 ms through this index, against a 200 ms recency-walk without
--    it.
--
-- 2. A storage column holds MANY attributes' values, so PostgreSQL's
--    per-column statistics blend every attribute together and the planner
--    cannot see per-attribute selectivity. It misestimated a 10,000-row
--    integer equality as ~100,000 and mis-sized the plan. Multi-column MCV
--    statistics on (attribute_definition_id, value_<col>) restore accurate
--    equality estimates: the same query dropped from 70 ms to 21 ms from
--    this DDL alone. RANGE predicates on shared columns remain blind —
--    PostgreSQL keeps no per-group histograms — which is a documented
--    residual, not something more DDL can fix.
--
-- CONCURRENTLY (and hence the no-transaction directive) and statement
-- idempotence follow migration 000018's pattern. The runner reaps an INVALID
-- namesake before it replays this file (reapInvalidIndexes in migrate.go),
-- scoped to current_schema(). This file must not carry its own catalogue
-- guard: an earlier in-file DO block matched pg_class.relname without a
-- pg_namespace join, so an invalid index in ANY schema of the database made
-- it fire, and its unqualified DROP INDEX resolved through search_path —
-- into a missing index (error 42704, a boot loop) or into this schema's
-- valid index (a write-blocking drop).

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_uniq_float
    ON flexitype_attribute_value (attribute_definition_id, value_float)
    WHERE archived_at IS NULL AND value_float IS NOT NULL;

CREATE STATISTICS IF NOT EXISTS st_flexitype_attribute_value_int (mcv, dependencies)
    ON attribute_definition_id, value_int FROM flexitype_attribute_value;
CREATE STATISTICS IF NOT EXISTS st_flexitype_attribute_value_float (mcv, dependencies)
    ON attribute_definition_id, value_float FROM flexitype_attribute_value;
CREATE STATISTICS IF NOT EXISTS st_flexitype_attribute_value_text (mcv, dependencies)
    ON attribute_definition_id, value_text FROM flexitype_attribute_value;
CREATE STATISTICS IF NOT EXISTS st_flexitype_attribute_value_time (mcv, dependencies)
    ON attribute_definition_id, value_time FROM flexitype_attribute_value;

-- DECIMAL IS A KNOWN RESIDUAL, deliberately.
--
-- A decimal lives in value_text but is COMPARED as (value_text)::numeric, so
-- the plain-column statistic above cannot serve it: a decimal equality keeps
-- the blended selectivity of every attribute stored as text. The obvious
-- repair — expression statistics on ((value_text)::numeric) — is UNSAFE
-- here. Extended statistics take no WHERE clause, so ANALYZE evaluates the
-- expression over every sampled row of the table, and the first string value
-- fails the cast:
--
--     ERROR: invalid input syntax for type numeric: "Widget"
--
-- That would break ANALYZE for the whole table, autovacuum included, on any
-- deployment holding one non-numeric text value. A CASE guarded by data_type
-- analyzes safely but never matches the query's expression, so it buys
-- nothing.
--
-- Decimal equality still seeks: the partial index on
-- (attribute_definition_id, (value_text::numeric)) over decimal rows serves
-- the probe. Only the ESTIMATE stays blended. Closing that needs decimals in
-- a column of their own, which is a storage change, not a statistics one.

-- The statistics objects hold no data until the next ANALYZE. Run one now so
-- the better estimates apply from this upgrade rather than from whenever
-- autovacuum next samples the table.
ANALYZE flexitype_attribute_value;
