-- +goose Up
ALTER TABLE vault_credentials ADD COLUMN status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'archived'));
CREATE INDEX vault_credentials_vault_created_id_idx ON vault_credentials (vault_id, created_at, id);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    LOCK TABLE vault_credentials IN ACCESS EXCLUSIVE MODE;
    IF EXISTS (SELECT 1 FROM vault_credentials WHERE status = 'archived') THEN
        RAISE EXCEPTION 'Cannot remove archived Credential classification';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX vault_credentials_vault_created_id_idx;
ALTER TABLE vault_credentials DROP COLUMN status;
