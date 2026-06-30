# Capability 存储可插拔后端设计

- **日期**: 2026-06-30
- **状态**: 已批准,待实现
- **背景**: 开源版要降低接入成本,OSS(Aliyun)是额外的部署负担。希望开源版默认零外部依赖(仅 Postgres),托管/商业版仍可配 OSS。

## 1. 现状

`capability_version` 表(`server/migrations/000001_init.sql:666-683`)中,capability 的元数据**早已存在 Postgres**:

- `content`、`source_payload`、`canonical_spec` 均为 `jsonb`,已在 PG。
- 真正依赖 OSS 的**只有 plugin/skill 类型的 zip 二进制**,通过 `oss_key` + `sha256` 引用(`init.sql:702-703`:mcp/skill 这两列为空)。

数据流现状:

- **上传**:浏览器走 presigned PUT **直传 OSS**(`server/internal/dev/uploads_routes.go:120`)。
- **下载**:daemon 走 presigned GET **直连 OSS**(`server/internal/connector/agentdaemon/capability_runtime.go:485` 和 `:919`)。
- **服务端处理**:import 流程用 `ossClient.Download(...)` 把 zip 拉进进程(`server/internal/dev/capability_import_routes.go:169/267/468/515`)。
- 对象大小上限 64 MiB(`server/internal/storage/oss/client.go:206`,`MaxDownloadBytes`)。

关键观察:调用方**已经**依赖各自定义的窄接口(`uploads_routes.go:19-22` 的 `PresignPut/PresignGet`;`capability_runtime.go:35` 的 `PresignGet`),OSS 具体类型在调用点已解耦。引入可插拔后端不需要大改调用方。

因此"高度依赖 OSS"本质上只是 **plugin/skill zip 二进制这一块**。本设计把它抽象成可插拔后端。

## 2. 目标与范围

- 将 zip 二进制存储抽象为可插拔 `BlobStore` 后端。
- 开源版默认 **PG 零依赖**;托管版仍可配 OSS。
- **不改动** mcp/skill/system_prompt 元数据路径(`content`/`canonical_spec`/`source_payload` 早已在 PG)。
- 单个对象上限维持 **64 MiB**。

### 明确不做(YAGNI)

- 不做本地 FS / MinIO 后端。
- 不支持 >64 MiB 的流式大对象。
- 不做 CDN、不做内容去重。

## 3. 架构总览

一个 `BlobStore` 接口 + 两个实现:

- `ossStore`:包装现有 `oss.Client`,行为不变(presigned 直传/直连保留)。
- `pgStore`:字节读写新表 `capability_blob`;客户端经 API 代理端点收发。

`server/cmd/server/main.go:176` 按配置 `PARSAR_BLOB_BACKEND` 注入实现。调用方继续依赖各自的窄接口,仅底层实现可换。

## 4. 数据模型

`capability_version` 改动:

- `oss_key` → **改名为 `storage_ref` (text)**:后端无关的引用 key。(已确认:直接改名,sqlc 重生成,存量 OSS 行的值不变、语义兼容。)
- 新增 `size_bytes (bigint)`:用于校验与 64 MiB 上限判断。

新表(仅 PG 后端写):

```sql
CREATE TABLE IF NOT EXISTS capability_blob (
  storage_ref text        PRIMARY KEY,
  bytes       bytea       NOT NULL,
  sha256      text        NOT NULL,
  size_bytes  bigint      NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE capability_blob ALTER COLUMN bytes SET STORAGE EXTERNAL; -- zip 已压缩,禁止 PG 二次压缩,省 CPU
```

引用语义:

- **OSS 后端**:`storage_ref` = OSS object key;字节在 OSS,`capability_blob` 不使用。
- **PG 后端**:`storage_ref` = `pg:<uuid>`;字节在 `capability_blob`。

### 为何单独建表(而非在 capability_version 加 bytea 列)

PG 的 TOAST 机制会把 64 MiB 的 `bytea` 行外存储,主堆只留 ~18 字节指针,且惰性解 TOAST——纯性能上 co-locate 与单独表几乎无差别。选择单独表是出于工程卫生:

- **避免 `SELECT *` 地雷**:热路径查询 `GetEnabledCapabilitiesForAgent`(`server/internal/db/queries/store.sql:3890`)用显式列清单且已加载 `cv.content`/`cv.canonical_spec`;若 blob 是该表列,未来任何 `SELECT *` 或新 ORM 映射都会把 64 MiB 拖进最热的表。
- **备份解耦**:`pg_dump --exclude-table=capability_blob` 可秒导元数据。
- **可插拔干净**:blob 若是 `capability_version` 的列,schema 会长出后端相关列(OSS 用 `storage_ref`、PG 用 bytea),schema 与后端选择耦合。单独表让 `capability_version` 在两种后端下结构一致。
- **维护解耦**:冷的大 blob 与热元数据的 autovacuum/TOAST 参数可分别调优。

## 5. 接口契约(统一 URL 契约)

```go
type URLSpec struct {
    URL     string
    Method  string            // "PUT" / "GET"
    Headers map[string]string // OSS: Content-Type=application/octet-stream;PG: 含签名 token
    Expires time.Time
}

type BlobStore interface {
    // 服务端 IO(import 处理 + PG 代理端点用)
    Put(ctx context.Context, ref string, r io.Reader, size int64, sha256 string) error
    Download(ctx context.Context, ref string) ([]byte, error)
    Stat(ctx context.Context, ref string) (size int64, sha256 string, err error)
    Delete(ctx context.Context, ref string) error

    // 客户端 URL 契约
    UploadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error)
    DownloadURL(ctx context.Context, ref string, ttl time.Duration) (URLSpec, error)
}
```

- **OSS 实现**:`UploadURL`/`DownloadURL` 返回 presigned PUT/GET(沿用现有 `PresignPut`/`PresignGet`);`Download` 沿用现有实现。
- **PG 实现**:`UploadURL`/`DownloadURL` 返回 API 代理端点 URL + HMAC 短期签名 token;`Put`/`Download` 直接读写 `capability_blob`。

## 6. 数据流

**上传(浏览器)** — 两种后端三步一致:

1. 前端向 API 请求 `UploadURL`,拿到 `{url, method, headers}`。
2. 按返回值 `PUT` 字节到该 URL。OSS = 直传 OSS;PG = 打到 `PUT /internal/blobs/<ref>?token=...`,字节流进 `capability_blob`。
3. 用 `{ref, sha256, size}` 调创建版本接口。

**下载(daemon)**:

- `capability_runtime.go:485` 与 `:919` 把 `c.oss.PresignGet(...)` 换成 `blobStore.DownloadURL(...)`,daemon 照旧 `GET url`。OSS = 直连;PG = 打到 API 代理端点。

**服务端 import 处理**:

- `capability_import_routes.go` 的 `Download(...)` 调用对两种后端通用。

**新增代理端点**:

- `PUT/GET /internal/blobs/{ref}`,用 HMAC 短期签名 token(= presigned URL 的等价物,免 session)。仅 PG 后端发放此类 URL。

## 7. 改动点清单

| 文件 | 改动 |
|------|------|
| `server/internal/storage/blob/`(新包) | 定义 `BlobStore`、`URLSpec`、`ossStore`(包装现有 `oss.Client`)、`pgStore` |
| PG blob store(新) | `Put/Download/Stat/Delete` 读写 `capability_blob` |
| blob 代理 handler(新) | `PUT/GET /internal/blobs/{ref}` + HMAC token 校验 |
| `server/migrations/000002_capability_blob_storage.sql`(新) | 改名 `oss_key→storage_ref`、加 `size_bytes`、建 `capability_blob` |
| `server/internal/db/queries/store.sql` + sqlc 重生成 | `oss_key`/`latest_oss_key` 等引用同步为 `storage_ref` |
| `server/internal/dev/uploads_routes.go:120/164` | `PresignPut/PresignGet` → `UploadURL/DownloadURL` |
| `server/internal/connector/agentdaemon/capability_runtime.go:485/919` | `PresignGet` → `DownloadURL` |
| `server/cmd/server/main.go:176` | 按 `PARSAR_BLOB_BACKEND` 注入实现 |
| 前端上传组件 | 使用 API 返回的 `{url, method, headers}`,不再硬编码 OSS content-type |

## 8. 错误处理与边界

- **sha256 校验**:服务端 `Put` 与 daemon 下载后均校验(沿用现有逻辑)。
- **64 MiB 上限**:代理端点 + `Put` 处强制拦截(PG 也要拦,避免超大对象冲爆 WAL)。
- **token 过期/篡改**:代理端点返回 401。
- **OSS 未配置**:不再是错误态,默认走 `pgStore`。
- **前提**:PG 后端下 daemon 必须能 HTTP 到达 API server(自托管同集群,成立)。

## 9. 配置与回滚

- `PARSAR_BLOB_BACKEND = pg | oss`,**默认 `pg`**(开源开箱即用)。已确认默认 `pg` 不影响现有部署。
- 无需数据迁移:OSS 老部署设 `oss` 后端,继续读存量 `storage_ref`(原 `oss_key` 值)。

## 10. 测试

- **接口一致性测试**:同一套用例跑 `ossStore`(fake/mock)与 `pgStore`。
- PG blob 往返(Put→Download 字节一致)。
- sha256 不匹配时拒绝。
- 超过 64 MiB 时拒绝。
- 代理端点 token 鉴权(有效/过期/篡改)。
- daemon 经代理端点完成下载。
- 迁移幂等(可重复执行)。

## 附:关键代码位置索引

- `capability_version` 表:`server/migrations/000001_init.sql:666-683`
- 热路径查询:`server/internal/db/queries/store.sql:3890`(`GetEnabledCapabilitiesForAgent`)
- OSS client:`server/internal/storage/oss/client.go`(`PresignPut:114`、`PresignGet:136`、`Download`、`MaxDownloadBytes:206`)
- 上传路由:`server/internal/dev/uploads_routes.go:120/164`
- daemon 下载:`server/internal/connector/agentdaemon/capability_runtime.go:485/919`
- import 处理:`server/internal/dev/capability_import_routes.go:169/267/468/515`
- 后端注入点:`server/cmd/server/main.go:176`
