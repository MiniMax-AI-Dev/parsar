-- +goose Up
CREATE TABLE environment_templates (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    name text CHECK (name IS NULL OR char_length(name) BETWEEN 1 AND 256),
    network_access text NOT NULL CHECK (network_access IN ('enabled', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT statement_timestamp()
);
CREATE INDEX environment_templates_tenant_order ON environment_templates (tenant_id, created_at, id);

-- +goose Down
DROP TABLE environment_templates;
