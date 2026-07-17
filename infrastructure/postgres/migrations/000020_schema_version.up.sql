-- Per-tenant schema version: a monotonic counter bumped whenever ANY type,
-- attribute or relationship definition changes. The GraphQL API (application/
-- gql) derives a tenant's schema from its live definitions and caches the built
-- schema in-process, per replica. That cache was previously invalidated only by
-- an in-process definition event, but event delivery is once-per-cluster (the
-- outbox leases each envelope to a single replica), so a definition added on one
-- replica stayed invisible to every OTHER replica's GraphQL schema until that
-- replica restarted (issue #192).
--
-- This counter is maintained by row triggers on the three definition tables,
-- not by application code, so it stays correct across EVERY write path — create,
-- update, archive/restore (a soft UPDATE) and hard delete — with no drift. A
-- replica tags each cached schema with the version it was built against and
-- rebuilds when the tenant's persisted version has moved past it, so a
-- definition change on any replica propagates to all of them.
CREATE TABLE flexitype_schema_version (
    tenant_id TEXT PRIMARY KEY,
    version   BIGINT NOT NULL
);

-- Bump one tenant's version, seeding the row at 1 on first sight. Called by the
-- trigger with the affected tenant id.
CREATE OR REPLACE FUNCTION flexitype_bump_schema_version(p_tenant text)
RETURNS void AS $$
BEGIN
    INSERT INTO flexitype_schema_version (tenant_id, version)
    VALUES (p_tenant, 1)
    ON CONFLICT (tenant_id)
    DO UPDATE SET version = flexitype_schema_version.version + 1;
END;
$$ LANGUAGE plpgsql;

-- AFTER-row trigger body shared by all three definition tables: bump the
-- affected tenant's version. INSERT/UPDATE carry the tenant in NEW; DELETE in
-- OLD. Returns NULL because an AFTER trigger's return value is ignored.
CREATE OR REPLACE FUNCTION flexitype_schema_version_trg() RETURNS trigger AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM flexitype_bump_schema_version(OLD.tenant_id);
    ELSE
        PERFORM flexitype_bump_schema_version(NEW.tenant_id);
    END IF;
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- The schema is derived from live type, attribute and relationship definitions,
-- and archive is a soft UPDATE, so every row event on any of the three tables
-- must bump the tenant's version.
CREATE TRIGGER flexitype_schema_version_type
    AFTER INSERT OR UPDATE OR DELETE ON flexitype_type_definition
    FOR EACH ROW EXECUTE FUNCTION flexitype_schema_version_trg();

CREATE TRIGGER flexitype_schema_version_attribute
    AFTER INSERT OR UPDATE OR DELETE ON flexitype_attribute_definition
    FOR EACH ROW EXECUTE FUNCTION flexitype_schema_version_trg();

CREATE TRIGGER flexitype_schema_version_relationship
    AFTER INSERT OR UPDATE OR DELETE ON flexitype_relationship_definition
    FOR EACH ROW EXECUTE FUNCTION flexitype_schema_version_trg();

-- Backfill: seed every tenant that already owns a definition at version 1. The
-- migration runs in one transaction with no concurrent writers, so the union of
-- the distinct tenant ids across the three definition tables captures every
-- tenant. ON CONFLICT DO NOTHING keeps it idempotent against any rows the
-- triggers may have already produced.
INSERT INTO flexitype_schema_version (tenant_id, version)
SELECT tenant_id, 1
  FROM (
        SELECT DISTINCT tenant_id FROM flexitype_type_definition
        UNION
        SELECT DISTINCT tenant_id FROM flexitype_attribute_definition
        UNION
        SELECT DISTINCT tenant_id FROM flexitype_relationship_definition
       ) AS t
ON CONFLICT (tenant_id) DO NOTHING;
