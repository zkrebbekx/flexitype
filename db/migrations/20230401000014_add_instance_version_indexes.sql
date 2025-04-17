-- +goose Up
-- SQL in this section is executed when the migration is applied
-- Add indexes for efficient instance version queries

-- Create an index for querying the latest version of an instance
CREATE INDEX idx_instance_id_version ON flexitype.instance(id, version DESC);

-- Create a function to get instances by ID and version
CREATE OR REPLACE FUNCTION flexitype.get_instance_by_id_and_version(
    p_id VARCHAR(255),
    p_version INTEGER DEFAULT NULL
)
RETURNS TABLE (
    id VARCHAR(255),
    version INTEGER,
    type_id VARCHAR(255),
    type_version INTEGER,
    attributes JSONB,
    created_at TIMESTAMP WITH TIME ZONE,
    updated_at TIMESTAMP WITH TIME ZONE,
    archived_at TIMESTAMP WITH TIME ZONE
) AS $$
BEGIN
    IF p_version IS NULL THEN
        -- Return the latest version if version not specified
        RETURN QUERY
        SELECT 
            i.id, 
            i.version, 
            i.type_id, 
            i.type_version, 
            i.attributes, 
            i.created_at, 
            i.updated_at, 
            i.archived_at
        FROM flexitype.instance i
        WHERE i.id = p_id
        ORDER BY i.version DESC
        LIMIT 1;
    ELSE
        -- Return the specific version
        RETURN QUERY
        SELECT 
            i.id, 
            i.version, 
            i.type_id, 
            i.type_version, 
            i.attributes, 
            i.created_at, 
            i.updated_at, 
            i.archived_at
        FROM flexitype.instance i
        WHERE i.id = p_id AND i.version = p_version;
    END IF;
END;
$$ LANGUAGE plpgsql;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP INDEX IF EXISTS flexitype.idx_instance_id_version;
DROP FUNCTION IF EXISTS flexitype.get_instance_by_id_and_version;