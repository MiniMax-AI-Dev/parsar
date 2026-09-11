-- +goose Up
ALTER TABLE sessions ADD COLUMN configuration jsonb NOT NULL DEFAULT '{}'::jsonb
    CHECK (jsonb_typeof(configuration) = 'object');

-- +goose Down
ALTER TABLE sessions DROP COLUMN configuration;
