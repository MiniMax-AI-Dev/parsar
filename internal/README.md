# `internal/` — Go 跨子树共享包

> Go 的 internal-package 可见性规则：`server/internal/...` 下的包只能被
> `server/` 子树内的代码 import。当一个包需要被 `server/`、`apps/parsar-daemon/`、
> `apps/parsar/` 这些**并列子树**共享时，必须放在仓库根的 `internal/` 才符合
> Go 编译器的可见性要求。

本目录的现有子包：

| 子目录 | 共享对象 | 内容 |
|---|---|---|
| `agentdaemon/proto/` | `server/` + `apps/parsar-daemon/` | server 与 daemon 二进制之间的线缆 schema（消息格式、版本协商） |
| `obs/` | server + 各 daemon / CLI | 共享的可观测性辅助（结构化日志字段、trace helper） |
| `runtimecrypto/` | server + parsar-daemon | runtime 信封加解密（沙盒侧解密 server 下发的临时凭证） |

---

## 何时把代码放这里

唯一判据：**是否需要被多个并列子树 import**。

- 只被 `server/` 用 → 放 `server/internal/...`
- 只被 `apps/parsar-daemon/` 用 → 放 `apps/parsar-daemon/internal/...`
- 同时被两边（或更多）用 → 放仓库根 `internal/`，本目录就是它的归宿

---

## 实现约束

- 子包以 Go package 形式存在，不依赖任何业务层包（避免循环依赖）。
- API 表面尽量小，只暴露跨子树确实需要的类型。
- 单元测试与子包同目录。`make check` 会一起跑。
- 若发现某个子包只被一个子树使用，应迁回该子树下的 `internal/`。

---

## 扩展性

如果将来需要为部署引入新的 connector / gateway / capability / audit sink
实现，并且它们需要被多个 binary 共享，可以在本目录下按用途分子目录
（如 `internal/connectors/<name>/`）。但优先考虑通过 server 已有的
extension interface 在 `server/internal/...` 内完成 —— 仅当确实需要跨子树
共享代码时才放本目录。
