DROP INDEX IF EXISTS idx_flexitype_service_account_roles;
CREATE INDEX IF NOT EXISTS idx_flexitype_role_tenant
    ON flexitype_role (tenant_id, name);
