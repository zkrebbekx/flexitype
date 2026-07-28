ALTER TABLE flexitype_service_account DROP COLUMN IF EXISTS field_permissions;
ALTER TABLE flexitype_service_account DROP COLUMN IF EXISTS roles;
DROP TABLE IF EXISTS flexitype_role;
