-- Symmetric relationships: kind distinguishes directed parent/child edges
-- from unordered peer links (canonical pair storage, never pinned).
-- parent_label/child_label are display-only role names on directed
-- definitions ("assembly"/"component" instead of parent/child).
ALTER TABLE flexitype_relationship_definition
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'directed',
    ADD COLUMN parent_label TEXT NOT NULL DEFAULT '',
    ADD COLUMN child_label TEXT NOT NULL DEFAULT '';
