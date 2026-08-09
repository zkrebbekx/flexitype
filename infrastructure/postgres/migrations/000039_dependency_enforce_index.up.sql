-- The value-write path asks once per transaction whether a tenant has any
-- dependency that refuses a write. Almost every tenant has none, and without
-- an index that question is a sequential scan on the hot path.
--
-- Partial on the answer we look for, so the index holds only the enforcing
-- rules rather than every dependency in the deployment.
CREATE INDEX IF NOT EXISTS idx_dependency_enforced_on_write
    ON flexitype_attribute_value_dependency (tenant_id)
    WHERE archived_at IS NULL
      AND effect ->> 'enforce' = 'on_write'
      AND effect ->> 'required' = 'true';
