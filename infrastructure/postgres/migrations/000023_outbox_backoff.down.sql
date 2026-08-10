-- +flexitype:no-transaction
--
-- Mirrors the up-file: dropping the index CONCURRENTLY needs the directive,
-- and MigrateDown honours it.
DROP INDEX CONCURRENTLY IF EXISTS idx_flexitype_event_outbox_claimable;

ALTER TABLE flexitype_event_outbox
    DROP COLUMN IF EXISTS parked_at,
    DROP COLUMN IF EXISTS next_attempt_at;
