-- +goose Up
ALTER TABLE environment_input_reservations
ADD COLUMN is_initial boolean NOT NULL DEFAULT false;

-- +goose Down
LOCK TABLE environment_input_reservations IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_input_reservations WHERE is_initial) THEN
        RAISE EXCEPTION 'Cannot remove initial Environment input origin while initial reservations exist';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE environment_input_reservations DROP COLUMN is_initial;
