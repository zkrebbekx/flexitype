-- Presentation metadata so consumers can render structured, ordered forms
-- instead of a flat attribute list: a named group/section, an explicit
-- sort order within the type, and per-attribute help text.
ALTER TABLE flexitype_attribute_definition
    ADD COLUMN attr_group TEXT NOT NULL DEFAULT '',
    ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN help_text  TEXT NOT NULL DEFAULT '';
