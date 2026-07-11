-- Saved, shareable entity views: a named FQL query over a root type with a
-- chosen set of display columns and sort. Tenant-scoped.
CREATE TABLE flexitype_saved_view (
    id         CHAR(26) PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    name       TEXT NOT NULL,
    root_type  TEXT NOT NULL,        -- type internal name the query is rooted at
    query      TEXT NOT NULL DEFAULT '',
    columns    JSONB NOT NULL DEFAULT '[]', -- attribute internal names, in order
    sort       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE INDEX idx_flexitype_saved_view_tenant ON flexitype_saved_view (tenant_id);
