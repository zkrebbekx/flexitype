-- Change-sets: reviewable batches of value mutations that leave live data
-- untouched until published. `mutations` is the ordered batch; publish_at
-- schedules a deferred apply picked up by the scheduler.
CREATE TABLE flexitype_changeset (
    id               TEXT PRIMARY KEY,
    tenant_id        TEXT NOT NULL,
    name             TEXT NOT NULL,
    state            TEXT NOT NULL,
    require_approval BOOLEAN NOT NULL DEFAULT FALSE,
    author           TEXT NOT NULL DEFAULT '',
    approver         TEXT NOT NULL DEFAULT '',
    mutations        JSONB NOT NULL DEFAULT '[]',
    publish_at       TIMESTAMPTZ,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL,
    published_at     TIMESTAMPTZ
);

CREATE INDEX flexitype_changeset_tenant ON flexitype_changeset (tenant_id, created_at DESC);
-- The scheduler scans for approved sets whose publish time has arrived.
CREATE INDEX flexitype_changeset_due ON flexitype_changeset (state, publish_at)
    WHERE publish_at IS NOT NULL;
