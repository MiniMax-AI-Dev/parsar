-- +goose Up
CREATE TABLE vault_credentials (
    id uuid PRIMARY KEY,
    vault_id uuid NOT NULL REFERENCES vaults(id) ON DELETE CASCADE,
    name text NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 256),
    auth_type text NOT NULL CHECK (auth_type = 'static_bearer'),
    mcp_server_url text NOT NULL,
    token_ciphertext bytea NOT NULL CHECK (octet_length(token_ciphertext) > 0),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

-- +goose Down
DROP TABLE vault_credentials;
