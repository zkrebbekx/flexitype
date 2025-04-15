-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE flexitype.attribute_definition (
    id VARCHAR(255) NOT NULL,
    type_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    data_type VARCHAR(50) NOT NULL,  -- string, int, float, boolean, date, object, array
    required BOOLEAN NOT NULL DEFAULT FALSE,
    default_value TEXT,              -- Stored as string representation, to be parsed by the application
    multi_valued BOOLEAN NOT NULL DEFAULT FALSE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    cascade_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    cascade_behavior VARCHAR(50) NOT NULL DEFAULT 'inherit',  -- inherit, override, disabled
    cascade_logic TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (type_id) REFERENCES flexitype.type_definition(id) ON DELETE CASCADE,
    CONSTRAINT uq_attr_type_name UNIQUE (type_id, name)
);

-- Create an index on type_id for quick lookups
CREATE INDEX idx_attribute_definition_type ON flexitype.attribute_definition(type_id);

-- Create an index on name for quick lookups
CREATE INDEX idx_attribute_definition_name ON flexitype.attribute_definition(name);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.attribute_definition CASCADE;