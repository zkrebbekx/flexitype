-- +flexitype:no-transaction
--
-- Concurrent drops, for the same reason the builds are concurrent: a plain
-- DROP INDEX takes a lock conflicting with every write on the table.
--
-- The two pg_trgm indexes are 000041's now, and it drops them. Reverting past
-- this migration also reverts the LIKE/ILIKE predicate that makes them
-- useful, so they are left as 000018 leaves them — dropped.
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_webhook_subscription_active;
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_webhook_delivery_envelope;
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_entity_attr;
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_media_key;
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_attribute_value_entity_all;
