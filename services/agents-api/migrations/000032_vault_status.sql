-- +goose Up
ALTER TABLE vaults ADD COLUMN status text NOT NULL DEFAULT 'active'
    CHECK (status IN ('active', 'archived'));
CREATE INDEX vaults_tenant_created_id_idx ON vaults (tenant_id, created_at, id);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM vaults WHERE status = 'archived') THEN
        RAISE EXCEPTION 'Cannot remove archived Vault classification';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX vaults_tenant_created_id_idx;
ALTER TABLE vaults DROP COLUMN status;
