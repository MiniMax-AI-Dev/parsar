-- +goose Up
CREATE INDEX environment_input_reservations_latest_idx
ON environment_input_reservations(session_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX environment_input_reservations_latest_idx;
