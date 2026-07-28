-- The retention pruner deleted with `recorded_at < cutoff` and no supporting
-- index, so every hourly pass scanned the whole outbox — the largest table in
-- an event-heavy deployment — and each deleted envelope cascaded into its
-- delivery rows.
--
-- Partial on expanded rows, because that is the only set the pruner deletes:
-- an envelope with no feed_seq is not eligible at all.
-- +flexitype:no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_event_outbox_prune
    ON flexitype_event_outbox (recorded_at)
    WHERE feed_seq IS NOT NULL;

-- The delivery-stats collector runs `GROUP BY status` on every Prometheus
-- scrape, and the existing indexes are partial on pending/inflight only — so
-- the scrape scanned every delivered row retained in the window.
-- +flexitype:no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_webhook_delivery_status
    ON flexitype_webhook_delivery (status);
