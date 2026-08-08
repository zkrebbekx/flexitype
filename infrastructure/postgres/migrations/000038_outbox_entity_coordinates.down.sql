ALTER TABLE flexitype_event_outbox
    DROP COLUMN IF EXISTS type_definition_id,
    DROP COLUMN IF EXISTS entity_id;
