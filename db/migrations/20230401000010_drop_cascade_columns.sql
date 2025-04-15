-- +goose Up
-- SQL in this section is executed when the migration is applied
-- Remove the old cascade columns from attribute_definition
ALTER TABLE flexitype.attribute_definition
  DROP COLUMN cascade_enabled,
  DROP COLUMN cascade_behavior,
  DROP COLUMN cascade_logic;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
-- Add back the cascade columns to attribute_definition
ALTER TABLE flexitype.attribute_definition
  ADD COLUMN cascade_enabled BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN cascade_behavior VARCHAR(50) NOT NULL DEFAULT 'inherit',
  ADD COLUMN cascade_logic TEXT;