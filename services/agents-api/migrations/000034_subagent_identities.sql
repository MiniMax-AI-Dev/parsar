-- +goose Up
CREATE TABLE subagent_identities (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    device_id uuid NOT NULL REFERENCES devices(id),
    engine text NOT NULL,
    native_id text NOT NULL CHECK (native_id <> ''),
    parent_native_id text NOT NULL CHECK (parent_native_id <> '' AND parent_native_id <> native_id),
    native_created_at bigint NOT NULL CHECK (native_created_at > 0),
    first_turn_id uuid NOT NULL,
    first_event_ordinal integer NOT NULL,
    UNIQUE (device_id, engine, native_id),
    UNIQUE (session_id, native_id),
    FOREIGN KEY (session_id, first_turn_id) REFERENCES turns(session_id, id),
    FOREIGN KEY (first_turn_id, first_event_ordinal) REFERENCES turn_events(turn_id, ordinal)
);

-- +goose Down
DROP TABLE subagent_identities;
