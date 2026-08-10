-- Per-row retry scheduling for the outbox lane.
--
-- +flexitype:no-transaction
--
-- The delivery lane already had next_attempt_at with exponential backoff and
-- an attempt cap. The outbox lane had neither: a failed row kept its low id,
-- claims are ordered by id, and there was no backoff and no cap — so the
-- oldest failing rows were re-claimed first on every pass, hammered the broker
-- every 2 seconds, and never let a newer envelope through. attempts was
-- written and never read anywhere.
--
-- One permanently failing row therefore stopped ALL external delivery: pub/sub
-- publishes, webhook fan-out and the events feed together, because a webhook
-- delivery row and feed_seq are only stamped for envelopes whose dispatch
-- fully succeeded.
--
-- docs/design/event-delivery.md listed exactly this as a problem the design
-- fixed. The fix had landed for the delivery lane only.

ALTER TABLE flexitype_event_outbox
    ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A row past the attempt cap is parked rather than retried forever. It
    -- stays readable, and redrive clears the flag; without a terminal state an
    -- undeliverable envelope is indistinguishable from a slow one.
    ADD COLUMN IF NOT EXISTS parked_at TIMESTAMPTZ;

-- The claim predicate is (dispatched_at IS NULL, not parked, due, unleased),
-- ordered by id. A partial index over exactly the claimable rows keeps the
-- scan proportional to the backlog rather than to the retained history, which
-- matters because dispatched rows stay until retention prunes them.
--
-- Built CONCURRENTLY for the reason 000033 records (issue #595): every write
-- inserts into this table, so a plain build blocks all of them for its
-- duration.
--
-- Both statements are idempotent, which is what makes running them outside a
-- transaction safe: ADD COLUMN IF NOT EXISTS is a metadata-only change here,
-- and the runner reaps an INVALID index before replaying the file.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_event_outbox_claimable
    ON flexitype_event_outbox (next_attempt_at, id)
    WHERE dispatched_at IS NULL AND parked_at IS NULL;
