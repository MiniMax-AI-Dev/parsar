-- +goose Up

ALTER TABLE secrets
  ADD COLUMN management_workspace_id uuid REFERENCES workspaces(id);

-- Creation metadata and the workspace runtime-registration pointer are
-- existing provenance. Conflicting or missing ownership remains unassigned.
WITH provenance AS (
  SELECT s.id AS secret_id, w.id AS workspace_id
  FROM secrets s JOIN workspaces w ON lower(s.metadata->>'workspace_id') = w.id::text
  UNION
  SELECT s.id, w.id
  FROM secrets s JOIN workspaces w ON lower(w.config->>'runtime_credential_secret_id') = s.id::text
), owners AS (
  SELECT secret_id, min(workspace_id::text)::uuid AS workspace_id
  FROM provenance
  GROUP BY secret_id
  HAVING count(*) = 1
)
UPDATE secrets s
SET management_workspace_id = o.workspace_id
FROM owners o
WHERE s.id = o.secret_id;

COMMENT ON COLUMN secrets.management_workspace_id IS
  'Workspace whose owner/admin may disable this secret; does not restrict shared use';

-- +goose Down

ALTER TABLE secrets DROP COLUMN management_workspace_id;
