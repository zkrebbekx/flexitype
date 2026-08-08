-- Restore the 000019 body: count first, then upsert.
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
