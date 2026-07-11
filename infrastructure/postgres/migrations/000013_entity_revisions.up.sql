-- Entity revisions: immutable point-in-time snapshots of an entity's live
-- attribute values. History is append-only; a restore writes new forward
-- state rather than mutating a revision. `values` is the snapshot payload
-- (an array of {attribute_definition_id, internal_name, data_type, value}).
CREATE TABLE flexitype_entity_revision (
    id                 TEXT PRIMARY KEY,
    tenant_id          TEXT NOT NULL,
    type_definition_id TEXT NOT NULL,
    entity_id          TEXT NOT NULL,
    seq                INTEGER NOT NULL,
    label              TEXT NOT NULL DEFAULT '',
    created_at         TIMESTAMPTZ NOT NULL,
    values             JSONB NOT NULL
);

CREATE INDEX flexitype_entity_revision_entity
    ON flexitype_entity_revision (tenant_id, type_definition_id, entity_id, seq DESC);
