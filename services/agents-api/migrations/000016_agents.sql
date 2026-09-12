-- +goose Up
CREATE TABLE agents (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    metadata jsonb NOT NULL CHECK (jsonb_typeof(metadata) = 'object'),
    configuration jsonb NOT NULL CHECK (jsonb_typeof(configuration) = 'object'),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
CREATE INDEX agents_tenant_created_idx ON agents (tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE agents;
