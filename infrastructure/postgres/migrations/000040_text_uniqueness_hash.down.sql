-- +flexitype:no-transaction
--
-- Restoring the raw-value index reinstates the ~2.7KB ceiling, so a database
-- already holding a longer text value cannot go back. That is the direction
-- the ceiling makes impossible, not a defect in this file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_uniq_text
    ON flexitype_attribute_value (attribute_definition_id, value_text)
    WHERE archived_at IS NULL AND value_text IS NOT NULL;

DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_uniq_text_hash;
