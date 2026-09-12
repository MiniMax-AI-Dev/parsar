-- +goose Up
LOCK TABLE turns IN ACCESS EXCLUSIVE MODE;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM turns WHERE NOT items_indexed) THEN
        RAISE EXCEPTION 'Unindexed Agents API history: prepare all Session Items with release 906069e before upgrading; see services/agents-api/README.md';
    END IF;
END
$$;
-- +goose StatementEnd
ALTER TABLE turns DROP COLUMN items_indexed;

-- +goose Down
ALTER TABLE turns ADD COLUMN items_indexed boolean NOT NULL DEFAULT true;
CREATE INDEX turns_unindexed_items_idx ON turns(session_id, created_at, id) WHERE NOT items_indexed;
