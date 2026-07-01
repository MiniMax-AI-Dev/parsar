# Parsar 部署 runbook

本文面向把 Parsar 部署到生产环境的运维。目标：从空数据库 + 配置文件出发，让 Parsar 完成真实初始化，
**不依赖** `seed-dev` 假数据、固定 fixture UUID、`X-Parsar-Dev-User-ID` dev shim 这些开发期捷径。

适用对象：自托管开源版本 —— 任何 deployment profile（单机 docker、K8s、裸金属）都按本文档走。

> **相关文档**
> - [feishu-prod.md](./feishu-prod.md) — Feishu OIDC + 事件订阅生产配置。
> - [feishu-bot-per-agent.md](./feishu-bot-per-agent.md) — 把单个 Agent 暴露成飞书机器人（每个挂飞书的 Agent 一份）。
> - [health-and-smoke.md](./health-and-smoke.md) — `/healthz`、`/readyz`、`smoke.sh` 检查器。
> - [config.example.yaml](./config.example.yaml) — 注释齐全的 YAML 模板。
> - [`deploy/compose/compose.example.yml`](../../deploy/compose/compose.example.yml) — 单机部署 compose 起点。
> - [`deploy/compose/.env.example`](../../deploy/compose/.env.example) — env 注入模板。

---

## 1. 整体启动顺序

```text
1. 准备 Postgres：空库可用 + 凭证就绪
2. 准备 config / env：见 §2
3. 跑数据库 migration            (./parsar-migrate 或 docker run parsar:<tag> parsar-migrate 或 make migrate-dev)
4. 启动 server                  (./parsar-server  或 docker run parsar:<tag>)
5. 健康检查通过                  (/healthz + /readyz 均返回 200)
6. 创建第一个 owner + workspace  (HTTP API 或 CLI，二选一)
7. 关闭 bootstrap token         (从 env 移除 PARSAR_BOOTSTRAP_TOKEN 并重启 server)
8. 跑 smoke-core 验证最小闭环   (scripts/smoke.sh --core)
9. 正常运行
```

> migration 必须先于 server 第一次承接业务请求。`/healthz` 是 liveness，
> 不查 schema；`/readyz` 只校验 DB 可连通。任何 conversation/agent 调用
> 在未 migrate 的空库上都会 500。

> `./parsar-server` 和 `./parsar-migrate` 是 production image 里 `WORKDIR`
> 下的两个 binary(由仓库根 `Dockerfile` + `make docker-build` 构建)。本地 dev
> 不需要这两个 binary —— 用 `make server` / `make migrate-dev` 走
> `go run ./cmd/...` 即可。

> step 8 的 smoke-core 不是装饰：它复跑 step 5 的三条探活，并额外确认
> `/api/v1/bootstrap/status` 已对外开放、`dev_auth_enabled=false`（生产硬约束）、
> bootstrap 状态已收敛到「已经有 owner」、第二次 POST `/api/v1/bootstrap`
> 必须返回 409（门已闩死）。完整规则见
> [health-and-smoke.md](./health-and-smoke.md#smoke-script)。

Docker Compose / K8s 部署模板见 §7。

---

## 2. 配置载入顺序

`server/internal/config` 是配置 source of truth。加载优先级（后者覆盖前者）：

1. 内置默认值（`Default()`）。
2. 可选 YAML 文件：通过 `PARSAR_CONFIG_FILE=<绝对路径>` 指定。
   - 路径必须是绝对路径或 `~/` 开头；**相对路径会被拒绝**（避免 CWD 误读）。
   - 不设置该 env 时，server 完全不读任何文件。**不存在「自动读 `./config.yaml`」的回退行为**。
3. 环境变量（始终最高优先级，便于注入 secret）。

启动时会校验：

- `database.url` 在生产 profile 必须非空。
- `secret.master_key` 在生产 profile 必须非空。
- `auth.dev_auth=true` 仅允许在 dev profile。
- `auth.cookie.secure=true` 在生产 profile 必须为 true。

Profile 推断规则：只要 `auth.dev_auth=true` 或 `gateway.feishu.mock=true` 任一为真，整个进程切到 dev profile；
否则按生产 profile 校验。

### 示例 YAML

参考 `docs/deploy/config.example.yaml`（带注释的部署模板）。
示例文件只放 placeholder，**任何真实凭证都不进 repo**。

### 关键 env 变量速查

| 用途 | env 名 | 默认 |
|---|---|---|
| 监听地址 | `PARSAR_ADDR` | `:8080` |
| 对外 URL（构 callback 用） | `PARSAR_PUBLIC_URL` | 空 |
| Runtime 数据目录 | `PARSAR_DATA_DIR` | `~/.parsar` |
| Postgres 连接 | `DATABASE_URL` | （必填） |
| Secret 主密钥 | `PARSAR_MASTER_KEY` | （生产必填） |
| Bootstrap token | `PARSAR_BOOTSTRAP_TOKEN` | 空（HTTP bootstrap 关闭） |
| Dev auth 开关 | `PARSAR_DEV_AUTH` | `false`（生产必为 false） |
| HTTPS cookie | `PARSAR_COOKIE_SECURE` | `false`（生产必为 true） |
| Runtime profile | `PARSAR_RUNTIME_PROFILE` | `managed` for managed deployments where the platform manages cloud sandboxes |

Feishu OAuth / event 相关 env 见 [feishu-prod.md](./feishu-prod.md)。

---

## 3. Bootstrap：第一个 owner + workspace

空库启动后 `GET /api/v1/bootstrap/status` 会返回：

```json
{
  "needed": true,
  "has_owners": false,
  "owner_count": 0,
  "http_enabled": false,
  "dev_auth_enabled": false
}
```

`needed=true` 表示设置尚未完成。安装阶段的 installer UI 可以根据这个状态决定下一步。

可选**两条路径**完成第一个 owner + workspace 的创建：

### 3.1 路径 A：HTTP API（远程部署）

适合无法直接登录目标机的场景（K8s、托管平台）。

```bash
# 1. 生成一个一次性强 token（32 字节随机）
export PARSAR_BOOTSTRAP_TOKEN="$(openssl rand -hex 32)"

# 2. 在 server 启动 env 中导出该 token，然后启动 server
#    （生产中通常通过 K8s secret / systemd EnvironmentFile 注入）

# 3. 调用 bootstrap 接口
curl -sf -X POST https://parsar.example.com/api/v1/bootstrap \
  -H "Authorization: Bearer ${PARSAR_BOOTSTRAP_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "email":          "admin@example.com",
    "name":           "First Admin",
    "workspace_name": "Acme"
  }'
```

成功返回 201：

```json
{
  "user_id": "...",
  "user_created": true,
  "workspace_id": "...",
  "workspace_slug": "workspace-deadbeef",
  "workspace_name": "Acme",
  "member_id": "...",
  "setup_complete": true
}
```

完成后：

- `GET /api/v1/bootstrap/status` 返回 `needed=false`、`has_owners=true`。
- 再次 POST 返回 409 + `bootstrap_closed`。
- **必须立刻把 `PARSAR_BOOTSTRAP_TOKEN` 从 env 移除**，并重启进程让其生效。
  （token 留在 env 即便已经无法触发 bootstrap，也是一份长期可被窃取的凭证。）

### 3.2 路径 B：CLI（本地 / 容器内）

适合可以直接 `kubectl exec` / `docker exec` 到目标机的场景。**不需要** token。

```bash
export DATABASE_URL="postgres://parsar:parsar@127.0.0.1:5432/parsar?sslmode=disable"

go run ./server/cmd/parsar-bootstrap \
  --email=admin@example.com \
  --workspace="Acme" \
  --name="First Admin"
```

或通过 Makefile：

```bash
DATABASE_URL=postgres://... \
PARSAR_BOOTSTRAP_EMAIL=admin@example.com \
PARSAR_BOOTSTRAP_WORKSPACE="Acme" \
PARSAR_BOOTSTRAP_NAME="First Admin" \
make bootstrap
```

退出码：

| code | 含义 |
|---|---|
| 0 | 成功 |
| 1 | 参数错误（缺 --email / --workspace / DATABASE_URL） |
| 2 | 已有 owner，bootstrap 已关闭 |
| 3 | 输入校验失败 |
| 4 | 数据库连接 / 提交失败 |
| 5 | 其它错误 |

### 3.3 Bootstrap 一致性

无论走哪条路径，最终都通过 `store.ProvisionFirstOwner` 在**单个事务**内：

1. 校验「没有任何 active workspace owner」（gate）。
2. UpsertUserByEmail。
3. CreateWorkspace（auto slug + operator 指定 name）。
4. 插入 workspace_members(owner)。
5. 写一条 `bootstrap.first_owner_created` audit 记录。

并发同时调用两次也只会成功一次，第二次返回 `ErrBootstrapClosed`。

---

## 4. 与 seed-dev / dev_auth 的关系

| | 用于 | 状态 |
|---|---|---|
| `make seed-dev-db` | 本地开发 fixture，写入固定 UUID 的 demo workspace | **不要在生产部署路径用** |
| `PARSAR_DEV_AUTH=true` + `X-Parsar-Dev-User-ID` header | 本地开发跳过 cookie 登录 | 生产必关，启动时 Validate() 会拒绝 |
| `PARSAR_FEISHU_MOCK=true` | 本地开发跳过真实飞书 | 同上 |
| `PARSAR_BOOTSTRAP_TOKEN` + HTTP bootstrap | **生产合法的初始化入口** | 单次使用，用完移除 |
| `parsar-bootstrap` CLI | **生产合法的初始化入口** | 仅本地访问 |
| `scripts/smoke.sh` (lite) | 部署后探活：`/healthz` `/readyz` `/api/v1/health` | 适用任意 deployment profile |
| `scripts/smoke.sh --core` | lite 探活 + bootstrap 链路验证：bootstrap 状态可读 + `dev_auth_enabled=false` + （可选）provision + idempotency 闭门 | 适用任意 deployment profile；带 `--bootstrap-token` 时额外验 POST 路径 |

---

## 5. 部署 ready 还剩下的依赖

本轨道（Bootstrap + Config）只覆盖**冷启动数据 + 配置层**。要让一套部署真正进入生产 ready，还缺：

| 项 | 当前状态 | 谁负责 |
|---|---|---|
| **Production artifact / OCI 镜像** | **已落地,见仓库根 `Dockerfile` + `make docker-build`** | — |
| Sandbox runner（E2B） | Phase 4 实现中 | 另一个 session |
| Admin UI（installer 第一步指引） | Phase 4 follow-up | 另一个 session |
| 真实 auth（Feishu OIDC 生产配置） | 已落地，见 `feishu-prod.md` | — |
| 真实 auth（其它 OAuth provider，如 GitHub / Google / email magic link） | 未实现 | 后续 phase |
| Smoke — 部署后探活（/healthz、/readyz、/api/v1/health） | 已落地，见 `health-and-smoke.md` 的 lite 模式 | — |
| Smoke — bootstrap 链路（status / dev_auth shim / provision / idempotency） | 已落地，见 `health-and-smoke.md` 的 core 模式 | — |
| Smoke — AgentRun / audit / usage 端到端 | 缺 `/api/v1/workspaces/{wid}/{agent-runs,audit-records,usage}` 等 cookie-session 入口；smoke-core 把这一项标 SKIP/TODO | 后续 phase |
| Audit sink 真实化（Kafka / 自托管存储） | 当前 in-memory + Postgres sink；接口已抽象 | 后续 phase |
| Memory L0-L3 | 未实现 | 后续 phase |
| Capability marketplace | 未实现 | 后续 phase |

**本轨道交付的不变量**：

- 不再依赖 seeddev fixture UUID。
- 不再依赖 `X-Parsar-Dev-User-ID` shim。
- 配置不会从 CWD 自动读 / 写。
- `make check` 仍然通过。
- 生产 profile 启动若缺关键 secret（master key / DATABASE_URL）会失败而不是降级。

---

## 6. 安全注意事项

- `PARSAR_BOOTSTRAP_TOKEN`、`PARSAR_MASTER_KEY`、`PARSAR_FEISHU_APP_SECRET` 等**永远不要进 repo**，也不要写进
  示例 YAML 的默认值 —— 示例文件里全是 `<placeholder>`。
- HTTP bootstrap 接口的 token 用 `crypto/subtle.ConstantTimeCompare` 比对，避免时序攻击。
- `bootstrap.first_owner_created` audit 事件会带 `user_email`、`workspace_slug`，可以在 audit log 中追溯安装时刻。
- `GET /api/v1/bootstrap/status` 是公开的（按设计），只暴露布尔状态 + owner 数量，不暴露任何身份。

---

## 7. Docker Compose 部署模板

仓库自带一份起点 compose，覆盖 §1 的 1–4 + 7 步：

```text
deploy/compose/
├── README.md                  目录说明 + 三种部署形态
├── compose.example.yml       parsar-server + postgres 两 service
└── .env.example               env 模板（全是 placeholder）
```

### 7.1 端到端跑一次（单机 + 内置 Postgres）

```bash
# 1. 准备 env
cp deploy/compose/.env.example deploy/compose/.env
# 编辑 deploy/compose/.env：
#   - PARSAR_SERVER_IMAGE   你的镜像 path
#   - PARSAR_PUBLIC_URL     反向代理对外 URL
#   - PARSAR_PG_PASSWORD    openssl rand -hex 24
#   - PARSAR_MASTER_KEY     openssl rand -hex 32
#   - PARSAR_BOOTSTRAP_TOKEN openssl rand -hex 32（用完移除）

# 2. （可选）准备 YAML config
sudo install -d /etc/parsar
sudo cp docs/deploy/config.example.yaml /etc/parsar/config.yaml
# 把每一个 <placeholder> 换成真值 —— 真凭证仍然走 env，不写文件。

# 3. 拉起 postgres + server
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env up -d

# 4. 跑 migration（容器内执行，连同一份 DATABASE_URL）
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env \
  exec parsar-server ./parsar-migrate

# 5. Smoke check
scripts/smoke.sh --api-url http://127.0.0.1:8080

# 6. Bootstrap 第一个 owner（§3.1 走 HTTP 或 §3.2 走 CLI）
curl -sf -X POST http://127.0.0.1:8080/api/v1/bootstrap \
  -H "Authorization: Bearer ${PARSAR_BOOTSTRAP_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","name":"First Admin","workspace_name":"Acme"}'

# 7. 关闭 bootstrap：把 .env 里 PARSAR_BOOTSTRAP_TOKEN 删掉，然后
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env \
  up -d --force-recreate parsar-server
```

### 7.2 校验 compose 文件语法

每次改完 `compose.example.yml` 或 `.env.example`，跑一次：

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env config >/dev/null
```

`docker compose config` 会展开所有 `${VAR}` 并校验 YAML / schema，
返回 0 = 文件可被 docker 引擎解析。CI 应该把这条命令加进 lint 阶段。

### 7.3 K8s / 其它 orchestrator

`compose.example.yml` 不是 K8s manifest，但可以照搬：

- env block → Deployment.spec.template.spec.containers[].env
- healthcheck → `livenessProbe` 用 `/healthz`、`readinessProbe` 用 `/readyz`
  （详见 [health-and-smoke.md §3](./health-and-smoke.md#kubernetes-probe-configuration)）
- volumes → ConfigMap (config.yaml) + PersistentVolumeClaim (runtime data)
- `.env` → Secret 资源；env 通过 `envFrom: secretRef:` 注入

K8s manifest / Helm chart / kustomize overlay 由各部署方按自己环境维护，
不进开源仓库。
