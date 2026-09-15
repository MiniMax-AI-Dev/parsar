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
