-- +goose Up
-- SQL in this section is executed when the migration is applied

-- Add validation configuration fields to attribute_cascade table
ALTER TABLE flexitype.attribute_cascade
ADD COLUMN validation_action VARCHAR(50) NULL,
ADD COLUMN validation_target_field VARCHAR(255) NULL,
ADD COLUMN validation_values JSONB NULL,
ADD COLUMN validation_string_value TEXT NULL,
ADD COLUMN validation_numeric_value DOUBLE PRECISION NULL;

-- Create index on validation target field for quick lookups
CREATE INDEX idx_attribute_cascade_target ON flexitype.attribute_cascade(validation_target_field);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
ALTER TABLE flexitype.attribute_cascade
DROP COLUMN validation_action,
DROP COLUMN validation_target_field,
DROP COLUMN validation_values,
DROP COLUMN validation_string_value,
DROP COLUMN validation_numeric_value;

DROP INDEX IF EXISTS idx_attribute_cascade_target;