-- +goose Up
ALTER TABLE environment_templates
    ADD COLUMN packages jsonb NOT NULL DEFAULT '{"npm":[],"python":[],"system":[]}'::jsonb CHECK (jsonb_typeof(packages) = 'object'),
    ADD COLUMN env_contents bytea,
    ADD COLUMN setup_contents bytea;
CREATE TABLE environment_setups (
    session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    contents bytea NOT NULL
);

-- +goose Down
DROP TABLE environment_setups;
ALTER TABLE environment_templates DROP COLUMN packages, DROP COLUMN env_contents, DROP COLUMN setup_contents;
