-- flexitype_role declares UNIQUE (tenant_id, name), whose backing index
-- already serves both `WHERE tenant_id = ?` and
-- `WHERE tenant_id = ? AND name = ANY(?)`. Migration 000026 then created a
-- byte-identical btree over the same columns, so every role write maintained
-- two indexes and the planner had two interchangeable candidates.
DROP INDEX IF EXISTS idx_flexitype_role_tenant;

-- Accounts are checked for a role by name before that role can be deleted,
-- which is a containment test over the array column.
CREATE INDEX IF NOT EXISTS idx_flexitype_service_account_roles
    ON flexitype_service_account USING GIN (roles);
