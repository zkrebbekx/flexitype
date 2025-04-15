-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE flexitype.attribute_value (
    id SERIAL NOT NULL,
    instance_id VARCHAR(255) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL,  -- Using name instead of ID for easier querying and to handle inherited attributes
    value_type VARCHAR(50) NOT NULL,       -- string, int, float, boolean, date, etc.
    string_value TEXT,
    int_value BIGINT,
    float_value DOUBLE PRECISION,
    boolean_value BOOLEAN,
    date_value TIMESTAMP WITH TIME ZONE,
    is_default BOOLEAN NOT NULL DEFAULT FALSE,  -- Indicates if this is a default value or explicitly set
    list_index INTEGER,                         -- For multi-valued attributes, index in the list
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (instance_id) REFERENCES flexitype.instance(id) ON DELETE CASCADE,
    CONSTRAINT uq_attr_value_instance_name_idx UNIQUE (instance_id, attribute_name, COALESCE(list_index, 0))
);

-- Create an index on instance_id for quick lookups
CREATE INDEX idx_attribute_value_instance ON flexitype.attribute_value(instance_id);

-- Create an index on attribute_name for quick lookups
CREATE INDEX idx_attribute_value_name ON flexitype.attribute_value(attribute_name);

-- Create a composite index for filtering by attributes
CREATE INDEX idx_attribute_value_query ON flexitype.attribute_value(attribute_name, value_type);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.attribute_value CASCADE;