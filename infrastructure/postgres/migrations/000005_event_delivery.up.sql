-- Event delivery to external services (design: docs/design/event-delivery.md).
--
-- feed_seq is a gap-free sequence assigned by the expansion step (a single
-- advisory-locked sequencer) in commit order — the cursor space for the
-- events feed. NULL until the envelope has been expanded.
ALTER TABLE flexitype_event_outbox ADD COLUMN feed_seq BIGINT;
CREATE UNIQUE INDEX idx_flexitype_event_outbox_feed_seq
    ON flexitype_event_outbox (feed_seq)
    WHERE feed_seq IS NOT NULL;
CREATE SEQUENCE flexitype_event_feed_seq;

-- Webhook subscriptions: DB-backed so every replica serves the same set.
-- previous_secret stays signing-valid during rotation.
CREATE TABLE flexitype_webhook_subscription (
    id              CHAR(26) PRIMARY KEY,
    tenant_id       TEXT NOT NULL DEFAULT 'default',
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    secret          TEXT NOT NULL DEFAULT '',
    previous_secret TEXT NOT NULL DEFAULT '',
    event_types     TEXT[] NOT NULL DEFAULT '{}', -- empty = all events
    active          BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL,
    updated_at      TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id, name)
);

-- One delivery row per (envelope × matching subscription), created by
-- expansion. Workers claim due rows with SKIP LOCKED, POST outside any
-- transaction, then record the outcome; exponential backoff schedules
-- retries per row, so one dead consumer never blocks another.
CREATE TABLE flexitype_webhook_delivery (
    id               CHAR(26) PRIMARY KEY,
    subscription_id  CHAR(26) NOT NULL REFERENCES flexitype_webhook_subscription (id) ON DELETE CASCADE,
    envelope_id      CHAR(26) NOT NULL REFERENCES flexitype_event_outbox (id) ON DELETE CASCADE,
    tenant_id        TEXT NOT NULL,
    event_type       TEXT NOT NULL,
    feed_seq         BIGINT NOT NULL,
    status           TEXT NOT NULL DEFAULT 'pending', -- pending | inflight | delivered | dead
    attempts         INTEGER NOT NULL DEFAULT 0,
    next_attempt_at  TIMESTAMPTZ NOT NULL,
    lease_expires_at TIMESTAMPTZ,
    last_error       TEXT NOT NULL DEFAULT '',
    response_code    INTEGER NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_flexitype_webhook_delivery_due
    ON flexitype_webhook_delivery (next_attempt_at)
    WHERE status = 'pending';
CREATE INDEX idx_flexitype_webhook_delivery_inflight
    ON flexitype_webhook_delivery (lease_expires_at)
    WHERE status = 'inflight';
CREATE INDEX idx_flexitype_webhook_delivery_subscription
    ON flexitype_webhook_delivery (subscription_id, feed_seq);

-- Named feed cursors: one logical read position per consuming service,
-- advanced with compare-and-swap so replicated consumers cannot both win.
CREATE TABLE flexitype_event_cursor (
    tenant_id  TEXT NOT NULL,
    consumer   TEXT NOT NULL,
    position   BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, consumer)
);
