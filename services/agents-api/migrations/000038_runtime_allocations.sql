-- +goose Up
CREATE TABLE runtime_allocations (
    id uuid PRIMARY KEY,
    environment_id uuid NOT NULL UNIQUE REFERENCES environments(id),
    device_id uuid NOT NULL UNIQUE REFERENCES devices(id),
    provider_key uuid NOT NULL,
    state text NOT NULL DEFAULT 'creating'
        CHECK (state IN ('creating', 'running', 'cleanup_pending', 'released')),
    create_settled boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    kept_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    released_at timestamptz,
    CHECK ((state = 'released') = (released_at IS NOT NULL)),
    CHECK (state <> 'released' OR create_settled)
);

-- +goose Down
LOCK TABLE runtime_allocations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM runtime_allocations) THEN
        RAISE EXCEPTION 'Cannot discard managed Runtime allocation and cleanup identities';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE runtime_allocations;
