-- +goose Up
-- Existing single-event requests retain their retry identity at position zero.
ALTER TABLE turn_inputs ADD COLUMN batch_position integer NOT NULL DEFAULT 0
    CHECK (batch_position >= 0);
ALTER TABLE turn_inputs DROP CONSTRAINT turn_inputs_session_id_idempotency_key_key;
ALTER TABLE turn_inputs ADD UNIQUE (session_id, idempotency_key, batch_position);

-- +goose Down
-- A multi-event request cannot be represented by the previous schema without
-- losing inputs or retry identities. Let the uniqueness check stop such a downgrade.
ALTER TABLE turn_inputs ADD UNIQUE (session_id, idempotency_key);
ALTER TABLE turn_inputs DROP COLUMN batch_position;
