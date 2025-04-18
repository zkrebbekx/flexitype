-- +goose Up
-- SQL in this section is executed when the migration is applied

-- Type definition version snapshots table
CREATE TABLE flexitype.type_definition_version (
    id VARCHAR(255) NOT NULL,
    version INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    parent_type_id VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    PRIMARY KEY (id, version)
);

-- Versioned attribute definition
CREATE TABLE flexitype.attribute_definition_version (
    id VARCHAR(255) NOT NULL,
    type_id VARCHAR(255) NOT NULL,
    type_version INT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    data_type VARCHAR(50) NOT NULL,
    required BOOLEAN NOT NULL DEFAULT FALSE,
    default_value TEXT,
    multi_valued BOOLEAN NOT NULL DEFAULT FALSE,
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id, type_id, type_version),
    FOREIGN KEY (type_id, type_version) REFERENCES flexitype.type_definition_version(id, version)
);

-- Versioned validation rules
CREATE TABLE flexitype.validation_rule_version (
    id SERIAL NOT NULL,
    type_id VARCHAR(255) NOT NULL,
    type_version INT NOT NULL,
    attribute_id VARCHAR(255) NOT NULL,
    rule_type VARCHAR(50) NOT NULL,
    parameters TEXT,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (type_id, type_version, attribute_id)
        REFERENCES flexitype.attribute_definition_version(type_id, type_version, id),
    CONSTRAINT uq_type_attribute_validation_rule_id UNIQUE (type_id, type_version, attribute_id, rule_type, sort_order)
);

-- Versioned attribute cascades
CREATE TABLE flexitype.attribute_cascade_version (
    id SERIAL NOT NULL,
    type_id VARCHAR(255) NOT NULL,
    type_version INT NOT NULL,
    attribute_id VARCHAR(255) NOT NULL,
    cascade_id VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    behavior VARCHAR(50) NOT NULL DEFAULT 'inherit',
    logic TEXT,
    weight INT NOT NULL DEFAULT 100,
    validation_action VARCHAR(50) NULL,
    validation_target_field VARCHAR(255) NULL,
    validation_values JSONB NULL,
    validation_string_value TEXT NULL,
    validation_numeric_value DOUBLE PRECISION NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (type_id, type_version, attribute_id)
        REFERENCES flexitype.attribute_definition_version(type_id, type_version, id),
    CONSTRAINT uq_type_version_attribute_cascade_id UNIQUE (type_id, type_version, attribute_id, cascade_id)
);

-- Update attribute_value table to include instance version
ALTER TABLE flexitype.attribute_value
ADD COLUMN instance_version INT NOT NULL DEFAULT 1;

-- Drop existing unique constraint
ALTER TABLE flexitype.attribute_value
DROP CONSTRAINT uq_attr_value_instance_name_idx;

-- Add new unique constraint that includes instance_version
ALTER TABLE flexitype.attribute_value
ADD CONSTRAINT uq_attr_value_instance_version_id_idx
UNIQUE (instance_id, instance_version, attribute_id, list_index);

-- Create index for querying by instance ID and version
CREATE INDEX idx_attribute_value_instance_version 
ON flexitype.attribute_value(instance_id, instance_version);

-- Create index to efficiently retrieve all values for an instance version
CREATE INDEX idx_attribute_value_by_instance_version 
ON flexitype.attribute_value(instance_id, instance_version, attribute_id);

-- Create index for type definition versions
CREATE INDEX idx_type_definition_version 
ON flexitype.type_definition_version(id, version DESC);

-- Create indexes for attribute definition versions
CREATE INDEX idx_attribute_definition_version_id
ON flexitype.attribute_definition_version(id);

CREATE INDEX idx_attribute_definition_version_query 
ON flexitype.attribute_definition_version(type_id, type_version);

-- Create indexes for validation rule versions
CREATE INDEX idx_validation_rule_version_query 
ON flexitype.validation_rule_version(type_id, type_version, attribute_id);

-- Create indexes for attribute cascade versions
CREATE INDEX idx_attribute_cascade_version_query 
ON flexitype.attribute_cascade_version(type_id, type_version, attribute_id);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back

-- Drop indexes
DROP INDEX IF EXISTS flexitype.idx_attribute_cascade_version_query;
DROP INDEX IF EXISTS flexitype.idx_validation_rule_version_query;
DROP INDEX IF EXISTS flexitype.idx_attribute_definition_version_query;
DROP INDEX IF EXISTS flexitype.idx_attribute_definition_version_name;
DROP INDEX IF EXISTS flexitype.idx_type_definition_version;
DROP INDEX IF EXISTS flexitype.idx_attribute_value_by_instance_version;
DROP INDEX IF EXISTS flexitype.idx_attribute_value_instance_version;

-- Reset attribute_value table
ALTER TABLE flexitype.attribute_value
DROP CONSTRAINT IF EXISTS uq_attr_value_instance_version_name_idx;

-- Recreate original constraint
ALTER TABLE flexitype.attribute_value
ADD CONSTRAINT uq_attr_value_instance_name_idx 
UNIQUE (instance_id, attribute_name, list_index);

-- Drop added column
ALTER TABLE flexitype.attribute_value
DROP COLUMN IF EXISTS instance_version;

-- Drop versioning tables
DROP TABLE IF EXISTS flexitype.attribute_cascade_version CASCADE;
DROP TABLE IF EXISTS flexitype.validation_rule_version CASCADE;
DROP TABLE IF EXISTS flexitype.attribute_definition_version CASCADE;
DROP TABLE IF EXISTS flexitype.type_definition_version CASCADE;