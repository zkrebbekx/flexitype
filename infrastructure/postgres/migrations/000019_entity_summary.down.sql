DROP TRIGGER IF EXISTS flexitype_entity_summary_maintain ON flexitype_attribute_value;
DROP FUNCTION IF EXISTS flexitype_entity_summary_trg();
DROP FUNCTION IF EXISTS flexitype_refresh_entity_summary(text, char, text);
DROP TABLE IF EXISTS flexitype_entity_summary;
