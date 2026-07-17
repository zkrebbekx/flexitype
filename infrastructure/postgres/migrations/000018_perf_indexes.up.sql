-- Performance indexes (post-1.0 review).

-- Webhook ClaimDue finds the earliest PENDING delivery per subscription. The
-- existing (subscription_id, feed_seq) index is not partial, so the
-- min(feed_seq) WHERE status='pending' subquery walks all *delivered* history
-- first. A partial index makes it an index-only probe.
CREATE INDEX IF NOT EXISTS idx_flexitype_webhook_delivery_pending
    ON flexitype_webhook_delivery (subscription_id, feed_seq)
    WHERE status = 'pending';

-- Batched entity hydration keys on (tenant_id, entity_id) (grid, GraphQL node
-- batches, relationship traversals), but the only value index leads with
-- (tenant_id, type_definition_id, entity_id) and the 2-tuple skips the middle
-- column, forcing a full-table scan.
CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_tenant_entity
    ON flexitype_attribute_value (tenant_id, entity_id)
    WHERE archived_at IS NULL;

-- The decimal uniqueness probe compares value_text::numeric, and the cast
-- defeats the plain text index, scanning the whole attribute partition per
-- write. Index the expression. Scoped to decimal rows so the ::numeric cast is
-- evaluated only where value_text is a valid number (value_text also holds
-- string/enum/url/email text for other data types).
CREATE INDEX IF NOT EXISTS idx_flexitype_attribute_value_decimal
    ON flexitype_attribute_value (attribute_definition_id, ((value_text)::numeric))
    WHERE data_type = 'decimal' AND archived_at IS NULL;

-- The two pg_trgm GIN indexes were meant to accelerate contains/icontains, but
-- those use strpos() (not LIKE/ILIKE/%/regex), which cannot use a trigram GIN,
-- and no query in the codebase uses an indexable trigram operator. They are
-- pure write-path cost (GIN maintenance on the hottest table) for zero read
-- benefit, so drop them.
DROP INDEX IF EXISTS idx_flexitype_attribute_value_trgm;
DROP INDEX IF EXISTS idx_flexitype_attribute_value_trgm_lower;
