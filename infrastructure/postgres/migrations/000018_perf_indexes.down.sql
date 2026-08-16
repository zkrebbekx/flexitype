DROP INDEX IF EXISTS idx_flexitype_attribute_value_decimal;
DROP INDEX IF EXISTS idx_flexitype_attribute_value_tenant_entity;
DROP INDEX IF EXISTS idx_flexitype_webhook_delivery_pending;

-- The pg_trgm GIN indexes are NOT restored here.
--
-- This block used to rebuild them "matching 000004", because 000004 created
-- them and 000018 dropped them. 000041 owns them now, and builds them
-- CONCURRENTLY — so rebuilding them here left two GIN indexes on the largest
-- table that no up-migration at this version owns, built with a plain,
-- lock-taking CREATE INDEX inside a transaction, and contradicted what
-- 000021's own down file says happens to them.
