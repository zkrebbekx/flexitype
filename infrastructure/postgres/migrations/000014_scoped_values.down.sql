DROP INDEX IF EXISTS idx_flexitype_attribute_value_scope;
ALTER TABLE flexitype_attribute_value
    DROP COLUMN IF EXISTS locale,
    DROP COLUMN IF EXISTS channel;
ALTER TABLE flexitype_attribute_definition
    DROP COLUMN IF EXISTS localizable,
    DROP COLUMN IF EXISTS scopable;
