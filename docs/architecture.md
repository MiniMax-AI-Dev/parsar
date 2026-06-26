# Parsar 架构基线

Parsar 是面向团队的开源 Agent 协作控制面。

```text
Parsar = 团队协作控制面 + Agent Connector 层
```

> 工程规则(worktree、强制检查、目录污染等)见 [`AGENTS.md`](../AGENTS.md);
> 这里只记录**架构边界**:协议、连接器、工具链选择。

## 技术栈

- Server: Go + Chi。
- Database: 只用 PostgreSQL。
- Web: Vite + React SPA。
- Agent runtime: `parsar-daemon`(Go),与用户设备或平台托管沙盒配对。
- API: OpenAPI-first(契约见 [`openapi/openapi.yaml`](openapi/openapi.yaml))。
- 部署:self-host 优先,默认 Docker Compose = Parsar + Postgres。
- DB 工具链:goose 管 migration,sqlc 从 checked-in SQL 生成 typed query,
  pgx / pgxpool 做运行时连接和执行。

Parsar 不在 core server 路径上使用 GORM 或其它 ORM。SQL 是 review 契约的一部分:
migrations 定义 schema、query 文件定义数据访问面、生成的 Go 代码让 call site 保持 typed。

成熟的外部能力能用就用 —— Parsar 拥有协作控制面、权限模型、Agent 编排记录、
连接器边界;不重造 migration engine、ORM、浏览器测试框架、运行时工具。

## Agent 执行接入路径

- **Agent Daemon Connector**(`connector_type=agent_daemon`)—— 当前 CLI Agent 的统一路径。
  `parsar-daemon` 内部 adapter 由 `project_agents.config.agent_kind` 选择
  (`opencode`、`claude_code`、未来的其它 kind)。
- **HTTP Agent Connector** —— 给自带 HTTP 接口、由 HTTP runner 认领的 Agent。

Agent Daemon 的 run 通过 streaming WebSocket 投递到**显式绑定**的 runtime
(`project_agents.runtime_id`);默认路径**不再**自动 Acquire sandbox 兜底。

## 协议边界

```text
Server ↔ parsar-daemon:
  Agent Daemon WebSocket(pairing → heartbeat → run dispatch)

Agent Connector 层:
  Agent Daemon Connector  (opencode / claude_code / 未来 adapter)
  HTTP Agent Connector
  ACP Connector(后续)
  A2A Connector(后续)

Agent Runtime ↔ 工具:
  MCP
```

## DB 工具链边界

- `goose` 管 migration 顺序和 schema 版本号。`server/cmd/migrate` 只是 goose 的薄包装,
  让脚本保持一条稳定命令。
- `sqlc` 把 `server/internal/db/queries/` 下 checked-in SQL 编译成
  `server/internal/db/sqlc/` 下的 typed Go 方法。
- `pgx` / `pgxpool` 是应用的连接和执行边界。`database/sql` 只在 goose 包装边界出现,
  因为 goose 走 `*sql.DB`。

## 产品验证边界

产品 E2E 用 **Playwright** 作为核心质量门 —— 它从用户视角验证 Parsar 自己的
web / API 行为。Agent runtime 内的浏览器自动化是另一回事:`browser-use` 之类的工具
可以后续评估作为 Agent 的浏览器能力,但**不**属于当前核心质量门。
