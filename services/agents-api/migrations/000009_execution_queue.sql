-- +goose Up
CREATE INDEX turns_execution_queue_idx ON turns (status, id)
WHERE status IN ('queued', 'in_progress', 'waiting');

-- +goose Down
DROP INDEX turns_execution_queue_idx;
