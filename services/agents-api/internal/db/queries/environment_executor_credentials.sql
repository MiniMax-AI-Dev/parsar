-- name: ExecutorProjectScopeExists :one
SELECT EXISTS (
    SELECT 1 FROM execution_project_scopes
    WHERE tenant_id = sqlc.arg(tenant_id) AND organization_id = sqlc.arg(organization_id) AND project_id = sqlc.arg(project_id)
);

-- name: IssueExecutorCredential :one
WITH issued AS (SELECT clock_timestamp() AS at)
INSERT INTO environment_executor_credentials
    (key_id, tenant_id, subject_kind, subject_id, environment_id, token_sha256, created_at, issued_at)
SELECT sqlc.arg(key_id), p.tenant_id, sqlc.arg(subject_kind), sqlc.arg(subject_id),
    sqlc.narg(environment_id)::uuid, sqlc.arg(token_sha256), issued.at, issued.at
FROM execution_project_scopes p CROSS JOIN issued
WHERE p.tenant_id = sqlc.arg(tenant_id) AND p.organization_id = sqlc.arg(organization_id)
    AND p.project_id = sqlc.arg(project_id)
    AND (sqlc.narg(environment_id)::uuid IS NULL OR EXISTS (
        SELECT 1 FROM environments e JOIN sessions s ON s.id = e.session_id
        WHERE e.id = sqlc.narg(environment_id) AND s.tenant_id = p.tenant_id AND s.deleted_at IS NULL
            AND s.creator_kind = sqlc.arg(subject_kind) AND s.creator_id = sqlc.arg(subject_id)
    ))
ON CONFLICT (key_id) DO NOTHING
RETURNING key_id, environment_id;

-- name: GetExecutorCredentialForPrincipal :one
SELECT c.environment_id
FROM environment_executor_credentials c
JOIN execution_project_scopes p ON p.tenant_id = c.tenant_id
WHERE c.key_id = sqlc.arg(key_id) AND c.tenant_id = sqlc.arg(tenant_id)
    AND c.subject_kind = sqlc.arg(subject_kind) AND c.subject_id = sqlc.arg(subject_id)
    AND p.organization_id = sqlc.arg(organization_id) AND p.project_id = sqlc.arg(project_id);

-- name: RotateExecutorCredential :one
UPDATE environment_executor_credentials
SET token_sha256 = sqlc.arg(token_sha256), issued_at = clock_timestamp(), revoked_at = NULL
WHERE key_id = sqlc.arg(key_id) AND tenant_id = sqlc.arg(tenant_id)
    AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id)
RETURNING key_id, environment_id;

-- name: RevokeExecutorCredential :execrows
UPDATE environment_executor_credentials SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE key_id = sqlc.arg(key_id) AND tenant_id = sqlc.arg(tenant_id)
    AND subject_kind = sqlc.arg(subject_kind) AND subject_id = sqlc.arg(subject_id);

-- name: AuthenticateEnvironmentExecutor :one
SELECT s.tenant_id
FROM environment_executor_credentials c
JOIN execution_project_scopes p ON p.tenant_id = c.tenant_id
JOIN environments e ON e.id = sqlc.arg(environment_id)
JOIN sessions s ON s.id = e.session_id
WHERE c.token_sha256 = sqlc.arg(token_sha256) AND c.revoked_at IS NULL
    AND c.tenant_id = s.tenant_id AND c.subject_kind = s.creator_kind AND c.subject_id = s.creator_id
    AND (c.environment_id IS NULL OR c.environment_id = e.id) AND s.deleted_at IS NULL;
