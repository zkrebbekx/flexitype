-- +flexitype:no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS flexitype_entity_revision_entity
    ON flexitype_entity_revision (tenant_id, type_definition_id, entity_id, seq DESC);

DROP INDEX CONCURRENTLY IF EXISTS flexitype_entity_revision_by_entity;
