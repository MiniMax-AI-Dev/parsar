-- +goose Up
CREATE TABLE function_calls (
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    call_id text NOT NULL,
    executor_call_id text NOT NULL,
    name text NOT NULL,
    arguments jsonb NOT NULL,
    result jsonb,
    applied boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (session_id, turn_id, call_id),
    UNIQUE (session_id, turn_id, executor_call_id),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id),
    CHECK (result IS NULL OR jsonb_typeof(result) = 'object'),
    CHECK (NOT applied OR result IS NOT NULL)
);
CREATE INDEX function_calls_pending_idx ON function_calls(session_id, turn_id) WHERE NOT applied;

-- +goose Down
DROP TABLE function_calls;
