-- +goose Up
CREATE TABLE execution_project_scopes (
    tenant_id uuid PRIMARY KEY CHECK (tenant_id <> '00000000-0000-0000-0000-000000000000'),
    organization_id text NOT NULL CHECK (organization_id <> ''),
    project_id text NOT NULL CHECK (project_id <> ''),
    UNIQUE (organization_id, project_id)
);

-- +goose Down
DROP TABLE execution_project_scopes;
