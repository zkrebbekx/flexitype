DROP INDEX IF EXISTS idx_flexitype_event_outbox_claimable;

ALTER TABLE flexitype_event_outbox
    DROP COLUMN IF EXISTS parked_at,
    DROP COLUMN IF EXISTS next_attempt_at;
