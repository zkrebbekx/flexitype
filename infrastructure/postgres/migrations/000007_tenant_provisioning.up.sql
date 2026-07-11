-- Runtime tenant and service-account management (hosted-tier foundation).
-- The file-based service-account loader remains a bootstrap path; these
-- tables let an admin-scoped API provision tenants and rotate credentials
-- without a redeploy.
CREATE TABLE flexitype_tenant (
    id         CHAR(26) PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE, -- the tenant_id used across the API
    active     BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE flexitype_service_account (
    id          CHAR(26) PRIMARY KEY, -- embedded in the bearer token
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    secret_hash TEXT NOT NULL,         -- hex(SHA-256(secret)); never the secret
    scopes      TEXT[] NOT NULL DEFAULT '{}',
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, name)
);

CREATE INDEX idx_flexitype_service_account_tenant
    ON flexitype_service_account (tenant_id);
