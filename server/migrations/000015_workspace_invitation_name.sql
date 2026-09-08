-- +goose Up
ALTER TABLE workspace_invitations ADD COLUMN name text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE workspace_invitations DROP COLUMN name;
