-- name: EnsureProjectScope :one
INSERT INTO execution_project_scopes (tenant_id, organization_id, project_id)
VALUES ($1, $2, $3)
ON CONFLICT (tenant_id) DO UPDATE SET tenant_id = EXCLUDED.tenant_id
WHERE execution_project_scopes.organization_id = EXCLUDED.organization_id
  AND execution_project_scopes.project_id = EXCLUDED.project_id
RETURNING tenant_id;
