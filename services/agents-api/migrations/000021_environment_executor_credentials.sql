-- +goose Up
CREATE TABLE environment_executor_credentials (
    environment_id uuid PRIMARY KEY REFERENCES environments(id),
    token_sha256 text NOT NULL UNIQUE CHECK (token_sha256 ~ '^[0-9a-f]{64}$'),
    issued_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    revoked_at timestamptz
);

-- +goose Down
DROP TABLE environment_executor_credentials;
