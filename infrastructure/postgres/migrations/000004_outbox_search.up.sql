-- Transactional event outbox: envelopes written in the same transaction as
-- the change they describe, dispatched by a relay. Upgrades every
-- dispatcher consumer (webhooks, pub/sub, the search indexer) from
-- at-most-once to at-least-once delivery.
CREATE TABLE flexitype_event_outbox (
    id             CHAR(26) PRIMARY KEY,
    tenant_id      TEXT NOT NULL DEFAULT 'default',
    actor          TEXT NOT NULL DEFAULT '',
    event_type     TEXT NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id   TEXT NOT NULL,
    payload        JSONB NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL,
    dispatched_at  TIMESTAMPTZ,
    attempts       INTEGER NOT NULL DEFAULT 0,
    last_error     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_flexitype_event_outbox_pending
    ON flexitype_event_outbox (id)
    WHERE dispatched_at IS NULL;

-- Search projection: one document per entity, rebuilt by the indexer from
-- value events. text_vector powers FQL matches(); document holds the
-- flattened live values.
CREATE TABLE flexitype_entity_search (
    tenant_id          TEXT NOT NULL,
    type_definition_id CHAR(26) NOT NULL,
    entity_id          TEXT NOT NULL,
    document           JSONB NOT NULL DEFAULT '{}'::jsonb,
    text_vector        tsvector NOT NULL DEFAULT ''::tsvector,
    updated_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, entity_id)
);

CREATE INDEX idx_flexitype_entity_search_vector
    ON flexitype_entity_search USING GIN (text_vector);
CREATE INDEX idx_flexitype_entity_search_type
    ON flexitype_entity_search (tenant_id, type_definition_id);

-- Trigram indexes make FQL contains/icontains index-assisted instead of
-- sequential. Extension creation needs elevated privileges on some managed
-- providers; degrade to a notice rather than failing the migration.
DO $$
BEGIN
    BEGIN
        CREATE EXTENSION IF NOT EXISTS pg_trgm;
    EXCEPTION WHEN insufficient_privilege THEN
        RAISE NOTICE 'pg_trgm unavailable (insufficient privileges); contains/icontains stay unindexed';
    END;

    IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_trgm
                 ON flexitype_attribute_value USING GIN (value_text gin_trgm_ops)
                 WHERE value_text IS NOT NULL';
        EXECUTE 'CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_trgm_lower
                 ON flexitype_attribute_value USING GIN (lower(value_text) gin_trgm_ops)
                 WHERE value_text IS NOT NULL';
    END IF;
END $$;
