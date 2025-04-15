-- +goose Up
-- SQL in this section is executed when the migration is applied
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

-- Create an index on attribute_id for quick lookups
CREATE INDEX idx_validation_rule_attribute ON flexitype.validation_rule(attribute_id);

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.validation_rule CASCADE;