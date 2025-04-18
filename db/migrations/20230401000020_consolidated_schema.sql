-- +goose Up
-- SQL in this section is executed when the migration is applied

-- Create schema
CREATE SCHEMA IF NOT EXISTS flexitype;

-- Create versioning tables for type definitions
CREATE TABLE flexitype.type_definition (
                                                   name VARCHAR(255) NOT NULL,
                                                   version INTEGER NOT NULL,
                                                   description TEXT,
                                                   parent_type_name VARCHAR(255),
                                                   created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                   updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                   archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
                                                   PRIMARY KEY (name, version)
);

-- Create versioning tables for attribute definitions
CREATE TABLE flexitype.attribute_definition (
                                                        type_name VARCHAR(255) NOT NULL,
                                                        type_version INTEGER NOT NULL,
                                                        name VARCHAR(255) NOT NULL,
                                                        description TEXT,
                                                        data_type VARCHAR(50) NOT NULL,
                                                        required BOOLEAN NOT NULL DEFAULT FALSE,
                                                        default_value TEXT,
                                                        multi_valued BOOLEAN NOT NULL DEFAULT FALSE,
                                                        disabled BOOLEAN NOT NULL DEFAULT FALSE,
                                                        created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                        updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                        PRIMARY KEY (type_name, type_version, name),
                                                        FOREIGN KEY (type_name, type_version) REFERENCES flexitype.type_definition(name, version)
);

-- Create versioning tables for validation rules
CREATE TABLE flexitype.validation_rule (
                                                   id SERIAL PRIMARY KEY,
                                                   type_name VARCHAR(255) NOT NULL,
                                                   type_version INTEGER NOT NULL,
                                                   attribute_name VARCHAR(255) NOT NULL,
                                                   rule_type VARCHAR(100) NOT NULL,
                                                   parameters TEXT,
                                                   sort_order INTEGER NOT NULL DEFAULT 0,
                                                   created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                   FOREIGN KEY (type_name, type_version, attribute_name) REFERENCES flexitype.attribute_definition(type_name, type_version, name)
);

-- Create versioning tables for attribute cascades
CREATE TABLE flexitype.attribute_cascade (
                                                     id SERIAL PRIMARY KEY,
                                                     type_name VARCHAR(255) NOT NULL,
                                                     type_version INTEGER NOT NULL,
                                                     attribute_name VARCHAR(255) NOT NULL,
                                                     cascade_id VARCHAR(255) NOT NULL,
                                                     enabled BOOLEAN NOT NULL DEFAULT TRUE,
                                                     behavior VARCHAR(50) NOT NULL,
                                                     logic TEXT NOT NULL,
                                                     weight INTEGER NOT NULL DEFAULT 0,
                                                     validation_action VARCHAR(50),
                                                     validation_target_field VARCHAR(255),
                                                     validation_values JSONB,
                                                     validation_string_value TEXT,
                                                     validation_numeric_value DOUBLE PRECISION,
                                                     created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
                                                     FOREIGN KEY (type_name, type_version, attribute_name) REFERENCES flexitype.attribute_definition(type_name, type_version, name)
);

-- Create index for case-insensitive name lookups
CREATE INDEX idx_type_definition_name_lower ON flexitype.type_definition(LOWER(name));

-- Create index for case-insensitive attribute name lookups
CREATE INDEX idx_attribute_definition_name_lower ON flexitype.attribute_definition(type_name, type_version, LOWER(name));

-- Create instance table
CREATE TABLE flexitype.instance (
    id VARCHAR(255) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    type_name VARCHAR(255) NOT NULL,
    type_version INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE DEFAULT NULL,
    PRIMARY KEY (id, version),
    FOREIGN KEY (type_name, type_version) REFERENCES flexitype.type_definition(name, version)
);

-- Create attribute_value table
CREATE TABLE flexitype.attribute_value (
    id SERIAL PRIMARY KEY,
    instance_id VARCHAR(255) NOT NULL,
    instance_version INTEGER NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,
    value_type VARCHAR(50) NOT NULL,
    string_value TEXT,
    int_value BIGINT,
    float_value DOUBLE PRECISION,
    boolean_value BOOLEAN,
    date_value TIMESTAMP WITH TIME ZONE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,
    list_index INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (instance_id, instance_version) REFERENCES flexitype.instance(id, version)
);

-- Create index for efficient attribute_value lookups
CREATE INDEX idx_attribute_value_instance ON flexitype.attribute_value(instance_id, instance_version);
CREATE INDEX idx_attribute_value_name_lower ON flexitype.attribute_value(LOWER(attribute_name));

-- Create object_value table for nested attributes
CREATE TABLE flexitype.object_value (
    id SERIAL PRIMARY KEY,
    attribute_value_id INTEGER NOT NULL,
    property_name VARCHAR(255) NOT NULL,
    value_type VARCHAR(50) NOT NULL,
    string_value TEXT,
    int_value BIGINT,
    float_value DOUBLE PRECISION,
    boolean_value BOOLEAN,
    date_value TIMESTAMP WITH TIME ZONE,
    list_index INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (attribute_value_id) REFERENCES flexitype.attribute_value(id) ON DELETE CASCADE
);

-- Create index for efficient object_value lookups
CREATE INDEX idx_object_value_attribute ON flexitype.object_value(attribute_value_id);

-- Create index for efficient cascade lookups
CREATE INDEX idx_attribute_cascade_type_attr ON flexitype.attribute_cascade(type_name, type_version, attribute_name);

-- Add comments for better documentation
COMMENT ON TABLE flexitype.type_definition IS 'Stores type definitions with name as the primary key';
COMMENT ON TABLE flexitype.attribute_definition IS 'Stores attribute definitions with composite primary key (type_name, type_version, name)';
COMMENT ON TABLE flexitype.instance IS 'Stores instances with references to type definitions by name';
COMMENT ON TABLE flexitype.attribute_value IS 'Stores attribute values with references to attributes by name';
COMMENT ON COLUMN flexitype.attribute_value.attribute_name IS 'Name of the attribute this value belongs to (references attribute_definition.name)';
COMMENT ON COLUMN flexitype.type_definition.name IS 'Unique name that serves as the primary identifier for this type';
COMMENT ON COLUMN flexitype.attribute_definition.name IS 'Name of the attribute, part of the composite primary key';

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
-- For safety, we won't provide a rollback script for this consolidated migration
-- It should be manually restored from a backup if needed

-- DROP SCHEMA flexitype CASCADE;