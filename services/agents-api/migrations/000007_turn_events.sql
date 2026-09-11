-- +goose Up
ALTER TABLE turns ADD COLUMN event_count integer NOT NULL DEFAULT 0;
ALTER TABLE turns ADD COLUMN event_bytes bigint NOT NULL DEFAULT 0;

CREATE TABLE turn_events (
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    ordinal integer NOT NULL CHECK (ordinal > 0),
    kind text NOT NULL,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (turn_id, ordinal),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id)
);

-- +goose Down
DROP TABLE turn_events;
ALTER TABLE turns DROP COLUMN event_bytes;
ALTER TABLE turns DROP COLUMN event_count;
