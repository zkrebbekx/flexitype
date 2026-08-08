-- Make the entity-summary refresh count AFTER it holds the summary-row lock.
--
-- The 000019 body counted the live value rows FIRST and then upserted the
-- summary row. Under READ COMMITTED the count uses a snapshot taken before
-- the upsert blocks on the row lock a concurrent same-entity writer holds,
-- so the LAST committer wrote a count taken before the other writer's row
-- was visible: two concurrent inserts for one entity could leave
-- value_count = 1 with 2 live rows, until the next write to that entity
-- repaired it.
--
-- The rewritten body takes the row lock first, with a no-op upsert whose
-- only job is to serialize concurrent refreshes of one key. The count runs
-- as a LATER statement, so it gets a fresh snapshot that includes every row
-- committed by the writer it waited on. The final UPDATE (or DELETE, when
-- no live rows remain) then writes a count that is current as of the lock.
--
-- Lock ordering is unchanged: a writer still locks its value rows first and
-- the summary row second, and batch writes already lock entities in one
-- canonical order, so no new deadlock cycle is possible.
--
-- Cost: one extra no-op upsert per refresh — a second write to a row this
-- function wrote anyway, on the entity's own hot key.
CREATE OR REPLACE FUNCTION flexitype_refresh_entity_summary(
    p_tenant text, p_type char(26), p_entity text
) RETURNS void AS $$
DECLARE
    v_count integer;
    v_last  timestamptz;
BEGIN
    -- Serialize refreshes of this key: insert-or-touch the summary row so
    -- this transaction holds its lock before anything is counted. The
    -- placeholder values are never visible outside this transaction; the
    -- statements below overwrite or delete them before commit.
    INSERT INTO flexitype_entity_summary
        (tenant_id, type_definition_id, entity_id, value_count, last_updated_at)
    VALUES (p_tenant, p_type, p_entity, 0, to_timestamp(0))
    ON CONFLICT (tenant_id, type_definition_id, entity_id)
    DO UPDATE SET value_count = flexitype_entity_summary.value_count;

    -- A NEW statement takes a fresh snapshot, so rows committed by the
    -- writer this transaction just waited on are included.
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
        UPDATE flexitype_entity_summary
           SET value_count = v_count, last_updated_at = v_last
         WHERE tenant_id = p_tenant
           AND type_definition_id = p_type
           AND entity_id = p_entity;
    END IF;
END;
$$ LANGUAGE plpgsql;
