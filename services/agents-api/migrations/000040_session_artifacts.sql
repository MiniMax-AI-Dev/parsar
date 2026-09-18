-- +goose Up
CREATE TABLE session_artifacts (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    turn_id uuid NOT NULL,
    environment_id uuid NOT NULL REFERENCES environments(id),
    path text NOT NULL CHECK (octet_length(path) BETWEEN 20 AND 4110),
    size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 0 AND 209715200),
    body_oid oid NOT NULL UNIQUE,
    sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    created_at timestamptz,
    UNIQUE (turn_id, path),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id)
);
CREATE INDEX session_artifacts_page_idx ON session_artifacts (session_id, created_at DESC, id DESC)
WHERE created_at IS NOT NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
    IF EXISTS (SELECT 1 FROM session_artifacts) THEN
        RAISE EXCEPTION 'delete session artifacts through the service before downgrade';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE session_artifacts;
