-- +goose Up
ALTER TABLE sessions
    ADD COLUMN creator_kind text,
    ADD COLUMN creator_id text,
    ADD CONSTRAINT sessions_creator_complete CHECK (
        (creator_kind IS NULL AND creator_id IS NULL)
        OR (creator_kind IS NOT NULL AND creator_id IS NOT NULL
            AND creator_kind IN ('user', 'service_account') AND creator_id <> '')
    );

-- +goose Down
ALTER TABLE sessions
    DROP CONSTRAINT sessions_creator_complete,
    DROP COLUMN creator_kind,
    DROP COLUMN creator_id;
