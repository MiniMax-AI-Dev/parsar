-- +goose Up
CREATE TABLE model_providers (
  id uuid PRIMARY KEY,
  workspace_id uuid NOT NULL REFERENCES workspaces(id),
  name text NOT NULL,
  protocol text NOT NULL CHECK (protocol IN ('anthropic', 'responses')),
  base_url text NOT NULL,
  encrypted_key bytea NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  UNIQUE (workspace_id, id)
);
ALTER TABLE models ADD COLUMN workspace_id uuid REFERENCES workspaces(id);
ALTER TABLE models ADD COLUMN provider_id uuid;
ALTER TABLE models ADD COLUMN context_window integer NOT NULL DEFAULT 0 CHECK (context_window >= 0);
ALTER TABLE models ADD COLUMN max_output_tokens integer NOT NULL DEFAULT 0 CHECK (max_output_tokens >= 0 AND max_output_tokens <= context_window);
ALTER TABLE models ADD CONSTRAINT fk_models_provider FOREIGN KEY (workspace_id, provider_id) REFERENCES model_providers(workspace_id, id);
ALTER TABLE models DROP CONSTRAINT chk_models_credential_mode;
ALTER TABLE models ADD CONSTRAINT chk_models_credential_mode CHECK (
  (credential_mode = 'inline_secret' AND credential_kind_code IS NULL AND workspace_id IS NULL AND provider_id IS NULL)
  OR (credential_mode = 'credential_ref' AND secret_id IS NULL AND credential_kind_code IS NOT NULL AND workspace_id IS NULL AND provider_id IS NULL)
  OR (credential_mode = 'core' AND workspace_id IS NOT NULL AND provider_id IS NOT NULL
      AND secret_id IS NULL AND credential_kind_code IS NULL AND provider_type = 'agents_api'
      AND adapter = 'agents_api' AND base_url = '' AND config = '{}'::jsonb)
);
CREATE UNIQUE INDEX uk_models_provider_key ON models(provider_id, model_key)
  WHERE credential_mode = 'core' AND deleted_at IS NULL;
ALTER TABLE product_core_sessions ADD COLUMN provider_snapshot bytea;

-- +goose Down
ALTER TABLE product_core_sessions DROP COLUMN provider_snapshot;
DELETE FROM models WHERE credential_mode = 'core';
DROP INDEX uk_models_provider_key;
ALTER TABLE models DROP CONSTRAINT chk_models_credential_mode;
ALTER TABLE models DROP CONSTRAINT fk_models_provider;
ALTER TABLE models DROP COLUMN max_output_tokens;
ALTER TABLE models DROP COLUMN context_window;
ALTER TABLE models DROP COLUMN provider_id;
ALTER TABLE models DROP COLUMN workspace_id;
DROP TABLE model_providers;
ALTER TABLE models ADD CONSTRAINT chk_models_credential_mode CHECK (
  (credential_mode = 'inline_secret' AND credential_kind_code IS NULL)
  OR (credential_mode = 'credential_ref' AND secret_id IS NULL AND credential_kind_code IS NOT NULL)
);
