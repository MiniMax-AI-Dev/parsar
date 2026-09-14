-- +goose Up
ALTER TABLE environment_executor_credentials
    ADD COLUMN key_id uuid,
    ADD COLUMN tenant_id uuid REFERENCES execution_project_scopes(tenant_id),
    ADD COLUMN subject_kind text,
    ADD COLUMN subject_id text,
    ADD COLUMN created_at timestamptz;

UPDATE environment_executor_credentials
SET key_id = environment_id, revoked_at = COALESCE(revoked_at, clock_timestamp());

ALTER TABLE environment_executor_credentials
    DROP CONSTRAINT environment_executor_credentials_pkey,
    ALTER COLUMN environment_id DROP NOT NULL,
    ALTER COLUMN key_id SET NOT NULL,
    ADD PRIMARY KEY (key_id),
    ADD CONSTRAINT executor_principal_complete CHECK (
        (tenant_id IS NULL AND subject_kind IS NULL AND subject_id IS NULL
            AND environment_id IS NOT NULL AND revoked_at IS NOT NULL AND created_at IS NULL)
        OR (tenant_id IS NOT NULL AND subject_kind IS NOT NULL AND subject_id IS NOT NULL
            AND subject_kind IN ('user', 'service_account') AND subject_id <> '' AND created_at IS NOT NULL)
    );

-- +goose Down
LOCK TABLE environment_executor_credentials IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_executor_credentials WHERE tenant_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot remove durable executor principal identities while principal keys exist';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE environment_executor_credentials
    DROP CONSTRAINT executor_principal_complete,
    DROP CONSTRAINT environment_executor_credentials_pkey,
    ALTER COLUMN environment_id SET NOT NULL,
    ADD PRIMARY KEY (environment_id),
    DROP COLUMN key_id,
    DROP COLUMN tenant_id,
    DROP COLUMN subject_kind,
    DROP COLUMN subject_id,
    DROP COLUMN created_at;
