-- name: GetAttachedVaultIDs :many
SELECT id FROM vaults
WHERE tenant_id = sqlc.arg(tenant_id) AND id = ANY(sqlc.arg(vault_ids)::uuid[]);

-- name: FindMCPStaticCredentials :many
SELECT c.id, c.vault_id, c.name, c.auth_type, c.mcp_server_url, c.created_at, c.updated_at
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id)
  AND v.id = ANY(sqlc.arg(vault_ids)::uuid[])
  AND c.auth_type = 'static_bearer'
  AND c.mcp_server_url = sqlc.arg(mcp_server_url)
  AND (sqlc.narg(credential_id)::uuid IS NULL OR c.id = sqlc.narg(credential_id)::uuid)
ORDER BY c.id
LIMIT 2;

-- name: GetMCPStaticCredentialCiphertext :one
SELECT c.token_ciphertext
FROM vault_credentials c
JOIN vaults v ON v.id = c.vault_id
WHERE v.tenant_id = sqlc.arg(tenant_id)
  AND v.id = ANY(sqlc.arg(vault_ids)::uuid[])
  AND v.id = sqlc.arg(vault_id)
  AND c.id = sqlc.arg(credential_id)
  AND c.auth_type = 'static_bearer'
  AND c.mcp_server_url = sqlc.arg(mcp_server_url);
