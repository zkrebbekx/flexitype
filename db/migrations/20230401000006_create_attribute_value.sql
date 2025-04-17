-- +goose Up
-- SQL in this section is executed when the migration is applied
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
                                           FOREIGN KEY (instance_id) REFERENCES flexitype.instance (id) ON DELETE CASCADE,
                                           CONSTRAINT uq_attr_value_instance_name_idx UNIQUE (instance_id, attribute_name, list_index)
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