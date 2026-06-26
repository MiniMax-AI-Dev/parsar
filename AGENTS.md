# Parsar 开发协议

Parsar 是面向团队的开源 Agent 协作控制面。本文件只承载**与开发阶段无关的长期规则**，任何时候都适用。

## 强制规则

- 所有 runtime config、logs、state、cache 必须写到 `~/.parsar/`。
- 不允许在 repo 根目录或当前工作目录写 runtime state。
- 用户传入的 working directory 必须是绝对路径或 `~/` 开头；相对路径直接拒绝，不要 resolve 到 CWD。
- 任何 install/setup 行为下笔前先问一句：这一步会不会写到用户当前目录？

## Worktree 工作流

所有代码改动 —— feature、fix、refactor、涉及代码路径的文档改动 —— 都必须在从 `main` 切出来的 git worktree 里进行。**禁止直接在 `main` 上提交。**

`main` 是唯一集成基线：

- 新 worktree 必须从最新的 `origin/main` 切；开 worktree 前先 `git fetch origin main`。
- `main` 是 source of truth，每个 worktree 从它出发，也回到它。
- 实现 + 验证完成后，push feature 分支，开 PR 回 `main`。**合入 `main` 必须走 PR review** —— 不允许本地 fast-forward 或 local merge 绕过审查。
- 提 review 前跑 `make check`，以及与改动相关的 E2E target。
- worktree 放在 `.worktrees/<feature-name>/` 下，避免散落在 repo 根。

```bash
git fetch origin main
git worktree add .worktrees/feature-name -b feature/name origin/main
```

不允许直接在 `main` 上开发。每个 session 都遵守这条。

## 架构基线

- Server: Go + Chi。
- Database: 只用 PostgreSQL。
- DB 工具链: goose migrations + sqlc 生成 queries + pgx/pgxpool 运行时访问。
- Web: Vite + React SPA，后期由 Go server 直接 serve。
- API: OpenAPI-first。
- Connector MVP: Agent Daemon Connector（`connector_type=agent_daemon`，adapter 由 `project_agents.config.agent_kind` 决定 —— `opencode`、`claude_code` …）+ HTTP Agent Connector。
- Agent Daemon 的 run 投递给与 agent 绑定的 `parsar-daemon` runtime（`project_agents.runtime_id`）；daemon 内部 adapter 决定真正跑哪个 CLI。

## Web UI 硬规则

弹窗(Dialog / Drawer / Modal)和详情面板内**禁止出现横向滚动条**。原因是终端用户报告"看不到底"远多于"屏幕太窄",横滑控件几乎都是 bug 漏出来的脏布局,不是设计意图。

落地三条:

1. `DialogContent` 默认加 `overflow-x-hidden`,垂直方向用 `max-h-[calc(100vh-2rem)] overflow-y-auto`。
2. 任何 `<pre>` / `<code>` 块默认 `whitespace-pre-wrap break-all` —— 代码/JSON/命令行该换行就换行,不要让用户左右滑。例外:终端日志流(append-only)可保留 `overflow-x-auto`,但必须套在 `overflow-hidden` 的父容器里,不让横滚条冒到弹窗。
3. 错误 / 警告 banner 一律加 `break-all`,避免长 token、URL、堆栈撑爆容器。

弹窗内部布局用 grid 多列时,每个子列加 `min-w-0`,否则子内容会反向把 grid track 撑宽。

## 代码注释

默认不写注释。源码里只在 **WHY 非显然**时留一行:隐藏的约束、不变量、特定 bug 的 workaround、读者会意外的行为。其他情况一律不加。

- 不解释 WHAT —— 标识符自己讲清楚,删了不影响理解就别写。
- 不写大段 docstring / 多行 block comment。
- 不 stamp 当前任务、调用方、PR/MR、issue 号 —— 这些属于 commit message 和 PR 描述,留在代码里会随重构腐烂。
- Go 导出符号需要 doc comment 时保持一行,不要展开成段落。

## 必跑检查

报告完成前必须跑：

```bash
make check
```

- DB 改动必须配 migration。
- API 改动先更新 `docs/openapi/openapi.yaml`，再写实现。

## 报告语言

验证报告 / 交付报告默认用中文。
