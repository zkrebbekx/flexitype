-- Per-side cardinality bounds on relationship definitions (NULL = unbounded).
-- min/max_children bound children per parent; min/max_parents bound parents
-- per child. Symmetric definitions use the children bounds as a per-entity cap.
ALTER TABLE flexitype_relationship_definition
    ADD COLUMN min_children INTEGER,
    ADD COLUMN max_children INTEGER,
    ADD COLUMN min_parents  INTEGER,
    ADD COLUMN max_parents  INTEGER;
