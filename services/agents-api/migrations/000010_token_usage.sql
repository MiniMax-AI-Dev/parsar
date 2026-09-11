-- +goose Up
ALTER TABLE turns ADD COLUMN token_usage jsonb;

-- +goose Down
ALTER TABLE turns DROP COLUMN token_usage;
