-- Decouple in-process dispatch from the advisory-locked expansion so
-- handler network I/O no longer runs while the sequencer lock is held.
-- Relays now lease a batch, dispatch outside any lock/transaction, then
-- finalize (stamp feed_seq + fan out deliveries) under the lock. A lease
-- that outlives its holder (crashed relay) is reclaimed once it expires.
ALTER TABLE flexitype_event_outbox
    ADD COLUMN claimed_at TIMESTAMPTZ,
    ADD COLUMN claimed_by TEXT;

-- The pending index already covers dispatched_at IS NULL; this partial
-- index speeds the "claimable" scan (pending and not currently leased).
DROP INDEX IF EXISTS idx_flexitype_event_outbox_pending;
CREATE INDEX idx_flexitype_event_outbox_pending
    ON flexitype_event_outbox (claimed_at, id)
    WHERE dispatched_at IS NULL;
