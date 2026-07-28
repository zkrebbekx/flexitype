-- +flexitype:no-transaction
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_event_outbox_prune;
-- +flexitype:no-transaction
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_webhook_delivery_status;
