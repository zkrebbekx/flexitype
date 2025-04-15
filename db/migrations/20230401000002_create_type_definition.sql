-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE flexitype.type_definition (
    id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    parent_type_id VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (parent_type_id) REFERENCES flexitype.type_definition(id)
);

-- Create an index on name for quick lookups
CREATE INDEX idx_type_definition_name ON flexitype.type_definition(name);

-- Create an index on parent_type_id for quick relationship lookups
CREATE INDEX idx_type_definition_parent ON flexitype.type_definition(parent_type_id);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.type_definition CASCADE;