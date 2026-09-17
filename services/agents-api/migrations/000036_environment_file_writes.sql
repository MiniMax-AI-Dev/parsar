-- +goose Up
CREATE TABLE environment_file_writes (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL REFERENCES environments(id),
    device_id uuid NOT NULL REFERENCES devices(id),
    request_sha256 text NOT NULL CHECK (request_sha256 ~ '^[0-9a-f]{64}$'),
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'committed', 'rejected')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    settled_at timestamptz,
    CHECK ((state = 'pending') = (settled_at IS NULL))
);
CREATE UNIQUE INDEX environment_file_writes_pending ON environment_file_writes(environment_id)
    WHERE state = 'pending';

-- +goose Down
LOCK TABLE environment_file_writes IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_file_writes) THEN
        RAISE EXCEPTION 'Cannot remove durable Environment file write identities while writes exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE environment_file_writes;
