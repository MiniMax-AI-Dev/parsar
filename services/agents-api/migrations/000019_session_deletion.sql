-- +goose Up
ALTER TABLE sessions ADD COLUMN deleted_at timestamptz;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM sessions WHERE deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot remove Session deletion markers while deleted Sessions exist';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE sessions DROP COLUMN deleted_at;
