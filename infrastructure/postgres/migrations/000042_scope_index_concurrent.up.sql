-- Rebuild the scoped-value index CONCURRENTLY.
--
-- +flexitype:no-transaction
--
-- 000014 built this index on flexitype_attribute_value, the largest table in
-- the database, with a plain CREATE INDEX. It could not do better in place:
-- the same file adds four columns with ALTER TABLE, and a no-transaction file
-- must not mix DDL that cannot be replayed with a build that can.
--
-- Splitting it leaves 000014 transactional and this file concurrent. Between
-- the two, entity hydration falls back to a wider index for the length of the
-- upgrade, which is the cost of not locking the table instead.
--
-- IF NOT EXISTS makes this a no-op for a database that already has it.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_scope
    ON flexitype_attribute_value (attribute_definition_id, entity_id, locale, channel);
