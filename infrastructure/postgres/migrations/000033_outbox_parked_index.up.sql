-- Recovery surface for parked outbox envelopes (issue #478).
--
-- Migration 000023 added parked_at as the terminal state for an envelope
-- that exhausts its retry budget, and its comment promised "redrive clears
-- the flag". Nothing did: Redeliver touched only flexitype_webhook_delivery,
-- Claim filtered parked rows out, and a parked row never got a feed_seq — so
-- the feed never showed it, the pruner never removed it, and the pending
-- metric counted it for ever. This release adds the parked listing, the
-- redrive endpoint, the parked gauge and the parked retention bound.
--
-- The index below serves the four new readers of the parked set: the
-- per-tenant parked listing (tenant_id, id keyset pages), the per-tenant
-- redrive, the per-scrape parked count, and the parked retention prune. It
-- is partial over exactly the parked rows, so in a healthy deployment it is
-- empty and costs no write amplification on the hot outbox insert path; its
-- size is proportional to the parked backlog, never to retained history.
CREATE INDEX IF NOT EXISTS idx_flexitype_event_outbox_parked
    ON flexitype_event_outbox (tenant_id, id)
    WHERE parked_at IS NOT NULL;
