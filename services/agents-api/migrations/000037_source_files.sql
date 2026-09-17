-- +goose Up
CREATE TABLE source_files (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    filename text NOT NULL CHECK (octet_length(filename) BETWEEN 1 AND 1024),
    purpose text NOT NULL CHECK (purpose = 'user_data'),
    body_oid oid NOT NULL UNIQUE,
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 536870912),
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT statement_timestamp()
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM source_files) THEN
        RAISE EXCEPTION 'delete source files through the service before downgrade';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE source_files;
