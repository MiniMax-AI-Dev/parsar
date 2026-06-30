-- +goose Up
-- ==============================================================
-- 000003_capability_blob_storage — capability 二进制(plugin/skill zip)
-- 的 Postgres 存储后端。
-- ==============================================================
-- 背景:
--   开源版希望零外部依赖(仅 Postgres)即可运行,不必接入阿里云 OSS。
--   capability 的 zip 体积 ≤ 64 MiB,放进 PG 的 bytea 完全可行。
--   本表只在 PARSAR_BLOB_BACKEND=pg(默认)时写入;backend=oss 时不碰。
--
-- 设计要点:
--   * storage_ref 即 capability_version.oss_key 里存的那个不透明引用值
--     (PG 后端形如 "pg:<uuid>");两边一致,下载时无需额外侧表。
--   * workspace_id 复刻 OSS key 形状自带的归属校验:PG 后端靠这一列
--     实现 BelongsToWorkspace 的跨租户闸门。
--   * 64 MiB 上限与 sha256 校验都在应用层(proxy PUT / PutBytes)做,
--     这里不加 DB 约束。
--   * capability_version 本表保持完全不动 —— 整个 schema 变更就这一张
--     新增表(additive),热路径与 canonical/daemon 契约零影响。
CREATE TABLE IF NOT EXISTS capability_blob (
  storage_ref  text        PRIMARY KEY,
  workspace_id text        NOT NULL,
  bytes        bytea       NOT NULL,
  sha256       text        NOT NULL,
  size_bytes   bigint      NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT NOW()
);

-- zip 已是压缩数据,EXTERNAL 关掉 PG 的 TOAST 行内压缩:对不可压缩字节
-- 没有体积收益,只白费 CPU。仍走 out-of-line TOAST 存大对象。
ALTER TABLE capability_blob ALTER COLUMN bytes SET STORAGE EXTERNAL;

COMMENT ON TABLE  capability_blob IS 'capability plugin/skill zip 的 PG 存储后端(PARSAR_BLOB_BACKEND=pg 时启用)';
COMMENT ON COLUMN capability_blob.storage_ref  IS '不透明存储引用 = capability_version.oss_key 的值(PG 后端形如 pg:<uuid>)';
COMMENT ON COLUMN capability_blob.workspace_id IS '归属 workspace;PG 后端据此做跨租户归属校验';
COMMENT ON COLUMN capability_blob.bytes        IS 'zip 原始字节(≤ 64 MiB,上限在应用层强制)';
COMMENT ON COLUMN capability_blob.sha256       IS 'zip 的 SHA-256 摘要(64 字符 hex),写入时计算';
COMMENT ON COLUMN capability_blob.size_bytes   IS 'zip 字节数;Stat 用';
COMMENT ON COLUMN capability_blob.created_at   IS '写入时间';

-- +goose Down
DROP TABLE IF EXISTS capability_blob;
