-- +goose Up
ALTER TABLE sessions ADD COLUMN event_sequence bigint NOT NULL DEFAULT 0;
CREATE TABLE session_events (
    session_id uuid NOT NULL REFERENCES sessions(id),
    sequence bigint NOT NULL,
    payload jsonb NOT NULL,
    payload_bytes integer GENERATED ALWAYS AS (octet_length(payload::text)) STORED,
    PRIMARY KEY (session_id, sequence)
);

-- +goose Down
DROP TABLE session_events;
ALTER TABLE sessions DROP COLUMN event_sequence;
