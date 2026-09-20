-- name: CreateEnvironmentTemplate :one
INSERT INTO environment_templates (id, tenant_id, name, network_access, files, file_contents)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING id, tenant_id, name, network_access, created_at, updated_at, files;

-- name: GetEnvironmentTemplate :one
SELECT id, tenant_id, name, network_access, created_at, updated_at, files FROM environment_templates WHERE tenant_id = $1 AND id = $2;

-- name: UpdateEnvironmentTemplate :one
UPDATE environment_templates SET
    name = CASE WHEN sqlc.arg(set_name)::boolean THEN sqlc.narg(name)::text ELSE name END,
    network_access = CASE WHEN sqlc.arg(set_network)::boolean THEN sqlc.arg(network_access)::text ELSE network_access END,
    files = CASE WHEN sqlc.arg(set_files)::boolean THEN sqlc.arg(files)::jsonb ELSE files END,
    file_contents = CASE WHEN sqlc.arg(set_files)::boolean THEN sqlc.narg(file_contents)::bytea ELSE file_contents END,
    updated_at = clock_timestamp()
WHERE tenant_id = sqlc.arg(tenant_id) AND id = sqlc.arg(id)
RETURNING id, tenant_id, name, network_access, created_at, updated_at, files;

-- name: DeleteEnvironmentTemplate :one
DELETE FROM environment_templates WHERE tenant_id = $1 AND id = $2 RETURNING id;

-- name: ListEnvironmentTemplates :many
SELECT id, tenant_id, name, network_access, created_at, updated_at, files FROM environment_templates
WHERE tenant_id = sqlc.arg(tenant_id)
  AND (sqlc.narg(after_created)::timestamptz IS NULL
       OR (NOT sqlc.arg(ascending)::boolean AND (created_at, id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
       OR (sqlc.arg(ascending)::boolean AND (created_at, id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
    CASE WHEN sqlc.arg(ascending)::boolean THEN created_at END ASC,
    CASE WHEN sqlc.arg(ascending)::boolean THEN id END ASC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN created_at END DESC,
    CASE WHEN NOT sqlc.arg(ascending)::boolean THEN id END DESC
LIMIT sqlc.arg(page_limit);

-- name: ResolveEnvironmentTemplate :one
SELECT * FROM environment_templates WHERE tenant_id = $1 AND id = $2;
