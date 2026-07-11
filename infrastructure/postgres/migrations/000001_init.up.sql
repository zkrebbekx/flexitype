-- flexitype initial schema. Tables are prefixed so an embedded deployment
-- can share a database with the host application.

CREATE TABLE flexitype_type_definition (
    id            CHAR(26) PRIMARY KEY,
    tenant_id     TEXT NOT NULL DEFAULT 'default',
    internal_name TEXT NOT NULL,
    display_name  TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    version       INTEGER NOT NULL DEFAULT 1,
    created_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL,
    archived_at   TIMESTAMPTZ
);

-- Machine names are unique per tenant among live definitions.
CREATE UNIQUE INDEX uq_flexitype_type_definition_name
    ON flexitype_type_definition (tenant_id, internal_name)
    WHERE archived_at IS NULL;

CREATE TABLE flexitype_attribute_definition (
    id                 CHAR(26) PRIMARY KEY,
    tenant_id          TEXT NOT NULL DEFAULT 'default',
    type_definition_id CHAR(26) NOT NULL REFERENCES flexitype_type_definition (id),
    internal_name      TEXT NOT NULL,
    display_name       TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    data_type          TEXT NOT NULL,
    required           BOOLEAN NOT NULL DEFAULT FALSE,
    multi_valued       BOOLEAN NOT NULL DEFAULT FALSE,
    is_unique          BOOLEAN NOT NULL DEFAULT FALSE,
    constraints        JSONB NOT NULL DEFAULT '[]'::jsonb,
    default_value      JSONB,
    version            INTEGER NOT NULL DEFAULT 1,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    archived_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX uq_flexitype_attribute_definition_name
    ON flexitype_attribute_definition (type_definition_id, internal_name)
    WHERE archived_at IS NULL;

CREATE INDEX idx_flexitype_attribute_definition_type
    ON flexitype_attribute_definition (tenant_id, type_definition_id);

-- One polymorphic value table with a typed column per storage class keeps
-- values indexable without a table per data type.
CREATE TABLE flexitype_attribute_value (
    id                      CHAR(26) PRIMARY KEY,
    tenant_id               TEXT NOT NULL DEFAULT 'default',
    type_definition_id      CHAR(26) NOT NULL REFERENCES flexitype_type_definition (id),
    attribute_definition_id CHAR(26) NOT NULL REFERENCES flexitype_attribute_definition (id),
    entity_id               TEXT NOT NULL,
    data_type               TEXT NOT NULL,
    value_bool              BOOLEAN,
    value_int               BIGINT,
    value_float             DOUBLE PRECISION,
    value_text              TEXT,
    value_time              TIMESTAMPTZ,
    value_json              JSONB,
    definition_version      INTEGER NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL,
    updated_at              TIMESTAMPTZ NOT NULL,
    archived_at             TIMESTAMPTZ
);

-- Entity hydration: every live value of one entity.
CREATE INDEX idx_flexitype_attribute_value_entity
    ON flexitype_attribute_value (tenant_id, type_definition_id, entity_id)
    WHERE archived_at IS NULL;

-- Per-definition scans and paginated listings.
CREATE INDEX idx_flexitype_attribute_value_definition
    ON flexitype_attribute_value (attribute_definition_id, id)
    WHERE archived_at IS NULL;

-- Write-path lookups: existing values for (definition, entity).
CREATE INDEX idx_flexitype_attribute_value_definition_entity
    ON flexitype_attribute_value (attribute_definition_id, entity_id)
    WHERE archived_at IS NULL;

-- Uniqueness probes per storage class.
CREATE INDEX idx_flexitype_attribute_value_uniq_text
    ON flexitype_attribute_value (attribute_definition_id, value_text)
    WHERE archived_at IS NULL AND value_text IS NOT NULL;
CREATE INDEX idx_flexitype_attribute_value_uniq_int
    ON flexitype_attribute_value (attribute_definition_id, value_int)
    WHERE archived_at IS NULL AND value_int IS NOT NULL;
CREATE INDEX idx_flexitype_attribute_value_uniq_time
    ON flexitype_attribute_value (attribute_definition_id, value_time)
    WHERE archived_at IS NULL AND value_time IS NOT NULL;

CREATE TABLE flexitype_attribute_value_dependency (
    id                  CHAR(26) PRIMARY KEY,
    tenant_id           TEXT NOT NULL DEFAULT 'default',
    source_attribute_id CHAR(26) NOT NULL REFERENCES flexitype_attribute_definition (id),
    target_attribute_id CHAR(26) NOT NULL REFERENCES flexitype_attribute_definition (id),
    conditions          JSONB NOT NULL DEFAULT '[]'::jsonb,
    effect              JSONB NOT NULL DEFAULT '{}'::jsonb,
    description         TEXT NOT NULL DEFAULT '',
    version             INTEGER NOT NULL DEFAULT 1,
    created_at          TIMESTAMPTZ NOT NULL,
    updated_at          TIMESTAMPTZ NOT NULL,
    archived_at         TIMESTAMPTZ,
    CONSTRAINT flexitype_dependency_not_self CHECK (source_attribute_id <> target_attribute_id)
);

CREATE INDEX idx_flexitype_dependency_target
    ON flexitype_attribute_value_dependency (target_attribute_id)
    WHERE archived_at IS NULL;
CREATE INDEX idx_flexitype_dependency_source
    ON flexitype_attribute_value_dependency (source_attribute_id)
    WHERE archived_at IS NULL;

-- Audit trail: one row per change, before/after JSON descriptors, written
-- in the same transaction as the change (pre-commit handler).
CREATE TABLE flexitype_activity_log (
    id           CHAR(26) PRIMARY KEY,
    tenant_id    TEXT NOT NULL DEFAULT 'default',
    actor        TEXT NOT NULL,
    entity       TEXT NOT NULL,
    entity_id    TEXT NOT NULL,
    action       TEXT NOT NULL,
    before_state JSONB,
    after_state  JSONB,
    occurred_at  TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_flexitype_activity_log_entity
    ON flexitype_activity_log (tenant_id, entity, entity_id, occurred_at DESC);
CREATE INDEX idx_flexitype_activity_log_occurred
    ON flexitype_activity_log (tenant_id, occurred_at DESC);
