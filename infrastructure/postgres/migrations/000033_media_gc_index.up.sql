-- Cross-tenant index for the media-blob GC reference count.
--
-- +flexitype:no-transaction
--
-- MediaKeyRefCount / MediaKeyRefCounts count the live rows that reference an
-- object key ACROSS tenants (blob keys live in one shared namespace). The only
-- object-key index (000021) leads with tenant_id, so the cross-tenant count
-- could not seek: every media overwrite/remove paid a scan of all media rows
-- post-commit, and a right-to-erasure purge ran one such scan per purged key
-- inside the shared post-commit budget until the budget expired and the
-- receipt reported the remaining bytes unpurged.
--
-- Partial on live media rows, because the count reads exactly that set:
-- archived rows are deliberately not counted (see the domain port).
--
-- Built CONCURRENTLY: flexitype_attribute_value is the largest table in the
-- database, where a plain CREATE INDEX would take a lock conflicting with
-- every write. The build drops an INVALID namesake first, because a failed
-- concurrent build leaves one behind that IF NOT EXISTS would skip forever.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_class c JOIN pg_index i ON i.indexrelid = c.oid
                WHERE c.relname = 'idx_flexitype_attribute_value_media_key_live' AND NOT i.indisvalid) THEN
        EXECUTE 'DROP INDEX idx_flexitype_attribute_value_media_key_live';
    END IF;
END $$;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_flexitype_attribute_value_media_key_live
    ON flexitype_attribute_value ((value_json->>'object_key'))
    WHERE data_type = 'media' AND archived_at IS NULL;
