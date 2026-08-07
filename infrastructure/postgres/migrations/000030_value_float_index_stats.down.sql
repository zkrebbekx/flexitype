-- +flexitype:no-transaction
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_uniq_float;
DROP STATISTICS IF EXISTS st_flexitype_attribute_value_int;
DROP STATISTICS IF EXISTS st_flexitype_attribute_value_float;
DROP STATISTICS IF EXISTS st_flexitype_attribute_value_text;
DROP STATISTICS IF EXISTS st_flexitype_attribute_value_time;
