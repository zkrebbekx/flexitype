-- Entity-summary projection: one materialised row per live entity carrying its
-- live value count and most-recent change. The entity browser (ListEntities)
-- and FQL-free facet resolution previously derived this on every page with a
-- GROUP BY over ALL live value rows of the type (seq-scan + hash-aggregate),
-- whose cost grows with the entity population — seconds at 10M rows. This table
-- lets a page be a keyset Index Scan of exactly `limit` rows.
--
-- The projection is maintained by a row trigger on flexitype_attribute_value,
-- not by application code, so it stays correct across EVERY write path (single
-- Set, overwrite, archive, hard delete/purge, snapshot apply, computed
-- materializer, bulk) with no drift.
CREATE TABLE flexitype_entity_summary (
    tenant_id          TEXT NOT NULL,
    type_definition_id CHAR(26) NOT NULL,
    entity_id          TEXT NOT NULL,
    value_count        INTEGER NOT NULL,
    last_updated_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, type_definition_id, entity_id)
);

-- The entity browser pages newest-first within one or more subtypes; entity_id
-- is the unique tiebreaker. Ordering last_updated_at DESC in the index lets the
-- keyset page (ORDER BY last_updated_at DESC, entity_id) read straight off it.
CREATE INDEX idx_flexitype_entity_summary_order
    ON flexitype_entity_summary (tenant_id, type_definition_id, last_updated_at DESC, entity_id);

-- Recompute one entity's summary from its live value rows. Called by the
-- trigger for the affected (tenant, type, entity) key. When the entity has no
-- live values left the summary row is removed so it drops out of the browser;
-- otherwise it is upserted with the fresh count and most-recent change.
CREATE OR REPLACE FUNCTION flexitype_refresh_entity_summary(
    p_tenant text, p_type char(26), p_entity text
) RETURNS void AS $$
DECLARE
    v_count integer;
    v_last  timestamptz;
BEGIN
    SELECT count(*), max(updated_at)
      INTO v_count, v_last
      FROM flexitype_attribute_value
     WHERE tenant_id = p_tenant
       AND type_definition_id = p_type
       AND entity_id = p_entity
       AND archived_at IS NULL;

    IF v_count = 0 THEN
        DELETE FROM flexitype_entity_summary
         WHERE tenant_id = p_tenant
           AND type_definition_id = p_type
           AND entity_id = p_entity;
    ELSE
        INSERT INTO flexitype_entity_summary
            (tenant_id, type_definition_id, entity_id, value_count, last_updated_at)
        VALUES (p_tenant, p_type, p_entity, v_count, v_last)
        ON CONFLICT (tenant_id, type_definition_id, entity_id)
        DO UPDATE SET value_count     = EXCLUDED.value_count,
                      last_updated_at = EXCLUDED.last_updated_at;
    END IF;
END;
$$ LANGUAGE plpgsql;

-- AFTER-row trigger body: refresh the summary for the row's key. INSERT/UPDATE
-- carry the affected key in NEW; DELETE in OLD. A value row's identity columns
-- (tenant/type/entity) never change in normal operation, but if an UPDATE ever
-- moved a row to a different key, the old key must be recomputed too. Returns
-- NULL because an AFTER trigger's return value is ignored.
CREATE OR REPLACE FUNCTION flexitype_entity_summary_trg() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM flexitype_refresh_entity_summary(OLD.tenant_id, OLD.type_definition_id, OLD.entity_id);
    ELSE
        PERFORM flexitype_refresh_entity_summary(NEW.tenant_id, NEW.type_definition_id, NEW.entity_id);
        IF TG_OP = 'UPDATE'
           AND (OLD.tenant_id, OLD.type_definition_id, OLD.entity_id)
               IS DISTINCT FROM
               (NEW.tenant_id, NEW.type_definition_id, NEW.entity_id) THEN
            PERFORM flexitype_refresh_entity_summary(OLD.tenant_id, OLD.type_definition_id, OLD.entity_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER flexitype_entity_summary_maintain
    AFTER INSERT OR UPDATE OR DELETE ON flexitype_attribute_value
    FOR EACH ROW EXECUTE FUNCTION flexitype_entity_summary_trg();

-- Backfill existing data. The migration runs in one transaction with no
-- concurrent writers, so a single grouped INSERT captures every live entity.
-- ON CONFLICT DO NOTHING keeps it idempotent against any rows the trigger may
-- have already produced.
INSERT INTO flexitype_entity_summary
    (tenant_id, type_definition_id, entity_id, value_count, last_updated_at)
SELECT tenant_id, type_definition_id, entity_id, count(*), max(updated_at)
  FROM flexitype_attribute_value
 WHERE archived_at IS NULL
 GROUP BY tenant_id, type_definition_id, entity_id
ON CONFLICT (tenant_id, type_definition_id, entity_id) DO NOTHING;
