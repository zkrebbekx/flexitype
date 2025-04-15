-- +goose Up
-- SQL in this section is executed when the migration is applied
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
    list_index INTEGER,                     -- For array properties, index in the list
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (attribute_value_id) REFERENCES flexitype.attribute_value(id) ON DELETE CASCADE,
    FOREIGN KEY (nested_object_id) REFERENCES flexitype.object_value(id) ON DELETE CASCADE,
    CONSTRAINT uq_object_property_idx UNIQUE (attribute_value_id, property_name, COALESCE(list_index, 0))
);

-- Create an index on attribute_value_id for quick lookups
CREATE INDEX idx_object_value_attr_value ON flexitype.object_value(attribute_value_id);

-- Create an index on nested_object_id for navigating hierarchical structures
CREATE INDEX idx_object_value_nested ON flexitype.object_value(nested_object_id);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.object_value CASCADE;