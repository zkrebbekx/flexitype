-- Rebuild the pg_trgm indexes CONCURRENTLY.
--
-- +flexitype:no-transaction
-- +flexitype:requires-extension pg_trgm
--
-- These two indexes are on flexitype_attribute_value, the largest table in
-- the database, and 000004 and 000021 both built them with a plain CREATE
-- INDEX — a lock that conflicts with every write, held for the whole build.
--
-- Neither file could do better. The build has to be conditional, because
-- gin_trgm_ops does not exist where pg_trgm could not be installed, and the
-- only conditional form in SQL is a DO block. A DO block is a transaction,
-- and CREATE INDEX CONCURRENTLY refuses to run inside one. So the condition
-- moved up to the runner: `requires-extension` skips this file where the
-- extension is absent, which leaves the statements free to be ordinary
-- concurrent builds.
--
-- A skipped file is not recorded, so a deployment that installs pg_trgm later
-- builds these on its next start.
--
-- IF NOT EXISTS makes this a no-op for a database that already has them from
-- 000004 or 000021. A failed concurrent build leaves an INVALID namesake that
-- IF NOT EXISTS would otherwise skip for ever; the runner reaps one before it
-- replays this file (reapInvalidIndexes in migrate.go).
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_trgm
    ON flexitype_attribute_value USING GIN (value_text gin_trgm_ops)
    WHERE value_text IS NOT NULL;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_trgm_lower
    ON flexitype_attribute_value USING GIN (lower(value_text) gin_trgm_ops)
    WHERE value_text IS NOT NULL;
