-- name: CreateStaticCredential :one
INSERT INTO vault_credentials (id, vault_id, name, auth_type, mcp_server_url, token_ciphertext)
SELECT sqlc.arg(id), v.id, sqlc.arg(name), 'static_bearer', sqlc.arg(mcp_server_url), sqlc.arg(token_ciphertext)
FROM vaults v
WHERE v.tenant_id = sqlc.arg(tenant_id) AND v.id = sqlc.arg(vault_id)
RETURNING id, vault_id, name, auth_type, mcp_server_url, created_at, updated_at;

-- name: GetCredential :one
SELECT c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id) AND v.id = sqlc.arg(vault_id) AND c.id = sqlc.arg(id);

-- name: UpdateStaticCredential :one
UPDATE vault_credentials c
SET token_ciphertext = sqlc.arg(token_ciphertext), updated_at = statement_timestamp()
FROM vaults v
WHERE v.id = c.vault_id AND v.tenant_id = sqlc.arg(tenant_id)
  AND v.id = sqlc.arg(vault_id) AND c.id = sqlc.arg(id)
  AND c.auth_type = 'static_bearer' AND c.mcp_server_url = sqlc.arg(mcp_server_url)
RETURNING c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at;

-- name: ListCredentials :many
SELECT c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id) AND v.id = sqlc.arg(vault_id)
  AND c.status = ANY(sqlc.arg(statuses)::text[])
  AND (sqlc.narg(after_created)::timestamptz IS NULL
    OR (NOT sqlc.arg(ascending)::boolean AND (c.created_at, c.id) < (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid))
    OR (sqlc.arg(ascending)::boolean AND (c.created_at, c.id) > (sqlc.narg(after_created)::timestamptz, sqlc.arg(after_id)::uuid)))
ORDER BY
  CASE WHEN sqlc.arg(ascending)::boolean THEN c.created_at END ASC,
  CASE WHEN sqlc.arg(ascending)::boolean THEN c.id END ASC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN c.created_at END DESC,
  CASE WHEN NOT sqlc.arg(ascending)::boolean THEN c.id END DESC
LIMIT sqlc.arg(page_limit);
