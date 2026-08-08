-- The fold is not reversible: once a parents bound has moved into the
-- children bound, nothing records which side it came from, and the previous
-- release ignored it anyway. Reverting the code is enough to restore the old
-- behaviour, so this step is deliberately empty rather than guessing.
SELECT 1;
