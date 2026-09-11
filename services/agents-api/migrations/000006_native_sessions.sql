-- +goose Up
ALTER TABLE session_devices ADD COLUMN native_session_id text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE session_devices DROP COLUMN native_session_id;
