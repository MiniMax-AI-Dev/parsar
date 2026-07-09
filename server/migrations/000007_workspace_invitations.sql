-- +goose Up
CREATE TABLE IF NOT EXISTS workspace_invitations (
  id           uuid PRIMARY KEY,
  token_hash   bytea NOT NULL,
  workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  email        text NOT NULL,
  role         text NOT NULL,
  invited_by   uuid NOT NULL REFERENCES users(id),
  expires_at   timestamptz NOT NULL,
  accepted_at  timestamptz,
  revoked_at   timestamptz,
  created_at   timestamptz NOT NULL
);

CREATE UNIQUE INDEX uk_workspace_invitations_token_hash
  ON workspace_invitations(token_hash);

CREATE INDEX idx_workspace_invitations_pending
  ON workspace_invitations(workspace_id)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;

CREATE UNIQUE INDEX uk_workspace_invitations_pending_email
  ON workspace_invitations(workspace_id, email)
  WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS workspace_invitations;
