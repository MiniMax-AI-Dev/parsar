-- +goose Up
ALTER TABLE sessions ADD COLUMN creation_request_hash text
    CHECK (creation_request_hash ~ '^[0-9a-f]{64}$');

-- +goose Down
ALTER TABLE sessions DROP COLUMN creation_request_hash;
