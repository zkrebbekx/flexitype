DROP TABLE flexitype_event_cursor;
DROP TABLE flexitype_webhook_delivery;
DROP TABLE flexitype_webhook_subscription;
DROP SEQUENCE flexitype_event_feed_seq;
DROP INDEX idx_flexitype_event_outbox_feed_seq;
ALTER TABLE flexitype_event_outbox DROP COLUMN feed_seq;
