DROP INDEX IF EXISTS idx_flexitype_event_outbox_pending;
CREATE INDEX idx_flexitype_event_outbox_pending
    ON flexitype_event_outbox (id)
    WHERE dispatched_at IS NULL;

ALTER TABLE flexitype_event_outbox
    DROP COLUMN claimed_by,
    DROP COLUMN claimed_at;
