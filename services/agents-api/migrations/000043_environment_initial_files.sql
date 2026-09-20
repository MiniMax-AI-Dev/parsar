-- +goose Up
ALTER TABLE environment_templates ADD COLUMN files jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(files) = 'array');
ALTER TABLE environment_templates ADD COLUMN file_contents bytea;
CREATE TABLE initial_environment_files (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position BETWEEN 0 AND 49),
    path text NOT NULL,
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 52428800),
    contents bytea NOT NULL,
    UNIQUE (session_id, position),
    UNIQUE (session_id, path)
);
ALTER TABLE runtime_allocations ADD COLUMN initialization text NOT NULL DEFAULT 'complete'
    CHECK (initialization IN ('pending', 'running', 'complete'));

-- +goose Down
ALTER TABLE runtime_allocations DROP COLUMN initialization;
DROP TABLE initial_environment_files;
ALTER TABLE environment_templates DROP COLUMN file_contents, DROP COLUMN files;
