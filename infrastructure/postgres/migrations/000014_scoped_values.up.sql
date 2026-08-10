-- Scoped values: an attribute value may vary by locale (en_AU, de_DE) and
-- by channel/context (web, print, marketplace). Empty string is the base
-- (unscoped) value. A value's identity within an entity and attribute is
-- (locale, channel), enforced per-scope in the value write path.
ALTER TABLE flexitype_attribute_value
    ADD COLUMN locale  TEXT NOT NULL DEFAULT '',
    ADD COLUMN channel TEXT NOT NULL DEFAULT '';

-- Entity hydration and per-scope lookups filter by (entity, locale, channel).
-- The index for that is built CONCURRENTLY in 000042: it is on the largest
-- table in the database, and this file cannot be a no-transaction file
-- because it also alters two tables. A database that already has the index
-- from this file keeps it — 000042 is IF NOT EXISTS.

-- Attribute-level scope enablement flags.
ALTER TABLE flexitype_attribute_definition
    ADD COLUMN localizable BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN scopable    BOOLEAN NOT NULL DEFAULT FALSE;
