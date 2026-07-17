DROP INDEX IF EXISTS idx_flexitype_attribute_value_decimal;
DROP INDEX IF EXISTS idx_flexitype_attribute_value_tenant_entity;
DROP INDEX IF EXISTS idx_flexitype_webhook_delivery_pending;

-- Restore the pg_trgm GIN indexes (best-effort, matching 000004).
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_trgm
                 ON flexitype_attribute_value USING GIN (value_text gin_trgm_ops)
                 WHERE value_text IS NOT NULL';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_trgm_lower
                 ON flexitype_attribute_value USING GIN (lower(value_text) gin_trgm_ops)
                 WHERE value_text IS NOT NULL';
    END IF;
END $$;
