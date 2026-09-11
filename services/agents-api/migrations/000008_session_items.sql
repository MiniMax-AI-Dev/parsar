-- +goose Up
ALTER TABLE turns ADD COLUMN items_indexed boolean NOT NULL DEFAULT false;
ALTER TABLE turns ALTER COLUMN items_indexed SET DEFAULT true;

CREATE TABLE session_items (
    id uuid PRIMARY KEY,
    session_id uuid NOT NULL,
    turn_id uuid NOT NULL,
    created_at timestamptz NOT NULL,
    position integer NOT NULL DEFAULT 0,
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    FOREIGN KEY (session_id, turn_id) REFERENCES turns(session_id, id)
);
CREATE INDEX session_items_page_idx ON session_items(session_id, created_at, position, id);
CREATE INDEX session_items_turn_idx ON session_items(turn_id);

CREATE INDEX turns_unindexed_items_idx ON turns(session_id, created_at, id) WHERE NOT items_indexed;
CREATE INDEX turn_inputs_item_history_idx ON turn_inputs(turn_id, created_at, sequence) WHERE kind = 'message';
CREATE INDEX turn_events_item_history_idx ON turn_events(turn_id, created_at, ordinal);

-- +goose Down
DROP INDEX turn_events_item_history_idx;
DROP INDEX turn_inputs_item_history_idx;
DROP INDEX turns_unindexed_items_idx;
DROP TABLE session_items;
ALTER TABLE turns DROP COLUMN items_indexed;
