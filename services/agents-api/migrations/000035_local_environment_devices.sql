-- +goose Up
ALTER TABLE devices ADD COLUMN environment_id uuid UNIQUE REFERENCES environments(id);

-- +goose Down
LOCK TABLE devices IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM devices WHERE environment_id IS NOT NULL) THEN
        RAISE EXCEPTION 'Cannot discard dedicated Environment device authority';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE devices DROP COLUMN environment_id;
