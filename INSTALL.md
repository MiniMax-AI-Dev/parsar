# INSTALL — Parsar 本地版快速上手(Local Quickstart)

> **面向:** 想在本机自用或验收 Parsar 的开发者 / AI 编码 agent(Claude Code、Cursor、Codex 等)。
> **给你什么:** 几分钟内拉起一个可用的 Parsar(mock 登录,无需配置飞书或任何密钥),并把你本机的 Claude Code / OpenCode / Codex 接入成一台**在线设备**——全程「浏览器复制一条命令 → 终端粘贴 → 设备在线」。
> **不在本页:** 自部署 / 生产部署(真实飞书 OIDC、自定义端口与密钥、bootstrap token)是另一条路径,见 `deploy/compose/compose.example.yml` 与 `docs/deploy/`。

---

## 总览

```
clone → (拉取/构建镜像) → docker compose up → 浏览器登录 → 接入设备 → 设备在线
```

本地栈是 all-in-one:`postgres` + 一次性 `init`(迁移 + 建首个 owner)+ `parsar-server`(mock 登录)。**profile not fork**:它与自部署版用的是**同一个镜像 / 迁移 / SPA / install.sh**,差异只在 compose 文件与环境变量。

---

## 0 · 前置

```bash
docker compose version                                       # 需要 v2+
claude --version || opencode --version || codex --version    # 至少一个,且已登录
```

- **Docker** 已安装并正在运行(macOS 需先打开 Docker Desktop,`docker info` 能看到 server 段)。
- 本机已装好并**登录**至少一个 Agent CLI(Claude Code / OpenCode / Codex)。daemon 不自带模型登录,它复用这个 CLI 的登录态与订阅,并能看见你本机真实的仓库。

---

## 1 · 拉代码

```bash
git clone <your-repo-url> parsar
cd parsar
# 本地版交付目前在 feature/deliverables-design 分支;合并主干后忽略此步,用默认分支即可
git checkout feature/deliverables-design
```

---

## 2 · 选择镜像 + 设置环境变量

默认从 GHCR 拉预构建镜像(下一步 `docker compose` 会自动完成,无需手动 pull):

```bash
# 端口默认 18080 / 15432;被占用就改这两个数(例:18088 / 15488)
export PARSAR_LOCAL_PORT=18080
export PARSAR_PG_PORT=15432
```

> **当前状态(待镜像发布到 GHCR 后删除本提示):**
> 让「接入设备」离线零配置的关键——**镜像内置的 4 平台 daemon 二进制**——尚未随镜像发布到 GHCR。在它发布之前,直接拉 GHCR 的镜像**不含**内置 daemon,**第 4 步接入设备会下载 daemon 失败(404)**。此期间请走本地构建作为 fallback:
> ```bash
> make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=local   # 首次约 5–10 分钟
> export PARSAR_SERVER_IMAGE=parsar:local
> ```
> 构建后验证镜像确实内置了 daemon:
> ```bash
> docker run --rm --entrypoint ls parsar:local /usr/local/share/parsar/daemon
> # 预期列出 4 个:parsar-daemon-{darwin,linux}-{amd64,arm64}
> ```
> 该镜像发布到 GHCR 后,本提示连同上面的构建步骤一并删除——届时直接进第 3 步用默认镜像即可。

---

## 3 · 一条命令拉起本地栈

```bash
docker compose -f docker-compose.local.yml up -d
```

首次会拉取/使用 `parsar-server` 镜像、跑数据库迁移、并自动创建第一个工作区(owner = mock 身份 `admin@example.com`)。

**预期:** 三个容器;`parsar-local-server` 与 `parsar-local-postgres` 在约 15 秒内 healthy,`parsar-local-init` 跑完后退出。

**验证:**
```bash
docker compose -f docker-compose.local.yml ps
docker inspect parsar-local-init --format 'init exit={{.State.ExitCode}}'
# 预期 0;重复 up 仍是 0(已 bootstrap 时 parsar-bootstrap 退 2,compose 用 `|| [ $? -eq 2 ]` 收敛为 0)
```

命令行快速自检(进浏览器前 30 秒确认整栈通):
```bash
B="http://127.0.0.1:${PARSAR_LOCAL_PORT}"
for p in /healthz /api/v1/health / ; do
  printf '%-18s -> %s\n' "$p" "$(curl -fsS -o /dev/null -w '%{http_code}' "$B$p")"
done
curl -fsS "$B/api/v1/bootstrap/status"; echo
```
**预期:**
```
/healthz           -> 200
/api/v1/health     -> 200
/                  -> 200
{"needed":false,"has_owners":true,"owner_count":1, ...}
```

---

## 4 · 浏览器 E2E 验收(北极星:接入你的设备)

1. 浏览器打开 **http://127.0.0.1:18080**(改过端口就用你的端口)。
2. 点**登录**(界面可能显示「飞书登录」;mock 模式下无需账号密码,直接进)——身份 `admin@example.com`,落在 `Local Workspace`。
3. 进**设备 / 运行时管理** → 点**「接入新设备」** → 填一个设备名 → 点**「生成连接命令」**。
4. 复制弹窗里**那一条**命令 → 粘到**本机另一个终端**执行。命令形如:
   ```bash
   curl -fsSL http://127.0.0.1:18080/api/v1/parsar-daemon/install.sh \
     | PARSAR_DAEMON_CONNECT_URL=http://127.0.0.1:18080 \
       PARSAR_DAEMON_CONNECT_TOKEN=<一次性 token> \
       PARSAR_DAEMON_CONNECT_DEVICE_NAME=<设备名> bash
   ```
   - server 地址由网页按 `PARSAR_PUBLIC_URL` 自动填好,**不用手改**。
   - 脚本会:从本机 server 下载对应平台 daemon → `chmod` → 后台 `connect`;token 走环境变量,不进命令行 argv,因此不落到 `ps` 输出或访问日志。你**全程不碰二进制、路径、token**。
5. 几秒后该设备从 `pending_pairing` → **「在线 / online」**。**E2E 通过。**
   想再确认能干活:建一个 Agent、跑通一个 issue。

---

## 5 · 关停 / 清理

```bash
docker compose -f docker-compose.local.yml down       # 停,保留数据卷
docker compose -f docker-compose.local.yml down -v    # 连 Postgres 数据一起删
```

---

## 排错

| 现象 | 原因 | 处理 |
|---|---|---|
| 接入设备时下载 daemon 报 **404** | 用的 GHCR 镜像不含内置 daemon(尚未发布) | 走第 2 步的本地构建,`export PARSAR_SERVER_IMAGE=parsar:local` 后 `up -d --force-recreate` |
| 端口报 `address already in use` | 18080 / 15432 被占 | 改 `PARSAR_LOCAL_PORT` / `PARSAR_PG_PORT` 后重新 `up -d` |
| `server` 崩溃循环 `agent_daemon owner URL not resolvable` | 缺 `PARSAR_AGENT_DAEMON_OWNER_URL`(本地 compose 已设默认 `http://parsar-server:8080`) | 若你改/删过该 env,补回即可 |
| `server` 一直 unhealthy 但其实能访问 | 镜像内置 HEALTHCHECK 用 HEAD,`/healthz` 仅接受 GET(本地 compose 已覆盖为 GET 探针) | 若你改过 compose,确认 healthcheck 用 GET |
| 设备在线但创建 Agent 时无可用 kind | 本机 Agent CLI 没装 / 没登录 | 在本机 `claude` / `opencode` / `codex` 登录后,daemon 会自动识别 |
| macOS `exec format error` | 在 x86 上构建、Apple Silicon 上运行 | 本机 `make docker-build` 重新构建为原生架构 |

查看日志:`docker logs -f parsar-local-server`

---

## TL;DR

```bash
git clone <repo> parsar && cd parsar && git checkout feature/deliverables-design
export PARSAR_LOCAL_PORT=18080 PARSAR_PG_PORT=15432
# 当前阶段(GHCR 尚无内置 daemon 镜像)→ 本地构建;发布后这两行可删,直接用默认镜像
make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=local
export PARSAR_SERVER_IMAGE=parsar:local
docker compose -f docker-compose.local.yml up -d
# 浏览器 http://127.0.0.1:18080 → 登录 → 接入新设备 → 复制命令 → 另一个终端跑 → 设备在线
```
