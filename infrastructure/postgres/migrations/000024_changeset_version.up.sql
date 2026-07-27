-- Change-sets are the one multi-user review artifact in the system, and they
-- had no optimistic locking. Every mutation was a read-modify-write of the
-- whole record with no version check, so two reviewers editing one set lost
-- each other's mutations, and an edit that raced an approval could write the
-- pre-approval state back — silently reverting the approval.
--
-- Existing rows start at 1, which is what a client that has never read a
-- version will send.
ALTER TABLE flexitype_changeset
    ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1;
