DROP TRIGGER IF EXISTS flexitype_schema_version_relationship ON flexitype_relationship_definition;
DROP TRIGGER IF EXISTS flexitype_schema_version_attribute ON flexitype_attribute_definition;
DROP TRIGGER IF EXISTS flexitype_schema_version_type ON flexitype_type_definition;
DROP FUNCTION IF EXISTS flexitype_schema_version_trg();
DROP FUNCTION IF EXISTS flexitype_bump_schema_version(text);
DROP TABLE IF EXISTS flexitype_schema_version;
