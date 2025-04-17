-- +goose Up
-- SQL in this section is executed when the migration is applied
-- This migration adds the changes needed to make the attribute name the primary key within a type

-- Update comments on the name field to indicate it's the primary key for API usage
COMMENT ON COLUMN flexitype.attribute_definition.name IS 'Name is the primary key for accessing attributes via API (while ID is for internal use)';

-- Remove the comment
COMMENT ON COLUMN flexitype.attribute_definition.name IS NULL;