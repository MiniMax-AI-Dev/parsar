-- +goose Up

ALTER TABLE secrets
  ADD COLUMN management_workspace_id uuid REFERENCES workspaces(id);

-- Only existing explicit workspace provenance is safe to carry forward.
-- Shared legacy credentials without provenance remain usable, but cannot
-- be disabled until an operator assigns a verified management workspace.
UPDATE secrets s
SET management_workspace_id = w.id
FROM workspaces w
WHERE s.metadata->>'workspace_id' = w.id::text;

COMMENT ON COLUMN secrets.management_workspace_id IS
  'Workspace whose owner/admin may disable this secret; does not restrict shared use';

-- +goose Down

ALTER TABLE secrets DROP COLUMN management_workspace_id;
