-- Saved views gain an optimistic-locking version.
--
-- Patch reads, merges and writes, so two concurrent patches — one setting the
-- sort, one renaming — each wrote the other's field back as it was before:
-- the same "one client silently clears what another set" outcome the sparse
-- decoder was added to remove, moved from an omitted field to a concurrent
-- write.
--
-- Existing rows start at 1, which is what a fresh read reports, so a client
-- that read before this migration and writes after it still matches.
ALTER TABLE flexitype_saved_view
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
