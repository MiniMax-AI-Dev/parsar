-- name: ListCatalogProviders :many
SELECT id::text, workspace_id::text, name, protocol, base_url, created_at, updated_at
FROM model_providers WHERE workspace_id = @workspace_id::uuid AND deleted_at IS NULL ORDER BY name, id;

-- name: CreateCatalogProvider :one
INSERT INTO model_providers(id, workspace_id, name, protocol, base_url, encrypted_key)
VALUES (@id::uuid, @workspace_id::uuid, @name, @protocol, @base_url, @encrypted_key)
RETURNING id::text, workspace_id::text, name, protocol, base_url, created_at, updated_at;

-- name: UpdateCatalogProvider :one
UPDATE model_providers SET name = @name, protocol = @protocol, base_url = @base_url,
  encrypted_key = coalesce(sqlc.narg(encrypted_key)::bytea, encrypted_key), updated_at = now()
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid AND deleted_at IS NULL
RETURNING id::text, workspace_id::text, name, protocol, base_url, created_at, updated_at;

-- name: DeleteCatalogProvider :execrows
UPDATE model_providers SET deleted_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid AND deleted_at IS NULL;

-- name: ListCatalogModels :many
SELECT m.id::text AS id, m.name, m.model_key, m.provider_id::text AS provider_id, p.name AS provider_name, p.protocol, m.context_window, m.max_output_tokens
FROM models m JOIN model_providers p ON p.id = m.provider_id AND p.workspace_id = m.workspace_id
WHERE m.workspace_id = @workspace_id::uuid AND m.credential_mode = 'core'
  AND m.deleted_at IS NULL AND m.status = 'active' AND p.deleted_at IS NULL ORDER BY p.name, m.name, m.id;

-- name: CreateCatalogModel :one
INSERT INTO models(id, slug, workspace_id, provider_id, name, model_key, context_window, max_output_tokens, provider_type, adapter, credential_mode, created_by, created_at, updated_at)
SELECT @id::uuid, @slug, p.workspace_id, p.id, @name, @model_key, @context_window, @max_output_tokens, 'agents_api', 'agents_api', 'core', @created_by::uuid, now(), now()
FROM model_providers p WHERE p.workspace_id = @workspace_id::uuid AND p.id = @provider_id::uuid AND p.deleted_at IS NULL
RETURNING id::text, name, model_key, provider_id::text, context_window, max_output_tokens;

-- name: RenameCatalogModel :one
UPDATE models m SET name = @name, updated_at = now()
FROM model_providers p
WHERE m.workspace_id = @workspace_id::uuid AND m.id = @id::uuid
  AND m.credential_mode = 'core' AND m.deleted_at IS NULL AND p.id = m.provider_id AND p.deleted_at IS NULL
RETURNING m.id::text AS id, m.name, m.model_key, m.provider_id::text AS provider_id, m.context_window, m.max_output_tokens;

-- name: DeleteCatalogModel :execrows
UPDATE models SET deleted_at = now(), updated_at = now()
WHERE workspace_id = @workspace_id::uuid AND id = @id::uuid AND credential_mode = 'core' AND deleted_at IS NULL;

-- name: GetCatalogModelExecution :one
SELECT m.model_key, p.id::text AS provider_id, p.protocol, p.base_url, p.encrypted_key, m.context_window, m.max_output_tokens
FROM models m JOIN model_providers p ON p.id = m.provider_id AND p.workspace_id = m.workspace_id
WHERE m.workspace_id = @workspace_id::uuid AND m.id = @id::uuid AND m.credential_mode = 'core'
  AND m.status = 'active' AND m.deleted_at IS NULL AND p.deleted_at IS NULL
FOR SHARE OF m, p;

-- name: GetCatalogModelChoice :one
SELECT m.model_key, p.protocol, m.context_window, m.max_output_tokens
FROM models m JOIN model_providers p ON p.id = m.provider_id AND p.workspace_id = m.workspace_id
WHERE m.workspace_id = @workspace_id::uuid AND m.id = @id::uuid AND m.credential_mode = 'core'
  AND m.status = 'active' AND m.deleted_at IS NULL AND p.deleted_at IS NULL;

-- name: GetCatalogRunWorkspace :one
SELECT workspace_id::text FROM agent_runs WHERE id = @id::uuid;

-- name: EnsureCatalogCoreSession :one
INSERT INTO product_core_sessions (id, workspace_id, conversation_id, agent_id, request, provider_snapshot)
SELECT @id::uuid, r.workspace_id, r.conversation_id, r.agent_id, @request::jsonb, @provider_snapshot
FROM agent_runs r WHERE r.id = @run_id::uuid
ON CONFLICT (conversation_id, agent_id) DO UPDATE SET id = product_core_sessions.id
RETURNING *;

-- name: GetCatalogCoreSession :one
SELECT s.id::text AS id, s.workspace_id::text AS workspace_id, s.core_session_id, s.request, s.provider_snapshot
FROM product_core_sessions s JOIN agent_runs r ON r.conversation_id = s.conversation_id AND r.agent_id = s.agent_id WHERE r.id = @run_id::uuid;
