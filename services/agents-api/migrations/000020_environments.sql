-- +goose Up
CREATE TABLE environments (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL UNIQUE REFERENCES sessions(id),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'connected', 'disconnected', 'expired', 'failed')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- +goose Down
LOCK TABLE environments IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environments) THEN
        RAISE EXCEPTION 'Cannot remove durable Environment identities while Environments exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE environments;
