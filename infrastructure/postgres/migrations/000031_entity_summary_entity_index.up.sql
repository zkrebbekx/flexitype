-- FQL traversals guard each counterpart against the entity-summary
-- projection. A value-less entity is invisible at the root, so a dangling
-- relationship must not match it inside child()/parent()/linked()
-- (issue #475). The guard probes by (tenant_id, entity_id) and does not
-- know the counterpart's type. The primary key
-- (tenant_id, type_definition_id, entity_id) serves only the tenant prefix
-- for that probe, so each probe would scan every summary row of the tenant.
-- This index makes each probe one B-tree lookup.
-- +flexitype:no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_entity_summary_entity
    ON flexitype_entity_summary (tenant_id, entity_id);
