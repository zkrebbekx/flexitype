-- Units of measure. A unit family (mass, length, …) names a base unit and
-- each member unit's factor to it; a quantity attribute pins a family and an
-- optional display unit. Quantity values store their base magnitude in
-- value_float (for comparison) and the original magnitude/unit in value_json.
CREATE TABLE flexitype_unit_family (
    id         TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    base_unit  TEXT NOT NULL,
    units      JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX flexitype_unit_family_tenant ON flexitype_unit_family (tenant_id);

ALTER TABLE flexitype_attribute_definition
    ADD COLUMN unit_family_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN display_unit   TEXT NOT NULL DEFAULT '';
