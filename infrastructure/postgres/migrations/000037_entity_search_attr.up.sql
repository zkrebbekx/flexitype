-- Per-attribute search vectors, so matches() can serve a field-restricted
-- principal.
--
-- flexitype_entity_search holds ONE tsvector per entity: every textual value
-- flattened together with no attribute identity. A search over it cannot be
-- filtered the way a named condition is, so a principal denied an attribute
-- recovered its content word by word — contains(notes, ...) was refused as
-- unknown while matches("recall") returned the entity. The only safe answer
-- at the time was to refuse matches() for any restricted principal, which
-- took the feature away from exactly the deployments that most need field
-- permissions.
--
-- This table carries one vector per (entity, attribute), so the query can
-- restrict the search to the names the caller may read. The entity-level
-- vector stays: a principal that reads everything searches one row instead
-- of one per attribute, which is the common case and the cheaper plan.
--
-- The entity id is indexed under the empty attribute name. It is not an
-- attribute and no policy hides it, so it stays searchable for everyone —
-- matching what the flattened document did.
CREATE TABLE flexitype_entity_search_attr (
    tenant_id      TEXT NOT NULL,
    entity_id      TEXT NOT NULL,
    attribute_name TEXT NOT NULL,
    text_vector    tsvector NOT NULL DEFAULT ''::tsvector,
    PRIMARY KEY (tenant_id, entity_id, attribute_name)
);

-- The search predicate is `tenant_id = ? AND attribute_name = ANY(?) AND
-- text_vector @@ ...` correlated to one entity, so the GIN index serves the
-- match and the primary key serves the correlation.
CREATE INDEX idx_flexitype_entity_search_attr_vector
    ON flexitype_entity_search_attr USING GIN (text_vector);

-- Existing rows are NOT backfilled here. flexitype_entity_search.document
-- already holds the per-attribute values, so the backfill derives this table
-- from it in bounded batches after this migration commits — see
-- "000037_entity_search_attr" in migrate_backfill.go. Until it completes, a
-- restricted principal's matches() simply finds nothing for an entity not yet
-- carried over, which is the safe direction; every write after this point
-- maintains its own rows.
