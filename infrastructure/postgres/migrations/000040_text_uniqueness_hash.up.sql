-- +flexitype:no-transaction
--
-- Built CONCURRENTLY on flexitype_attribute_value, the largest table in the
-- database, where a plain CREATE INDEX takes a lock conflicting with every
-- write and holds it for the whole build. See docs/upgrades.md.

-- A `text` attribute could not store a long value at all (issue #590).
--
-- The uniqueness probe index was a plain btree over the raw value:
--
--   (attribute_definition_id, value_text) WHERE value_text IS NOT NULL
--
-- so every text-backed row had to fit a btree tuple, and Postgres refused the
-- INSERT past roughly 2.7KB with "index row size ... exceeds btree version 4
-- maximum" (SQLSTATE 54000). That surfaced as HTTP 500, and only on Postgres:
-- the in-memory backend stores the value happily, so no in-memory test could
-- see it. `text` exists to hold long-form values, so the storage class made
-- the data type useless.
--
-- The probe only ever asks for equality, so it does not need the value in the
-- index. Hashing it removes the length limit and keeps the lookup indexed; the
-- query still compares the full value as well, so a hash collision cannot make
-- two different values look equal.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_uniq_text_hash
    ON flexitype_attribute_value (attribute_definition_id, md5(value_text))
    WHERE archived_at IS NULL AND value_text IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_uniq_text;
