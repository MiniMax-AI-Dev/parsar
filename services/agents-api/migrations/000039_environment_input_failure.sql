-- +goose Up
ALTER TABLE environment_input_reservations DROP CONSTRAINT environment_input_reservations_state_check;
ALTER TABLE environment_input_reservations ADD CONSTRAINT environment_input_reservations_state_check
    CHECK (state IN ('pending', 'admitted', 'expired', 'cancelled', 'failed'));

-- +goose Down
-- The constraint refuses rollback while failed outcomes still need to be retained.
ALTER TABLE environment_input_reservations DROP CONSTRAINT environment_input_reservations_state_check;
ALTER TABLE environment_input_reservations ADD CONSTRAINT environment_input_reservations_state_check
    CHECK (state IN ('pending', 'admitted', 'expired', 'cancelled'));
