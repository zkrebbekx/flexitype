-- +goose Up
-- SQL in this section is executed when the migration is applied
ALTER TABLE flexitype.type_definition
ADD COLUMN archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL;

ALTER TABLE flexitype.instance
ADD COLUMN archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL;

-- Create indexes for better performance when filtering by archived status
CREATE INDEX idx_type_definition_archived_at ON flexitype.type_definition(archived_at) WHERE archived_at IS NOT NULL;
CREATE INDEX idx_instance_archived_at ON flexitype.instance(archived_at) WHERE archived_at IS NOT NULL;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
ALTER TABLE flexitype.instance DROP COLUMN IF EXISTS archived_at;
ALTER TABLE flexitype.type_definition DROP COLUMN IF EXISTS archived_at;

DROP INDEX IF EXISTS idx_type_definition_archived_at;
DROP INDEX IF EXISTS idx_instance_archived_at;