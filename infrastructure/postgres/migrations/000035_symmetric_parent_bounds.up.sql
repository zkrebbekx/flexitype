-- Fold a symmetric relationship's parents bounds into its children bounds.
--
-- A symmetric link has no parent side: enforceCardinality and
-- RelationshipRequirements read the CHILDREN bounds for a symmetric kind, so
-- a stored min_parents/max_parents was accepted at creation and then never
-- enforced. A symmetric spouse_of declared max_parents 1 admitted a-b, a-c
-- and a-d, and an unmet min_parents was invisible.
--
-- Construction now refuses those bounds, which leaves the rows written
-- before this release. Each one carries an intent the author expressed and
-- the system ignored, so the bound moves to the side that IS enforced
-- rather than being dropped:
--
--   * an unset children bound takes the parents bound outright;
--   * a set children bound keeps whichever is TIGHTER, because both were
--     declared as limits and honouring the looser one would widen what the
--     author asked for;
--   * the parents bounds are then cleared, so the row satisfies the new
--     construction rule and reloads cleanly.
--
-- Only symmetric definitions are touched. A directed definition enforces
-- both sides and is left exactly as it is.
--
-- GREATEST and LEAST IGNORE nulls (they return null only when every
-- argument is null), which is exactly the rule above: with one side unset
-- the other stands, and with both set the tighter one wins — the greater
-- minimum, the lesser maximum.
UPDATE flexitype_relationship_definition
   SET min_children = GREATEST(min_children, min_parents),
       max_children = LEAST(max_children, max_parents),
       min_parents  = NULL,
       max_parents  = NULL
 WHERE kind = 'symmetric'
   AND (min_parents IS NOT NULL OR max_parents IS NOT NULL);
