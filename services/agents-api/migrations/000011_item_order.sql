-- +goose Up
ALTER TABLE session_items ADD COLUMN output_index integer CHECK (output_index >= 0);

WITH ordered AS (
    SELECT id,
        (row_number() OVER (PARTITION BY session_id ORDER BY created_at, position, id) - 1)::integer AS item_position,
        CASE WHEN payload->>'role' IS DISTINCT FROM 'user' THEN
            (count(*) FILTER (WHERE payload->>'role' IS DISTINCT FROM 'user') OVER (
                PARTITION BY turn_id ORDER BY created_at, position, id ROWS UNBOUNDED PRECEDING
            ) - 1)::integer
        END AS output_index
    FROM session_items
)
UPDATE session_items i SET position = ordered.item_position, output_index = ordered.output_index
FROM ordered WHERE i.id = ordered.id;

CREATE UNIQUE INDEX session_items_position_idx ON session_items(session_id, position);
CREATE UNIQUE INDEX session_items_output_idx ON session_items(turn_id, output_index);

-- +goose Down
DROP INDEX session_items_output_idx;
DROP INDEX session_items_position_idx;
ALTER TABLE session_items DROP COLUMN output_index;
