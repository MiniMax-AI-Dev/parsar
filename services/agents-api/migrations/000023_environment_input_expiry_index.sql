-- +goose Up
CREATE INDEX environment_input_reservations_pending_deadline_idx
    ON environment_input_reservations(deadline, id) WHERE state = 'pending';

-- +goose Down
DROP INDEX environment_input_reservations_pending_deadline_idx;
