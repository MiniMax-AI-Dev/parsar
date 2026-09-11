-- +goose Up
CREATE TABLE turns (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'in_progress', 'waiting', 'completed', 'failed', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    completed_at timestamptz,
    cancel_requested_at timestamptz,
    outcome jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(outcome) = 'object'),
    UNIQUE (session_id, id),
    CHECK ((completed_at IS NOT NULL) = (status IN ('completed', 'failed', 'cancelled')))
);
CREATE UNIQUE INDEX turns_one_active_idx ON turns(session_id)
    WHERE status IN ('queued', 'in_progress', 'waiting');

-- Inputs also retain retry identity after their target Turn has ended.
CREATE TABLE turn_inputs (
    sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    session_id uuid NOT NULL REFERENCES sessions(id),
    turn_id uuid,
    idempotency_key text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('message', 'cancel')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (session_id, idempotency_key),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    CHECK (kind <> 'message' OR turn_id IS NOT NULL)
);
CREATE INDEX turn_inputs_turn_sequence_idx ON turn_inputs(turn_id, sequence);

-- +goose Down
DROP TABLE turn_inputs;
DROP TABLE turns;
