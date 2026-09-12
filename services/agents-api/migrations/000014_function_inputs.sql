-- +goose Up
ALTER TABLE turn_inputs DROP CONSTRAINT turn_inputs_kind_check;
ALTER TABLE turn_inputs ADD CONSTRAINT turn_inputs_kind_check
    CHECK (kind IN ('message', 'cancel', 'tool_result'));
ALTER TABLE turn_inputs ADD CONSTRAINT turn_inputs_function_turn_check
    CHECK (kind <> 'tool_result' OR turn_id IS NOT NULL);

-- +goose Down
-- Refuse a downgrade with saved function inputs rather than delete retry history.
ALTER TABLE turn_inputs DROP CONSTRAINT turn_inputs_kind_check;
ALTER TABLE turn_inputs ADD CONSTRAINT turn_inputs_kind_check
    CHECK (kind IN ('message', 'cancel'));
ALTER TABLE turn_inputs DROP CONSTRAINT turn_inputs_function_turn_check;
