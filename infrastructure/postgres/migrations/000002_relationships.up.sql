-- Relationships between types: user-defined relationship definitions with
-- parent/child endpoints, version policies, inheritance, and their own
-- attributes (held by a hidden companion "attribute set" type definition so
-- the whole attribute/constraint/dependency machinery applies to links).

-- Type definitions gain a kind: ordinary entity types vs the hidden
-- attribute-set types owned by relationship definitions.
ALTER TABLE flexitype_type_definition
    ADD COLUMN kind TEXT NOT NULL DEFAULT 'entity';

CREATE TABLE flexitype_relationship_definition (
    id                    CHAR(26) PRIMARY KEY,
    tenant_id             TEXT NOT NULL DEFAULT 'default',
    internal_name         TEXT NOT NULL,
    display_name          TEXT NOT NULL,
    description           TEXT NOT NULL DEFAULT '',
    parent_type_id        CHAR(26) NOT NULL REFERENCES flexitype_type_definition (id),
    child_type_id         CHAR(26) NOT NULL REFERENCES flexitype_type_definition (id),
    attribute_set_id      CHAR(26) NOT NULL REFERENCES flexitype_type_definition (id),
    extends_id            CHAR(26) REFERENCES flexitype_relationship_definition (id),
    parent_version_policy TEXT NOT NULL DEFAULT 'latest',
    child_version_policy  TEXT NOT NULL DEFAULT 'latest',
    version               INTEGER NOT NULL DEFAULT 1,
    created_at            TIMESTAMPTZ NOT NULL,
    updated_at            TIMESTAMPTZ NOT NULL,
    archived_at           TIMESTAMPTZ
);

CREATE UNIQUE INDEX uq_flexitype_relationship_definition_name
    ON flexitype_relationship_definition (tenant_id, internal_name)
    WHERE archived_at IS NULL;

CREATE INDEX idx_flexitype_relationship_definition_parent
    ON flexitype_relationship_definition (parent_type_id);
CREATE INDEX idx_flexitype_relationship_definition_child
    ON flexitype_relationship_definition (child_type_id);

CREATE TABLE flexitype_relationship (
    id                         CHAR(26) PRIMARY KEY,
    tenant_id                  TEXT NOT NULL DEFAULT 'default',
    relationship_definition_id CHAR(26) NOT NULL REFERENCES flexitype_relationship_definition (id),
    parent_entity_id           TEXT NOT NULL,
    child_entity_id            TEXT NOT NULL,
    -- NULL tracks the latest version of the endpoint's type definition; a
    -- value pins the link to that specific version.
    parent_type_version        INTEGER,
    child_type_version         INTEGER,
    created_at                 TIMESTAMPTZ NOT NULL,
    updated_at                 TIMESTAMPTZ NOT NULL,
    archived_at                TIMESTAMPTZ
);

-- One live link per (definition, parent, child).
CREATE UNIQUE INDEX uq_flexitype_relationship_pair
    ON flexitype_relationship (relationship_definition_id, parent_entity_id, child_entity_id)
    WHERE archived_at IS NULL;

CREATE INDEX idx_flexitype_relationship_parent
    ON flexitype_relationship (relationship_definition_id, parent_entity_id)
    WHERE archived_at IS NULL;
CREATE INDEX idx_flexitype_relationship_child
    ON flexitype_relationship (relationship_definition_id, child_entity_id)
    WHERE archived_at IS NULL;
CREATE INDEX idx_flexitype_relationship_tenant_parent
    ON flexitype_relationship (tenant_id, parent_entity_id)
    WHERE archived_at IS NULL;
CREATE INDEX idx_flexitype_relationship_tenant_child
    ON flexitype_relationship (tenant_id, child_entity_id)
    WHERE archived_at IS NULL;
