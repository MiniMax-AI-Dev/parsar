# Spec & Memory 模块方案

## Context

Parsar 当前缺一套"把工程约定 / 用户偏好 / 项目背景注入到每次 agent 对话"的中心化机制。
现状是每次对话都要靠用户在 chat 里现场告诉 agent "我们项目用 Go + gin"、"别再用 Promise.all" 这类规则,导致:

1. **重复劳动** — 不同 sandbox / 不同 session / 不同 agent 之间无法共享上下文
2. **规则漂移** — 用户口头给的约定 agent 不记得,下一轮就忘
3. **冷启动昂贵** — 新建一个 project_agent,前几轮总在喂背景

类似 Claude Code 的 auto-memory 和外部产品(如 Trellis)已经证明"把 spec/memory 持久化 + 自动注入"是有效的产品形态。
本方案在 parsar 上自研一套同等能力,**不引入外部代码**(规避 AGPL + 避免抄袭嫌疑),**MVP 聚焦 spec + memory 两类内容**,**支持 claude code / opencode / codex 三平台**。

预期结果:用户在 UI 里维护一份 workspace 级 spec 和 user/project 级 memory,所有 sandbox 自动注入,
agent 主动写回新发现的规则,跨 sandbox 自然共享。

---

## 设计决策汇总(已与用户对齐 22 条)

| # | 决策 | 选择 |
|---|------|------|
| 1 | 数据权威源 | DB-first(server 唯一权威,sandbox 只是消费者 + 写回客户端) |
| 2 | MVP 范围 | 仅 spec + memory(暂不做 tasks / workflow) |
| 3 | MVP 平台 | claude code / opencode / codex |
| 4 | spec 粒度 | 多 fragment(扁平 title+body+tags,非文件树) |
| 5 | spec scope | workspace 级 |
| 6 | memory scope | user + project 级,两种独立 |
| 7 | memory 4 类 | user / feedback / project / reference |
| 8 | 注入策略 | 全量(MVP),后续可演进按 tag 智能注入 |
| 9 | 注入时机 | SessionStart 全量 + per-turn 增量 |
| 10 | per-turn 含义 | session 内启动后他人新写的(增量,非重复全量) |
| 11 | Codex 平台 | 降级为只 SessionStart(无 per-turn hook) |
| 12 | 写回方式 | sandbox 内 `parsar` CLI |
| 13 | memory 写入触发 | Agent 自觉(system prompt 元指令 + agent 调 `parsar memory add`) |
| 14 | 审批模式 | 直接写 + 审计(无 approval 流) |
| 15 | CLI 鉴权 | 复用 runtime pairing → runner_credential(扩展现有机制) |
| 16 | 预置内容 | 只预置 hook 脚本 + `parsar` CLI,不预置 CLAUDE.md / AGENTS.md |
| 17 | spec 冷启动 | 手写 + agent 写回 + import 三路 |
| 18 | import 来源(MVP) | 只文本粘贴,后端拆 fragment |
| 19 | CLI 命名 | `parsar`(沿用 `parsar-daemon-*` 约定) |
| 20 | 表设计 | 新增 `spec_fragments` + `memories`,audit 复用 `audit_records` |
| 21 | 平台覆盖 | 所有 hook / plugin 各平台单独适配 |
| 22 | 不引入 Trellis 代码 | 自研,避免 AGPL + 抄袭嫌疑 |

---

## 1. 数据模型

### 1.1 设计原则:枚举值由代码侧管理

所有"取值有限的字符串字段"(`source` / `scope` / `memory_type` 等)在 DB 层只用 `TEXT NOT NULL`,
**不加 `CHECK (col IN (...))` 约束**。原因:

- 加新值时不用改 migration(避免 schema 演进卡 enum)
- 代码侧统一管理常量,validation 集中在 handler / CLI 入口层
- 未来要从 `source='agent'` 细分成 `source='agent:auto'` / `source='agent:proposed'` 这种,
  DB 层不阻塞,只改代码 enum + handler 校验

**Go 侧 enum 常量定义(`server/internal/specmemory/types.go`):**

```go
type Source string
const (
    SourceManual     Source = "manual"
    SourceAgent      Source = "agent"
    SourceImport     Source = "import"
    SourceUser       Source = "user"
    SourceAutoReview Source = "auto-review"   // 二期 review 兜底用,MVP 不写但常量先占位
)
func (s Source) Valid() bool { ... }

type Scope string
const (
    ScopeUser    Scope = "user"
    ScopeProject Scope = "project"
)

type MemoryType string
const (
    MemoryTypeUser      MemoryType = "user"
    MemoryTypeFeedback  MemoryType = "feedback"
    MemoryTypeProject   MemoryType = "project"
    MemoryTypeReference MemoryType = "reference"
)
```

handler / CLI 在入参时调 `.Valid()` 校验;sqlc 生成的 model 字段类型用 `string`,
service 层做 Source/Scope/MemoryType 之间的转换。

**字段间的结构性约束**(scope='project' 必须有 project_id)依然保留在 DB 层,
因为它不是枚举值约束,而是防数据腐烂的关系约束,跟扩展性无关。

### 1.2 新增表 `spec_fragments`

workspace 级,扁平多片段(每片段独立可编辑可注入)。

```sql
CREATE TABLE spec_fragments (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    UUID NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  title           TEXT NOT NULL,
  body            TEXT NOT NULL,
  tags            TEXT[] NOT NULL DEFAULT '{}',
  source          TEXT NOT NULL,              -- 代码侧 specmemory.Source 管理
  created_by      UUID REFERENCES users(id),  -- agent 写入时为 NULL
  agent_actor     TEXT,                       -- agent 写入时记录 connector 名 + project_agent_id
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at      TIMESTAMPTZ                 -- 软删
);
CREATE INDEX idx_spec_fragments_workspace ON spec_fragments(workspace_id) WHERE deleted_at IS NULL;
CREATE INDEX idx_spec_fragments_tags ON spec_fragments USING GIN(tags) WHERE deleted_at IS NULL;
```

### 1.3 新增表 `memories`

user 级和 project 级共用一张表,通过 `scope` 字段区分。

```sql
CREATE TABLE memories (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  scope           TEXT NOT NULL,              -- 代码侧 specmemory.Scope 管理
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  project_id      UUID REFERENCES projects(id) ON DELETE CASCADE, -- scope='project' 时非空
  memory_type     TEXT NOT NULL,              -- 代码侧 specmemory.MemoryType 管理
  title           TEXT,                       -- 可选,简短标题
  body            TEXT NOT NULL,              -- 主体
  why             TEXT,                       -- feedback/project 类推荐填写
  tags            TEXT[] NOT NULL DEFAULT '{}',
  source          TEXT NOT NULL,              -- 代码侧 specmemory.Source 管理
  agent_actor     TEXT,                       -- agent 写入时记录连接器名 + project_agent_id
  conversation_id UUID REFERENCES conversations(id), -- agent 写入时关联会话
  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted_at      TIMESTAMPTZ,
  -- 仅保留字段间结构性约束(防关系腐烂,跟扩展性无关)
  CONSTRAINT project_scope_requires_project_id
    CHECK ((scope = 'user' AND project_id IS NULL) OR (scope = 'project' AND project_id IS NOT NULL))
);
CREATE INDEX idx_memories_user ON memories(user_id, scope) WHERE deleted_at IS NULL;
CREATE INDEX idx_memories_project ON memories(project_id) WHERE deleted_at IS NULL AND scope = 'project';
```

### 1.4 Audit(复用 `audit_records`)

直接用 `server/migrations/000001_init.sql:1055-1102` 已有的表,新增 event_type:
- `spec_fragment.created` / `.updated` / `.deleted`
- `memory.created` / `.updated` / `.deleted`
- `spec.injected` / `memory.injected`(可选,debug 用)

actor_type 用 `user` / `agent`,actor_id 对应 user_id / project_agent_id。
通过 `server/internal/audit/postgres.go` 现有 `PostgresSink.Write()` 接口写入。

### 1.5 迁移文件位置

新建 `server/migrations/000005_spec_memory.sql`,遵循 goose 格式(参考现有 `000003_capability_canonical.sql`)。

---

## 2. Server 模块

### 2.1 新增 package `server/internal/specmemory/`

职责:
- DB 操作(CRUD spec_fragments / memories)
- 注入数据组装(把数据库内容渲染成 prompt 段)
- import 服务(粘贴文本 → 拆 fragment)
- audit 日志记录

文件组织建议:
```
server/internal/specmemory/
  types.go           # enum 常量(Source / Scope / MemoryType)+ 业务级 struct
  store.go           # sqlc 包装 + 业务级 CRUD(string ↔ enum 转换)
  injector.go        # 注入数据组装(SnapshotSpec / SnapshotMemory / IncrementalMemory)
  importer.go        # 文本拆 fragment
  prompts.go         # 注入到 prompt 的模板(段标题、格式、元指令)
  service.go         # 对外接口,被 connector 和 handler 调用
```

sqlc 查询放 `server/internal/db/queries/spec_fragments.sql` 和 `memories.sql`(沿用现有 sqlc 约定,参考 `audit_records.sql`)。

### 2.2 注入入口接入

**OpenCode connector** — `server/internal/connector/opencode/connector.go:Prompt()` L444:
在第 529 行 `stringFrom(mergedConfig, "system_prompt")` 拿到原始 system_prompt 后,
追加调用 `specmemory.Service.RenderInjection(ctx, workspaceID, userID, projectID, ...)` 返回的内容,
拼到 system_prompt 末尾(或固定标记块内)。

**AgentDaemon connector** — `server/internal/connector/agentdaemon/model_injection.go:renderStaticAgentOptions()` L163-183:
在 `system_prompt` 和 `override_system_prompt` 提取之后,新增第三个来源 `spec_memory_injection`,优先级最低(被 override 覆盖)。
通过 `agent_options` map 走 `PromptRequestPayload.AgentOptions` 传到 sandbox 内。

**注入数据形状**:

```go
type Injection struct {
    SpecBlock          string  // <spec> ... </spec>
    MemoryBlock        string  // <memory> ... </memory>
    MemoryWriteGuide   string  // <memory-write-guide> ... </memory-write-guide>
    IncrementalMemory  string  // 仅 per-turn,只含 session 启动后的新增
}
```

`SessionStart` 注入 SpecBlock + MemoryBlock + MemoryWriteGuide(全量)。
`per-turn` 注入仅 IncrementalMemory(差量)。

### 2.3 注入段格式(关键)

详见 `prompts.go` 内的模板。固定结构:

```
<spec workspace="{workspace_name}">
### {fragment.title}
{fragment.body}
{if tags}[tags: {tags}]{/if}

### {fragment.title}
...
</spec>

<memory>
## user
- {body}
## feedback
- {body} (Why: {why})
## project
- {body} (Why: {why})
## reference
- {body}
</memory>

<memory-write-guide>
You have a persistent memory via `parsar memory add --type X --body "..." [--why "..."]`.

Types:
- user: 用户角色、偏好、长期目标
- feedback: 用户的明确纠正 / 已确认的非显然决策(必须填 --why)
- project: 当前项目背景、里程碑、决策动因(必须填 --why)
- reference: 外部仪表盘、文档、Slack channel 等指针

When to save:
任何时候用户透露了对未来对话有帮助的稳定信息。安静写入,不要在对话里宣布。

When NOT to save:
- 任何可以从代码 / git history 推断的内容
- bug fix recipe(代码本身就是答案)
- ephemeral 任务上下文
</memory-write-guide>
```

### 2.4 HTTP / gRPC API

新增 handler(沿用现有 `server/internal/handler/` 模式):

```
GET    /api/v1/workspaces/:wid/spec/fragments
POST   /api/v1/workspaces/:wid/spec/fragments
PATCH  /api/v1/workspaces/:wid/spec/fragments/:id
DELETE /api/v1/workspaces/:wid/spec/fragments/:id
POST   /api/v1/workspaces/:wid/spec/import      # body: { text: "..." }

GET    /api/v1/memories?scope=user
GET    /api/v1/memories?scope=project&project_id=...
POST   /api/v1/memories
PATCH  /api/v1/memories/:id
DELETE /api/v1/memories/:id

# 给 sandbox CLI / hook 调
GET    /api/v1/agent-runtime/injection/snapshot?workspace_id=&user_id=&project_id=
GET    /api/v1/agent-runtime/injection/incremental?session_id=&since=
POST   /api/v1/agent-runtime/spec/fragments     # agent 写入,无 user_id
POST   /api/v1/agent-runtime/memories            # agent 写入,无 user_id
```

`/api/v1/agent-runtime/*` 走 runner_credential 鉴权(见 3.3)。其他走现有 user session 鉴权。

---

## 3. Sandbox 集成

### 3.1 `parsar` CLI 设计

Go 二进制,放 `/usr/local/bin/parsar`(与 `parsar-daemon` 同位置)。
源码新建 `cmd/parsar/`(独立 binary,和 server 共享 model)。

子命令:
```
parsar spec list [--tag X]
parsar spec add --title "..." --body "..." [--tag a,b]
parsar spec edit <id> [--title ...] [--body ...] [--tag ...]
parsar spec rm <id>

parsar memory list [--scope user|project] [--type user|feedback|project|reference]
parsar memory add --type <type> --body "..." [--title ...] [--why ...] [--tag a,b]
parsar memory edit <id> ...
parsar memory rm <id>

parsar sync                        # 重新拉取注入快照(调试用)
parsar --version
```

环境变量(由 sandbox provider 注入):
```
PARSAR_SERVER_URL=https://api.parsar.internal
PARSAR_RUNNER_TOKEN=<pairing-derived token>
PARSAR_RUNTIME_ID=<uuid>
PARSAR_WORKSPACE_ID=<uuid>
PARSAR_USER_ID=<uuid>
PARSAR_PROJECT_ID=<uuid>            # 可空
PARSAR_CONNECTOR=claude|opencode|codex
PARSAR_PROJECT_AGENT_ID=<uuid>
PARSAR_CONVERSATION_ID=<uuid>
```

CLI 调用 `/api/v1/agent-runtime/...` endpoint,header 带 `Authorization: Bearer $PARSAR_RUNNER_TOKEN`。

### 3.2 Hook 脚本(三平台各一组)

预装到镜像内 `/opt/parsar/hooks/` 下,各平台启动时由 server 生成 `.claude/settings.json` 或等价配置指向这些脚本。

**Claude Code (Python):**
```
/opt/parsar/hooks/claude/session-start.py        # 调 /injection/snapshot,返回 hookSpecificOutput
/opt/parsar/hooks/claude/user-prompt-submit.py   # 调 /injection/incremental,additionalContext 输出
```

通过 `~/.claude/settings.json` 或 `/workspace/.claude/settings.json` 配置(由 server 生成,见 3.4):
```json
{
  "hooks": {
    "SessionStart": [{"matcher": "*", "hooks": [{"type": "command", "command": "/opt/parsar/hooks/claude/session-start.py", "timeout": 10}]}],
    "UserPromptSubmit": [{"matcher": "*", "hooks": [{"type": "command", "command": "/opt/parsar/hooks/claude/user-prompt-submit.py", "timeout": 5}]}]
  }
}
```

**OpenCode (JS plugin):**
```
/opt/parsar/hooks/opencode/session-injection.js   # 注册 chat.message 第一条消息时拼 snapshot
/opt/parsar/hooks/opencode/per-turn-injection.js  # 注册 tool 或 message 钩子拼增量
```

通过 `~/.config/opencode/config.toml` 或 `.opencode/config.toml` 配置 plugin 路径(由 server 生成)。
插件用 `fetch()` 调 server endpoint。

**Codex (降级方案):**
- 无 per-turn,只 SessionStart
- sandbox 启动时 server 生成 `~/.codex/AGENTS.md`,内容 = snapshot 注入(spec + memory + write-guide)
- 不挂任何 hook 脚本
- agent 调 `parsar memory add` 仍可写回(CLI 一致)

**所有 hook 脚本依赖 `parsar` CLI 或 `curl` 直接调 endpoint**,与 server 通信走 `PARSAR_RUNNER_TOKEN`。
建议统一让 hook 通过 `parsar inject snapshot` / `parsar inject incremental` 子命令拿数据,
hook 脚本只负责把 CLI stdout 拼到平台要求的格式(stdout JSON / additionalContext / 等)。
这样后续修改注入逻辑只改 Go CLI 一处,不用改三处 hook。

### 3.3 鉴权(复用 runtime pairing)

参考已对齐的设计 — 新增 `runtime_type = 'agent_runtime'`(沿用 user feedback memory:
*"用 runtimes 的 pairing_token → runner_credential 流程,新增 runtime_type"*)。

**流程:**
1. `SandboxProvider.Acquire()` 在创建 sandbox 时调用 `CreateRuntimePairing()`,
   除了给 `parsar-daemon` 颁发 token,**再颁发一份给 `parsar` CLI 用**(可以是同一 runtime 的两个 derived token,或两份独立 runtime)。
   推荐:**单个 runtime 颁发的 token 同时给 daemon 和 CLI 用**,scope 字段标记权限范围。
2. token 通过环境变量 `PARSAR_RUNNER_TOKEN` 传入 sandbox(server provider 在 `RunCommand()` 里 export)。
3. server 端新增中间件 `auth.RunnerCredentialMiddleware`,识别 `/api/v1/agent-runtime/*` 路径,
   查 `runtimes` 表的 `pairing_token_hash`,校验后把 runtime 关联的 workspace/project/user 注入 ctx。

**新增 store 方法**(在 `server/internal/store/store.go` 现有 `CreateRuntimePairing()` 旁边):
```go
ValidateRunnerToken(ctx, token) (RuntimeIdentity, error)
// RuntimeIdentity: { RuntimeID, WorkspaceID, UserID, ProjectID, ProjectAgentID, Scope }
```

### 3.4 Dockerfile 修改

文件:`infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile`

新增内容(在现有 parsar-daemon 安装段后):
```dockerfile
# parsar CLI
ARG PARSAR_VERSION=...
RUN curl -fSL "$GITLAB_URL/parsar-$PARSAR_VERSION-linux-amd64" -o /usr/local/bin/parsar \
    && chmod +x /usr/local/bin/parsar

# Hook scripts (从 build context COPY,源在 infra/e2b-templates/parsar-daemon-claudecode/hooks/)
COPY hooks/claude /opt/parsar/hooks/claude
COPY hooks/opencode /opt/parsar/hooks/opencode
RUN chmod +x /opt/parsar/hooks/claude/*.py
```

Hook 脚本源放 `infra/e2b-templates/parsar-daemon-claudecode/hooks/{claude,opencode}/` 下。

**`.claude/settings.json` / opencode config 不在镜像里 seed**,而是 sandbox 启动后由 server 调 e2b Exec 写入(因为内容含 runtime-specific 值)。
扩展 `SandboxProvider.Acquire()` 后的 `RunCommand()` 链,在启动 `parsar-daemon connect` 之前先写入这些配置文件。

### 3.5 Sandbox 生命周期改造

文件:`server/internal/connector/agentdaemon/sandbox_provider.go`

在 `Acquire()` 当前链路上加一步:
1. `CreateRuntimePairing()` ← 已有
2. `e2b.Create()` ← 已有
3. **新增**: `seedPlatformConfig(ctx, sandbox, connector, runtimeContext)` — 调 e2b Exec 写入 `.claude/settings.json` 或等价配置
4. `RunCommand(parsar-daemon connect ...)` ← 已有,新增环境变量 `PARSAR_*`
5. `WaitForDevice()` / `Binder.Bind()` ← 已有

新增辅助函数 `seedPlatformConfig` 放在 `sandbox_provider.go` 或独立 `sandbox_seed.go`。

---

## 4. 写回流程

### 4.1 Agent 主动写 memory

1. SessionStart 注入的 `<memory-write-guide>` 教 agent 在合适时机写
2. Agent 在工具调用中执行 `parsar memory add --type feedback --body "..." --why "..."`
3. `parsar` CLI 调 `POST /api/v1/agent-runtime/memories`,header 带 token
4. server 校验 token → 写入 `memories` 表 → 写入 `audit_records`(actor_type=agent, actor_id=project_agent_id)
5. 下一轮 user prompt 时,per-turn hook 拉增量,新写的 memory 自动进入 prompt

### 4.2 User 在 UI 改 spec / memory

1. UI 调 `/api/v1/workspaces/:wid/spec/fragments` 等 endpoint(走 user session 鉴权)
2. server 写入表 + audit
3. 下一次 SessionStart 时新内容自动注入
4. 当前进行中的 session 通过 per-turn 增量拉到(注入 incremental 段)

### 4.3 冲突处理(MVP 简化)

- spec_fragments / memories 是细粒度记录,单条 update 用乐观锁(`updated_at` 作 version)
- 同一条同时改:后写覆盖前写,UI 弹"内容已被他人更新"提示
- 没有 git 式 merge,因为粒度本来就细

---

## 5. 三平台适配差异速查表

| 维度 | Claude Code | OpenCode | Codex |
|------|-------------|----------|-------|
| SessionStart 注入 | hook 脚本 `additionalContext` | plugin `chat.message` 钩子 | `AGENTS.md` 启动文件 |
| per-turn 增量注入 | hook 脚本 `UserPromptSubmit` | plugin `chat.message` 钩子 | **不支持(降级)** |
| 注入位置 | system prompt 前 / `<context>` 块 | message parts | AGENTS.md |
| 配置文件位置 | `.claude/settings.json` | `~/.config/opencode/config.toml` | `~/.codex/AGENTS.md` |
| 配置生成时机 | sandbox 启动后由 server 写入 | sandbox 启动后由 server 写入 | sandbox 启动后由 server 写入(整个 AGENTS.md) |
| Hook 脚本语言 | Python(stdin/stdout JSON) | JS(plugin factory) | 无 |
| Hook 通过 parsar CLI 调 server | ✅ | ✅ | N/A |
| 写回 memory 路径 | `parsar memory add` | `parsar memory add` | `parsar memory add` |

---

## 6. MVP 范围 & 不做的事

**做:**
- spec_fragments / memories 两张表 + audit 复用
- workspace 级 spec UI(列表 + 详情编辑 + 标签)
- user / project 级 memory UI(分类列表 + 编辑 + audit 入口)
- 文本粘贴 import(后端拆 fragment,简单按 H2/H3 切片)
- `parsar` CLI 完整子命令
- Claude / OpenCode SessionStart + per-turn hook
- Codex SessionStart 注入(AGENTS.md 生成)
- runner_credential 鉴权扩展
- agent 自觉写 memory 元指令

**不做(留二期):**
- tag-based 智能注入(只全量)
- 后置 LLM review 兜底(只 agent 自觉)
- import 来源:文件上传 / 仓库扫描(只文本粘贴)
- Trellis 式目录结构 spec(只扁平 fragment)
- memory 版本管理 / 回退
- 团队级 memory 共享(只 user / project)
- approval / 审批流(直接写 + 审计)

---

## 7. 落地任务清单(可独立分配)

### Phase 1: 数据层(必须最先)
1. 写 migration `server/migrations/000005_spec_memory.sql`(spec_fragments + memories + 约束 + 索引)
2. 写 sqlc 查询 `server/internal/db/queries/spec_fragments.sql` + `memories.sql`
3. 跑 `make sqlc` 生成 Go 代码

### Phase 2: Server 业务层
4. 新建 package `server/internal/specmemory/`(types / store / injector / importer / service / prompts)。**types.go 先建,定义 Source / Scope / MemoryType 三个 enum 常量与 Valid() 校验,后续所有模块依赖它。**
5. 实现 importer:简单按 markdown H2/H3 切片(import 用)
6. 实现 injector:渲染 SpecBlock / MemoryBlock / MemoryWriteGuide / IncrementalMemory 模板
7. 接入 `audit_records` 写入(用现有 `audit.PostgresSink`)

### Phase 3: 鉴权扩展
8. 扩展 `runtimes` 表的 `runtime_type` 取值(加 'agent_runtime')— 或确认现有字段能复用
9. 在 store 新增 `ValidateRunnerToken(ctx, token) (RuntimeIdentity, error)`
10. 新增中间件 `server/internal/middleware/runner_credential.go`,挂到 `/api/v1/agent-runtime/*` 路由

### Phase 4: HTTP Handler
11. UI 端 spec / memory CRUD handler(走 user session 鉴权)
12. agent-runtime 端 inject snapshot / incremental / 写回 handler(走 runner credential)
13. import handler

### Phase 5: 注入接入(connector 改造)
14. OpenCode connector `Prompt()` 在 system_prompt 拼接处调 `specmemory.Service.RenderInjection()`
15. AgentDaemon connector `renderStaticAgentOptions()` 加 `spec_memory_injection` 第三来源
16. 单元测试覆盖注入字符串拼接

### Phase 6: `parsar` CLI
17. 新建 `cmd/parsar/main.go`,实现子命令(用现有 cobra 或类似)
18. CLI 复用 server 的 model struct,HTTP 调用走 token
19. 跨平台编译(linux/amd64 + linux/arm64),发布到 GitLab artifact

### Phase 7: Sandbox 镜像 & Hook
20. 修改 `infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile` 安装 `parsar` 二进制 + COPY hook 脚本目录
21. 新建 `infra/e2b-templates/parsar-daemon-claudecode/hooks/claude/{session-start.py, user-prompt-submit.py}`
22. 新建 `infra/e2b-templates/parsar-daemon-claudecode/hooks/opencode/{session-injection.js, per-turn-injection.js}`
23. 三平台 hook 脚本统一改成 "调 `parsar inject ...` 拿数据"(注入逻辑集中在 Go 侧)

### Phase 8: Sandbox 生命周期改造
24. `sandbox_provider.go` 的 Acquire 链路加 `seedPlatformConfig()` 步骤,写入平台特定配置
25. 改造 `RunCommand(parsar-daemon ...)` 注入 `PARSAR_*` 全套环境变量

### Phase 9: UI
26. Spec fragment 列表 + 详情编辑(workspace 设置页下增加 tab)
27. Memory 列表(分 user / project)+ 详情 + audit 入口
28. Import 大文本框 + 拆分预览 + 入库

### Phase 10: 验证 & 上线
29. 集成测试:三平台各跑一遍 SessionStart 注入 + agent 调 `parsar memory add` 写回
30. 文档:README / 用户使用指南 / 内部开发者文档

依赖关系:Phase 1 → 2 → (3 || 4 || 5) → 6 → (7 || 8) → 9 → 10。
3、4、5 可并行;6 依赖 3;7、8 依赖 6;9 依赖 4。

---

## 8. 关键文件路径速查

| 用途 | 文件 |
|------|------|
| 迁移 | `server/migrations/000005_spec_memory.sql`(新建) |
| sqlc 查询 | `server/internal/db/queries/spec_fragments.sql`、`memories.sql`(新建) |
| 业务 package | `server/internal/specmemory/`(新建) |
| 鉴权中间件 | `server/internal/middleware/runner_credential.go`(新建) |
| 注入接入(OpenCode) | `server/internal/connector/opencode/connector.go:529`(改造) |
| 注入接入(AgentDaemon) | `server/internal/connector/agentdaemon/model_injection.go:163-183`(改造) |
| sandbox 生命周期 | `server/internal/connector/agentdaemon/sandbox_provider.go`(改造,新增 seedPlatformConfig) |
| Proto(无需改) | `internal/agentdaemon/proto/outbound.go`(已支持 AgentOptions map) |
| Audit 接入(无需改表) | `server/internal/audit/postgres.go:24-50` 现有 `PostgresSink.Write()` |
| Dockerfile | `infra/e2b-templates/parsar-daemon-claudecode/e2b.Dockerfile`(改造) |
| Hook 脚本 | `infra/e2b-templates/parsar-daemon-claudecode/hooks/{claude,opencode}/`(新建) |
| CLI 源码 | `cmd/parsar/`(新建) |
| UI | 取决于前端项目结构,通常 `web/src/pages/workspace/spec/` 等(参照现有 workspace settings 页) |

---

## 9. 风险与开放问题

### 风险
1. **Hook 脚本失败兜底** — 如果 `/injection/snapshot` 超时或失败,hook 是返回空(降级为无注入)还是中断 session?
   建议:返回空 + 服务端写 audit + 在 sandbox 内打 warning log,session 继续。
2. **token 泄露** — `PARSAR_RUNNER_TOKEN` 在 sandbox 内任何进程可读。
   缓解:token 短 TTL(1 小时)+ sandbox 关闭即吊销 + 只能访问 `/api/v1/agent-runtime/*` 路径。
3. **注入大小膨胀** — 用户 memory 几百条时 system prompt 会很大。
   MVP 不做截断,二期按 tag / 时间窗筛选。
4. **多 connector 同 project 并发** — 同一 project 多个 sandbox 同时跑,memory 增量同步频率需要测。
   方案:per-turn hook 用 `since` 参数(上一次拉取时间)做增量游标。
5. **Codex 降级体验差** — 没有 per-turn,session 中 agent 写回的 memory 当 session 看不到。
   方案:在 Codex 注入的 AGENTS.md 末尾加一句"如新增 memory 请在下一次会话生效",承认这是已知限制。

### 开放问题(实施时再决策,不阻塞 plan)
1. **session_id 来源** — Conversation ID 在 sandbox 内通过什么环境变量传?是否每轮 hook 也要带 since 游标?
2. **import 切片策略** — H2 切 vs H3 切 vs LLM 切?MVP 先 H2 切 + 用户预览修改。
3. **Audit 保留时长 / 谁能查** — 沿用 audit_records 现有策略即可,不在本方案决策范围。
4. **memory why 字段是否强制** — feedback / project 类推荐填,但 schema 不强制(NOT NULL),由 CLI / UI 引导用户。
5. **是否给 user/project memory 加 importance / 排序权重字段** — 二期再说,MVP 用 updated_at desc。

---

## 10. Verification(怎么验)

### 10.1 单元测试
- `specmemory/injector_test.go`:给定一组 fragments + memories,渲染出的字符串符合模板
- `specmemory/importer_test.go`:给定一段 markdown,拆出来的 fragments 数量 / title / body 正确
- `middleware/runner_credential_test.go`:有效 token / 过期 token / 无 token 三种 case

### 10.2 集成测试(三平台)
跑一个 e2e 脚本:
1. 创建 workspace + project + project_agent
2. 在 spec UI 加 2 条 fragment
3. 在 memory UI 加 1 条 user memory
4. 启动 sandbox(分别用 claude / opencode / codex connector)
5. 第一轮 prompt:让 agent 复述 spec — 期望它能引用 fragment 内容
6. 用户回复:"我们项目用 cursor 不用 offset 做分页" — 期望 agent 调 `parsar memory add --type feedback`
7. 查 `memories` 表,新行存在,actor_type=agent
8. 第二轮 prompt:让 agent 复述 memory — 期望它能引用刚才学到的

### 10.3 手动验证(Claude Code 路径优先)
- `e2b spawn` 一个 sandbox,exec `cat /workspace/.claude/settings.json` 看配置生成对不对
- exec `/opt/parsar/hooks/claude/session-start.py < empty.json` 看 stdout 是不是合法 JSON
- exec `parsar memory list --scope user` 看 CLI 能否调通
- 在 sandbox 内 exec `parsar memory add --type user --body "test"`,查表确认写入 + audit

### 10.4 平台对比验证
- 三平台同一 spec / memory,prompt 注入后实际打到模型的内容做 diff,确认核心内容一致(忽略平台固有差异)
- token 用量对比:无注入 vs 有注入(MVP 全量),看实际开销
