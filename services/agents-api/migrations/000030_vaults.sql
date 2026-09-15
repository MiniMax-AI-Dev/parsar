-- +goose Up
CREATE TABLE vaults (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    name text CHECK (name IS NULL OR octet_length(name) BETWEEN 1 AND 256),
    metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

-- +goose Down
DROP TABLE vaults;
