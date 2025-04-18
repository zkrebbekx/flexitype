-- Update attribute_value table to use attribute_name instead of attribute_id
ALTER TABLE flexitype.attribute_value RENAME COLUMN attribute_id TO attribute_name;

-- Update attribute value queries to use case-insensitive lookups
CREATE INDEX idx_attribute_value_name_lower ON flexitype.attribute_value (LOWER(attribute_name));

-- Update comments for clarity
COMMENT ON COLUMN flexitype.attribute_value.attribute_name IS 'Name of the attribute this value belongs to (references attribute_definition_version.name)';