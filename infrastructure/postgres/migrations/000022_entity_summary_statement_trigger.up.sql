-- Replace the entity-summary row trigger with statement triggers that take
-- their locks in a canonical order.
--
-- The row trigger from 000019 upserted a shared summary row per
-- (tenant, type, entity) once per affected value row, in whatever order the
-- statement happened to touch them. Two transactions writing to DISJOINT value
-- rows of the same two entities, in opposite order, therefore deadlocked on the
-- summary rows — a conflict that could not occur before the projection existed,
-- introduced by a performance optimisation. Reproduced on Postgres 16: two
-- transactions each inserting two distinct value rows for the same two
-- entities, in opposite order, deadlock with the trigger and do not without it.
--
-- Nothing in the application sorts entity ids, so opposite ordering is the
-- normal case rather than the exception, and a naive retry re-runs the same
-- unordered writes into the same lock ordering.
--
-- A statement trigger over a transition table sees every affected key at once,
-- so it can sort them. Every writer then takes the summary locks in the same
-- sequence and one simply waits. Sorting also collapses the per-row work: a
-- 20-row delete refreshed the same summary 20 times, which is where the
-- measured 2.2 ms of trigger time per PurgeEntity went.

DROP TRIGGER IF EXISTS flexitype_entity_summary_maintain ON flexitype_attribute_value;

CREATE OR REPLACE FUNCTION flexitype_entity_summary_ins_trg() RETURNS trigger AS $$
DECLARE k record;
BEGIN
    FOR k IN SELECT DISTINCT tenant_id, type_definition_id, entity_id
               FROM new_rows
              ORDER BY tenant_id, type_definition_id, entity_id
    LOOP
        PERFORM flexitype_refresh_entity_summary(k.tenant_id, k.type_definition_id, k.entity_id);
    END LOOP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION flexitype_entity_summary_del_trg() RETURNS trigger AS $$
DECLARE k record;
BEGIN
    FOR k IN SELECT DISTINCT tenant_id, type_definition_id, entity_id
               FROM old_rows
              ORDER BY tenant_id, type_definition_id, entity_id
    LOOP
        PERFORM flexitype_refresh_entity_summary(k.tenant_id, k.type_definition_id, k.entity_id);
    END LOOP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- An UPDATE that moved a row to a different key must refresh both, so it reads
-- the union of both transition tables — the same case the row trigger handled
-- by comparing OLD and NEW.
CREATE OR REPLACE FUNCTION flexitype_entity_summary_upd_trg() RETURNS trigger AS $$
DECLARE k record;
BEGIN
    FOR k IN SELECT DISTINCT tenant_id, type_definition_id, entity_id
               FROM (SELECT tenant_id, type_definition_id, entity_id FROM new_rows
                     UNION
                     SELECT tenant_id, type_definition_id, entity_id FROM old_rows) u
              ORDER BY tenant_id, type_definition_id, entity_id
    LOOP
        PERFORM flexitype_refresh_entity_summary(k.tenant_id, k.type_definition_id, k.entity_id);
    END LOOP;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER flexitype_entity_summary_ins
    AFTER INSERT ON flexitype_attribute_value
    REFERENCING NEW TABLE AS new_rows
    FOR EACH STATEMENT EXECUTE FUNCTION flexitype_entity_summary_ins_trg();

CREATE TRIGGER flexitype_entity_summary_upd
    AFTER UPDATE ON flexitype_attribute_value
    REFERENCING NEW TABLE AS new_rows OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION flexitype_entity_summary_upd_trg();

CREATE TRIGGER flexitype_entity_summary_del
    AFTER DELETE ON flexitype_attribute_value
    REFERENCING OLD TABLE AS old_rows
    FOR EACH STATEMENT EXECUTE FUNCTION flexitype_entity_summary_del_trg();
