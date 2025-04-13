-- Drop indexes
DROP INDEX IF EXISTS idx_attributes_name;
DROP INDEX IF EXISTS idx_attributes_type;
DROP INDEX IF EXISTS idx_attribute_values_attribute_id;
DROP INDEX IF EXISTS idx_type_links_source_attr_id;
DROP INDEX IF EXISTS idx_type_links_target_attr_id;

-- Drop tables
DROP TABLE IF EXISTS type_links;
DROP TABLE IF EXISTS attribute_values;
DROP TABLE IF EXISTS attributes; 