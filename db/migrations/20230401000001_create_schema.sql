-- +goose Up
-- SQL in this section is executed when the migration is applied
CREATE SCHEMA IF NOT EXISTS flexitype;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back
DROP SCHEMA IF EXISTS flexitype CASCADE;