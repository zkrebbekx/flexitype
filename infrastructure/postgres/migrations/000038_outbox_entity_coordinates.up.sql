-- Entity coordinates on the outbox row.
--
-- An envelope names the AGGREGATE that emitted it, which for a value event is
-- the attribute VALUE. A consumer that only wants "entity E changed, re-read
-- it" could not answer that from the envelope and had to decode the payload,
-- which couples every router to the payload schema of every event type it
-- routes.
--
-- Both columns are nullable: an event that concerns no entity (a schema
-- change) or more than one (a relationship link, which names two endpoints)
-- leaves them empty. A row written before this migration also leaves them
-- empty, and a consumer that falls back to the payload keeps working.
ALTER TABLE flexitype_event_outbox
    ADD COLUMN IF NOT EXISTS type_definition_id text,
    ADD COLUMN IF NOT EXISTS entity_id text;
