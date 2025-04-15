-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE flexitype.instance (
    id VARCHAR(255) NOT NULL,
    type_id VARCHAR(255) NOT NULL,
    type_version INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (type_id) REFERENCES flexitype.type_definition(id)
);

-- Create an index on type_id for quick lookups
CREATE INDEX idx_instance_type ON flexitype.instance(type_id);

-- Create an index on type_version for querying by version
CREATE INDEX idx_instance_type_version ON flexitype.instance(type_id, type_version);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.instance CASCADE;