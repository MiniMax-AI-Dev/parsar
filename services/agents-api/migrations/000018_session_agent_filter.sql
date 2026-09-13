-- +goose Up
CREATE INDEX sessions_tenant_agent_created_idx
ON sessions (tenant_id, (configuration #>> '{agent,id}'), created_at DESC, id DESC);

-- +goose Down
DROP INDEX sessions_tenant_agent_created_idx;
