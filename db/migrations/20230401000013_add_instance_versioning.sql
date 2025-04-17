-- +goose Up
-- SQL in this section is executed when the migration is applied
-- Add version field to instance table
ALTER TABLE flexitype.instance ADD COLUMN version INTEGER NOT NULL DEFAULT 1;

-- Create a composite primary key on (id, version) to enable versioning
ALTER TABLE flexitype.instance DROP CONSTRAINT instance_pkey;
ALTER TABLE flexitype.instance ADD PRIMARY KEY (id, version);

-- Create an index for the new version field
CREATE INDEX idx_instance_version ON flexitype.instance(version);

-- Create a function to get the latest version of an instance
CREATE OR REPLACE FUNCTION flexitype.get_latest_instance_version(p_id VARCHAR(255))
RETURNS INTEGER AS $$
DECLARE
  latest_version INTEGER;
BEGIN
  SELECT COALESCE(MAX(version), 0)
  INTO latest_version
  FROM flexitype.instance
  WHERE id = p_id;
  
  RETURN latest_version;
END;
$$ LANGUAGE plpgsql;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
-- Restore the original primary key
ALTER TABLE flexitype.instance DROP CONSTRAINT instance_pkey;
ALTER TABLE flexitype.instance ADD PRIMARY KEY (id);

-- Drop the version field
ALTER TABLE flexitype.instance DROP COLUMN version;

-- Drop the function
DROP FUNCTION IF EXISTS flexitype.get_latest_instance_version;