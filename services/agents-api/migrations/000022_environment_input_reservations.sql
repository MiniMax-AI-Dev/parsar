-- +goose Up
CREATE TABLE environment_input_reservations (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES environments(session_id),
    idempotency_key text NOT NULL,
    batch jsonb NOT NULL CHECK (CASE WHEN jsonb_typeof(batch) = 'array'
        THEN jsonb_array_length(batch) BETWEEN 1 AND 64 ELSE false END),
    state text NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'admitted', 'expired', 'cancelled')),
    created_at timestamptz NOT NULL,
    deadline timestamptz NOT NULL,
    settled_at timestamptz,
    UNIQUE (session_id, idempotency_key),
    CHECK ((settled_at IS NOT NULL) = (state <> 'pending'))
);
CREATE UNIQUE INDEX environment_input_reservations_one_pending_idx
    ON environment_input_reservations(session_id) WHERE state = 'pending';

-- +goose Down
LOCK TABLE environment_input_reservations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_input_reservations) THEN
        RAISE EXCEPTION 'Cannot remove durable Environment input identities while reservations exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE environment_input_reservations;
