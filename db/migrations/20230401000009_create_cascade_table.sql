-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE TABLE flexitype.attribute_cascade (
    id SERIAL NOT NULL,
    attribute_id VARCHAR(255) NOT NULL,
    cascade_id VARCHAR(255) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    behavior VARCHAR(50) NOT NULL DEFAULT 'inherit',  -- inherit, override, disabled
    logic TEXT,
    weight INT NOT NULL DEFAULT 100,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (id),
    FOREIGN KEY (attribute_id) REFERENCES flexitype.attribute_definition(id) ON DELETE CASCADE,
    CONSTRAINT uq_attribute_cascade_id UNIQUE (attribute_id, cascade_id)
);

-- Create an index on attribute_id for quick lookups
CREATE INDEX idx_attribute_cascade_attribute ON flexitype.attribute_cascade(attribute_id);

-- Migrate existing cascade data
INSERT INTO flexitype.attribute_cascade (
    attribute_id, 
    cascade_id,
    enabled, 
    behavior, 
    logic, 
    weight
)
SELECT 
    id AS attribute_id,
    COALESCE(cascade_logic, id) AS cascade_id,
    cascade_enabled,
    cascade_behavior,
    cascade_logic,
    100 AS weight
FROM 
    flexitype.attribute_definition
WHERE 
    cascade_enabled = TRUE;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP TABLE IF EXISTS flexitype.attribute_cascade CASCADE;