-- +goose Up
CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    engine text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    idempotency_key text NOT NULL,
    request_hash text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (tenant_id, idempotency_key)
);
CREATE INDEX sessions_tenant_created_idx ON sessions (tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP TABLE sessions;
