-- blob.sql — capability plugin/skill zip storage for the Postgres
-- blob backend (migration 000002, PARSAR_BLOB_BACKEND=pg).
--
-- Conventions match the rest of this dir:
--   * @param::type cast on every parameter
--   * storage_ref is the opaque reference also held in
--     capability_version.oss_key; for the PG backend it looks like
--     "pg:<uuid>". The OSS backend never touches this table.
--   * The 64 MiB cap and sha256 are enforced in the Go blob layer
--     (PGStore.PutBytes), not in SQL — see internal/storage/blob.

-- name: InsertCapabilityBlob :exec
-- Upsert so a re-upload of the same storage_ref overwrites cleanly.
-- workspace_id is part of the update set so a corrected owner on
-- re-upload is reflected (the auth gate reads it back via meta).
insert into capability_blob (
  storage_ref, workspace_id, bytes, sha256, size_bytes
)
values (
  @storage_ref::text, @workspace_id::text, @bytes::bytea,
  @sha256::text, @size_bytes::bigint
)
on conflict (storage_ref) do update set
  bytes        = excluded.bytes,
  sha256       = excluded.sha256,
  size_bytes   = excluded.size_bytes,
  workspace_id = excluded.workspace_id;

-- name: GetCapabilityBlobBytes :one
-- Hot download path: the proxy GET streams these bytes back.
select bytes
from capability_blob
where storage_ref = @storage_ref::text;

-- name: GetCapabilityBlobMeta :one
-- Stat + cross-tenant gate: workspace_id backs BelongsToWorkspace,
-- size_bytes/sha256 back Stat without pulling the full bytea.
select sha256, size_bytes, workspace_id
from capability_blob
where storage_ref = @storage_ref::text;

-- name: DeleteCapabilityBlob :exec
-- Cleanup hook for capability deletion. Idempotent.
delete from capability_blob
where storage_ref = @storage_ref::text;
