-- +flexitype:no-transaction
--
-- Concurrent drop, for the same reason the build is concurrent: a plain
-- DROP INDEX takes a lock conflicting with every write on the table.
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_media_key_live;
