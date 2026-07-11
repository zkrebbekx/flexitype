-- Duplicate-detection matching rules and dismissed candidate pairs.
-- A rule declares an attribute plus a comparison strategy for one type;
-- dismissals record pairs an operator has judged not duplicates, so they
-- never resurface on re-scan. Detection is report-only (no merge).
CREATE TABLE flexitype_match_rule (
    id                      TEXT PRIMARY KEY,
    tenant_id               TEXT NOT NULL,
    type_definition_id      TEXT NOT NULL,
    attribute_definition_id TEXT NOT NULL,
    strategy                TEXT NOT NULL,
    threshold               DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL
);

CREATE INDEX flexitype_match_rule_type
    ON flexitype_match_rule (tenant_id, type_definition_id);

CREATE TABLE flexitype_match_dismissal (
    rule_id    TEXT NOT NULL REFERENCES flexitype_match_rule (id) ON DELETE CASCADE,
    tenant_id  TEXT NOT NULL,
    entity_a   TEXT NOT NULL,
    entity_b   TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (rule_id, entity_a, entity_b)
);
