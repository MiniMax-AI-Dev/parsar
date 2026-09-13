-- name: IssueEnvironmentExecutorCredential :execrows
INSERT INTO environment_executor_credentials (environment_id, token_sha256)
VALUES ($1, $2) ON CONFLICT (environment_id) DO NOTHING;

-- name: RotateEnvironmentExecutorCredential :execrows
UPDATE environment_executor_credentials
SET token_sha256 = $2, issued_at = clock_timestamp(), revoked_at = NULL
WHERE environment_id = $1;

-- name: RevokeEnvironmentExecutorCredential :execrows
UPDATE environment_executor_credentials SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE environment_id = $1;

-- name: AuthenticateEnvironmentExecutor :one
SELECT s.tenant_id
FROM environment_executor_credentials c
JOIN environments e ON e.id = c.environment_id
JOIN sessions s ON s.id = e.session_id
WHERE c.environment_id = $1 AND c.token_sha256 = $2
    AND c.revoked_at IS NULL AND s.deleted_at IS NULL;
