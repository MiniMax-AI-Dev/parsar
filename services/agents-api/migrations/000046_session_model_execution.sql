-- +goose Up
CREATE TABLE session_model_execution (
  session_id uuid PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  encrypted_config bytea NOT NULL
);

-- +goose Down
DROP TABLE session_model_execution;
