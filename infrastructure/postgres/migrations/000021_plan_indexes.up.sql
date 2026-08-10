-- Indexes for the query plans corrected in the same change.
--
-- +flexitype:no-transaction
--
-- Every index here is built CONCURRENTLY: three of them are on
-- flexitype_attribute_value, the largest table in the database, where a plain
-- CREATE INDEX would take a lock conflicting with every write and hold it for
-- the whole build. See docs/upgrades.md.
--
-- A failed concurrent build leaves an INVALID namesake behind that
-- IF NOT EXISTS would then skip forever. The runner reaps such an index
-- before it replays this file (reapInvalidIndexes in migrate.go), scoped to
-- current_schema(). This file must not carry its own catalogue guards for
-- the concurrent builds: the earlier in-file DO blocks matched
-- pg_class.relname without a pg_namespace join, so an invalid index in ANY
-- schema of the database made them fire, and their unqualified DROP INDEX
-- resolved through search_path into the wrong schema.

-- Archived-inclusive entity lookups had no usable index. Every entity-leading
-- index is partial on `archived_at IS NULL`, but two paths include archived
-- rows deliberately — PurgeEntity, which must erase them, and media download
-- authorization, whose blob may back an archived value. Both therefore
-- sequentially scanned the value table: measured at 4.16M rows, 573 ms to
-- delete 20 rows, and 252 ms per media authorization. Media download is an
-- ordinary authenticated read that a gallery page issues dozens of times, so
-- that made a page view a repeated full scan of the largest table.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_entity_all
    ON flexitype_attribute_value (tenant_id, entity_id);

-- Media download authorization probes the object key inside the metadata JSON.
-- Without an expression index that is a scan of every media row in the tenant.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_media_key
    ON flexitype_attribute_value (tenant_id, (value_json->>'object_key'))
    WHERE data_type = 'media';

-- The FQL value scope selects one attribute of one entity. The nearest index
-- leads with (tenant_id, entity_id) only, so every candidate entity's whole
-- value set was fetched and then filtered by attribute — visible as
-- "Rows Removed by Filter" on each of hundreds of thousands of probes.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_entity_attr
    ON flexitype_attribute_value (tenant_id, entity_id, attribute_definition_id)
    WHERE archived_at IS NULL;

-- flexitype_webhook_delivery.envelope_id is an ON DELETE CASCADE foreign key.
-- Postgres never indexes the referencing side automatically, so every parent
-- delete sequentially scanned the delivery table: measured at 300k delivery
-- rows, 13.7 ms of cascade per pruned envelope. The retention pruner's
-- NOT EXISTS anti-join needs the same index.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_webhook_delivery_envelope
    ON flexitype_webhook_delivery (envelope_id);

-- Outbox expansion now selects subscriptions by the claimed batch's tenants
-- instead of loading every active subscription in the database.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_webhook_subscription_active
    ON flexitype_webhook_subscription (tenant_id)
    WHERE active;

-- Restore the pg_trgm GIN indexes that 000018 dropped.
--
-- 000018's diagnosis was right — strpos() cannot use a trigram GIN — but it
-- resolved the mismatch by dropping the indexes, which left contains/icontains
-- with no index support at all. The predicate is now LIKE/ILIKE, which these
-- indexes do serve, so the operator is indexable for the first time.
--
-- Extension creation needs elevated privileges on some managed providers;
-- degrade to a notice rather than failing the migration, matching 000004.
DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION WHEN insufficient_privilege THEN
        RAISE NOTICE 'pg_trgm unavailable (insufficient privileges); contains/icontains stay unindexed';
    END;
END $$;

-- The two trgm builds that stood here moved to 000041, which builds them
-- CONCURRENTLY. They were plain builds inside a DO block because the build had
-- to be conditional on pg_trgm and a DO block is the only conditional form in
-- SQL — and a DO block is a transaction, which CREATE INDEX CONCURRENTLY
-- refuses to run inside. 000041 carries the condition as a runner directive
-- (+flexitype:requires-extension), so the statements are ordinary concurrent
-- builds and this file no longer takes a write-blocking lock on the largest
-- table in the database.
--
-- The bespoke invalid-namesake reaper went with them. It existed because the
-- runner's reap covers CREATE INDEX CONCURRENTLY statements only, and these
-- were not; in 000041 they are, so the runner reaps them.
--
-- A database that already ran this file keeps its indexes: 000041 is
-- IF NOT EXISTS.
