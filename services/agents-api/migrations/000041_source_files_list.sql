-- +goose Up
CREATE INDEX source_files_page_idx ON source_files (tenant_id, created_at DESC, id DESC);

-- +goose Down
DROP INDEX source_files_page_idx;
