-- +flexitype:no-transaction
--
-- Dropped concurrently for the reason they are built concurrently: a plain
-- DROP INDEX takes ACCESS EXCLUSIVE on the table.
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_trgm_lower;
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_trgm;
