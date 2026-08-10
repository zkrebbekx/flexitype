-- Tenant index for the outbox, so a tenant purge seeks its rows.
--
-- flexitype_event_outbox had no index leading with tenant_id: its indexes
-- serve the relay (claimable, pending, feed_seq, prune, parked). A tenant
-- erasure's residual redaction therefore scanned EVERY tenant's outbox to
-- find one tenant's rows, inside the erasure transaction, while the relay's
-- Finalize waited on those locks holding the global expansion lock — so
-- feed-seq assignment, webhook fan-out and pub/sub finalization stopped
-- fleet-wide for the length of that scan.
--
-- With this index each redaction chunk seeks. The redaction is also chunked
-- now (residual_eraser.go), which bounds the work per statement; the index
-- is what bounds the work overall.
--
-- The index is partial: a row whose payload carries no entity_id holds no
-- erasable value, and the redaction never touches it. That is a correctness
-- statement, not a size one — a value event DOES carry entity_id and IS
-- indexed, so this predicate excludes schema and relationship events rather
-- than the rows the relay writes most.
--
-- +flexitype:no-transaction
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_event_outbox_tenant
    ON flexitype_event_outbox (tenant_id)
    WHERE payload->>'entity_id' IS NOT NULL;
