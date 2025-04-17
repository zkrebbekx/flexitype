-- +goose Up
-- SQL in this section is executed when the migration is applied

-- Create schema
CREATE SCHEMA IF NOT EXISTS flexitype;

-- Create type_definition table
CREATE TABLE flexitype.type_definition (
    id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    parent_type_id VARCHAR(255),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    PRIMARY KEY (id),
    FOREIGN KEY (parent_type_id) REFERENCES flexitype.type_definition(id)
);

-- Create indexes for type_definition
CREATE INDEX idx_type_definition_name ON flexitype.type_definition(name);
CREATE INDEX idx_type_definition_parent ON flexitype.type_definition(parent_type_id);
CREATE INDEX idx_type_definition_archived_at ON flexitype.type_definition(archived_at) WHERE archived_at IS NOT NULL;

-- Create attribute_definition table
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
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (type_id) REFERENCES flexitype.type_definition(id) ON DELETE CASCADE,
    CONSTRAINT uq_attr_type_name UNIQUE (type_id, name)
);

-- Create indexes for attribute_definition
CREATE INDEX idx_attribute_definition_type ON flexitype.attribute_definition(type_id);
CREATE INDEX idx_attribute_definition_name ON flexitype.attribute_definition(name);

-- Add comment for attribute_definition name
COMMENT ON COLUMN flexitype.attribute_definition.name IS 'Name is the primary key for accessing attributes via API (while ID is for internal use)';

-- Create validation_rule table
CREATE TABLE flexitype.validation_rule (
    id SERIAL NOT NULL,
    attribute_id VARCHAR(255) NOT NULL,
    rule_type VARCHAR(50) NOT NULL,  -- required, min, max, pattern, etc.
    parameters TEXT,                 -- Stored as serialized key-value pairs, to be parsed by the application
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (attribute_id) REFERENCES flexitype.attribute_definition(id) ON DELETE CASCADE
);

-- Create index for validation_rule
CREATE INDEX idx_validation_rule_attribute ON flexitype.validation_rule(attribute_id);

-- Create instance table
CREATE TABLE flexitype.instance (
    id VARCHAR(255) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    type_id VARCHAR(255) NOT NULL,
    type_version INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    PRIMARY KEY (id, version),
    FOREIGN KEY (type_id) REFERENCES flexitype.type_definition(id)
);

-- Create indexes for instance
CREATE INDEX idx_instance_type ON flexitype.instance(type_id);
CREATE INDEX idx_instance_type_version ON flexitype.instance(type_id, type_version);
CREATE INDEX idx_instance_archived_at ON flexitype.instance(archived_at) WHERE archived_at IS NOT NULL;
CREATE INDEX idx_instance_version ON flexitype.instance(version);
CREATE INDEX idx_instance_id_version ON flexitype.instance(id, version DESC);

-- Create attribute_value table
CREATE TABLE flexitype.attribute_value (
    id serial NOT NULL,
    instance_id varchar(255) NOT NULL,
    attribute_name varchar(255) NOT NULL, -- Using name instead of ID for easier querying and to handle inherited attributes
    value_type varchar(50) NOT NULL, -- string, int, float, boolean, date, etc.
    string_value text,
    int_value bigint,
    float_value double precision,
    boolean_value boolean,
    date_value timestamp with time zone,
    is_default boolean NOT NULL DEFAULT FALSE, -- Indicates if this is a default value or explicitly set
    list_index integer DEFAULT 0, -- For multi-valued attributes, index in the list (defaulted to 0)
    created_at timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    CONSTRAINT uq_attr_value_instance_name_idx UNIQUE (instance_id, attribute_name, list_index)
);

-- Create indexes for attribute_value
CREATE INDEX idx_attribute_value_instance ON flexitype.attribute_value(instance_id);
CREATE INDEX idx_attribute_value_name ON flexitype.attribute_value(attribute_name);
CREATE INDEX idx_attribute_value_query ON flexitype.attribute_value(attribute_name, value_type);

-- Create object_value table
CREATE TABLE flexitype.object_value (
    id SERIAL NOT NULL,
    attribute_value_id INTEGER NOT NULL,
    property_name VARCHAR(255) NOT NULL,
    value_type VARCHAR(50) NOT NULL,        -- string, int, float, boolean, date, nested_object
    string_value TEXT,
    int_value BIGINT,
    float_value DOUBLE PRECISION,
    boolean_value BOOLEAN,
    date_value TIMESTAMP WITH TIME ZONE,
    nested_object_id INTEGER,               -- Self-reference for nested objects
    list_index INTEGER DEFAULT 0,           -- For array properties, index in the list (defaulted to 0)
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (attribute_value_id) REFERENCES flexitype.attribute_value(id) ON DELETE CASCADE,
    FOREIGN KEY (nested_object_id) REFERENCES flexitype.object_value(id) ON DELETE CASCADE,
    CONSTRAINT uq_object_property_idx UNIQUE (attribute_value_id, property_name, list_index)
);

-- Create indexes for object_value
CREATE INDEX idx_object_value_attr_value ON flexitype.object_value(attribute_value_id);
CREATE INDEX idx_object_value_nested ON flexitype.object_value(nested_object_id);

-- Create attribute_cascade table
CREATE TABLE flexitype.attribute_cascade (
    id SERIAL NOT NULL,
    attribute_id VARCHAR(255) NOT NULL,
    cascade_id VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    behavior VARCHAR(50) NOT NULL DEFAULT 'inherit',  -- inherit, override, disabled
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
    FOREIGN KEY (attribute_id) REFERENCES flexitype.attribute_definition(id) ON DELETE CASCADE,
    CONSTRAINT uq_attribute_cascade_id UNIQUE (attribute_id, cascade_id)
);

-- Create indexes for attribute_cascade
CREATE INDEX idx_attribute_cascade_attribute ON flexitype.attribute_cascade(attribute_id);
CREATE INDEX idx_attribute_cascade_target ON flexitype.attribute_cascade(validation_target_field);

-- We're removing the SQL functions and handling this logic in the repository code instead
-- This approach is more flexible and avoids SQL parsing issues

-- +goose Down
-- SQL in this section is executed when the migration is rolled back

-- Drop tables
DROP TABLE IF EXISTS flexitype.attribute_cascade CASCADE;
DROP TABLE IF EXISTS flexitype.object_value CASCADE;
DROP TABLE IF EXISTS flexitype.attribute_value CASCADE;
DROP TABLE IF EXISTS flexitype.instance CASCADE;
DROP TABLE IF EXISTS flexitype.validation_rule CASCADE;
DROP TABLE IF EXISTS flexitype.attribute_definition CASCADE;
DROP TABLE IF EXISTS flexitype.type_definition CASCADE;

-- Drop schema
DROP SCHEMA IF EXISTS flexitype CASCADE;