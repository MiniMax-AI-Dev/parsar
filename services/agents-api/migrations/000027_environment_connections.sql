-- +goose Up
CREATE TABLE environment_connections (
    environment_id uuid PRIMARY KEY REFERENCES environments(id),
    generation uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0)
);

-- +goose Down
LOCK TABLE environment_connections IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM environment_connections) THEN
        RAISE EXCEPTION 'Cannot remove active Environment observation fencing';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE environment_connections;
