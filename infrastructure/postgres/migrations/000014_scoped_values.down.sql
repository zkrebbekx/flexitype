-- The scope index belongs to 000042, which drops it.
ALTER TABLE flexitype_attribute_value
    DROP COLUMN IF EXISTS locale,
    DROP COLUMN IF EXISTS channel;
ALTER TABLE flexitype_attribute_definition
    DROP COLUMN IF EXISTS localizable,
    DROP COLUMN IF EXISTS scopable;
