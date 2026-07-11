-- Computed (read-only, derived) attributes. `computed` holds the derivation
-- spec: a formula over other attributes, or a relationship rollup. NULL for
-- regular attributes. Values are materialized by an event subscriber, so a
-- computed attribute is queryable like any stored value.
ALTER TABLE flexitype_attribute_definition
    ADD COLUMN computed JSONB;
