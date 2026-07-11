DROP INDEX IF EXISTS idx_flexitype_type_definition_extends;
ALTER TABLE flexitype_type_definition DROP COLUMN IF EXISTS extends_id;
