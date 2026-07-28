-- +flexitype:no-transaction
-- Revisions are read, sequenced and purged by (tenant, entity), not by type.
--
-- An entity's anchor can NARROW: writing an inherited attribute anchors it to
-- the declaring parent, and naming the subtype afterwards moves it down. The
-- value rows move with it. Revision rows, keyed on the type, did not — so a
-- pre-narrowing revision became invisible under the new anchor, and an
-- erasure purging under the new anchor reported success while leaving a full
-- snapshot of the entity's values behind.
--
-- The old index leads with (tenant_id, type_definition_id), so it cannot
-- serve a lookup that does not name the type.
CREATE INDEX CONCURRENTLY IF NOT EXISTS flexitype_entity_revision_by_entity
    ON flexitype_entity_revision (tenant_id, entity_id, seq DESC);

DROP INDEX CONCURRENTLY IF EXISTS flexitype_entity_revision_entity;
