-- +goose Up
-- ==============================================================
-- 000003_capability_blob_storage — Postgres storage backend for capability
-- binaries (plugin/skill zip).
-- ==============================================================
-- Background:
--   The open-source build wants to run with zero external dependencies
--   (Postgres only), without wiring up Aliyun OSS. Capability zips are
--   <= 64 MiB, so storing them in PG bytea is perfectly viable. This
--   table is written only when PARSAR_BLOB_BACKEND=pg (default); when
--   backend=oss it is untouched.
--
-- Design points:
--   * storage_ref is the opaque reference value stored in
--     capability_version.oss_key (for the PG backend, shaped like
--     "pg:<uuid>"); the two sides agree, so downloads need no extra
--     side table.
--   * workspace_id mirrors the tenancy check baked into the OSS key
--     shape: on the PG backend this column powers BelongsToWorkspace's
--     cross-tenant gate.
--   * The 64 MiB cap and sha256 verification live in the application
--     layer (proxy PUT / PutBytes); no DB constraint is added here.
--   * capability_version is untouched in this migration -- the entire
--     schema change is this single additive new table, with zero impact
--     on the hot path or the canonical/daemon contract.
CREATE TABLE IF NOT EXISTS capability_blob (
  storage_ref  text        PRIMARY KEY,
  workspace_id text        NOT NULL,
  bytes        bytea       NOT NULL,
  sha256       text        NOT NULL,
  size_bytes   bigint      NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT NOW()
);

-- zip payloads are already compressed; EXTERNAL disables PG's inline TOAST
-- compression: no size win on incompressible bytes, only wasted CPU. Large
-- objects still go out-of-line via TOAST.
ALTER TABLE capability_blob ALTER COLUMN bytes SET STORAGE EXTERNAL;

COMMENT ON TABLE  capability_blob IS 'PG storage backend for capability plugin/skill zips (enabled when PARSAR_BLOB_BACKEND=pg)';
COMMENT ON COLUMN capability_blob.storage_ref  IS 'Opaque storage reference = value of capability_version.oss_key (PG backend shape: pg:<uuid>)';
COMMENT ON COLUMN capability_blob.workspace_id IS 'Owning workspace; PG backend uses this for cross-tenant ownership checks';
COMMENT ON COLUMN capability_blob.bytes        IS 'Raw zip bytes (<= 64 MiB, upper bound enforced in the application layer)';
COMMENT ON COLUMN capability_blob.sha256       IS 'SHA-256 digest of the zip (64-char hex), computed on write';
COMMENT ON COLUMN capability_blob.size_bytes   IS 'Zip byte count; used by Stat';
COMMENT ON COLUMN capability_blob.created_at   IS 'Write time';

-- +goose Down
DROP TABLE IF EXISTS capability_blob;
