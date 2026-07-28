-- Field-level permissions were stored per service account with no indirection,
-- so a deployment fronting many human users had to create one account per
-- person and duplicate the whole per-attribute permission set onto each. A
-- permission change then meant rewriting every affected account, and there was
-- no way to answer "what can a reviewer see" without reading every account.
--
-- A role names a permission set once. Accounts reference roles; an account's
-- own permissions still win where both speak, so nothing existing changes.
CREATE TABLE IF NOT EXISTS flexitype_role (
    id                CHAR(26) PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    name              TEXT NOT NULL,
    description       TEXT NOT NULL DEFAULT '',
    -- Scopes the role grants, unioned with the account's own.
    scopes            TEXT[] NOT NULL DEFAULT '{}',
    -- attribute internal_name -> none | read | write
    field_permissions JSONB NOT NULL DEFAULT '{}',
    created_at        TIMESTAMPTZ NOT NULL,
    updated_at        TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE INDEX IF NOT EXISTS idx_flexitype_role_tenant
    ON flexitype_role (tenant_id, name);

-- An account's roles, by name within its tenant. A name rather than an id so
-- a role can be renamed only by rewriting the accounts that hold it, which is
-- the visible, auditable direction.
ALTER TABLE flexitype_service_account
    ADD COLUMN IF NOT EXISTS roles TEXT[] NOT NULL DEFAULT '{}';

-- Per-account field permissions, previously expressible only in the JSON
-- accounts file and absent from the provisioning table entirely.
ALTER TABLE flexitype_service_account
    ADD COLUMN IF NOT EXISTS field_permissions JSONB NOT NULL DEFAULT '{}';
