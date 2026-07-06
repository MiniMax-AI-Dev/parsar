# Parsar 局域网部署指南 — 多人可用的完整服务

> **面向:** 想在一台开发机 / 内网服务器上部署 Parsar，让局域网内**多人**通过浏览器访问、在飞书群里 @Bot 对话、接入设备的团队。
> **与 INSTALL.md 的区别:** INSTALL.md 是 127.0.0.1 单机自用 + mock 登录；本文档是 0.0.0.0 绑定 + 真实飞书 OAuth + 飞书 Bot + 云端沙箱 + 局域网可达。
> **前提假设:** 一台 Linux 机器（也适用于 macOS），有 Docker + Docker Compose v2，能出网。

---

## 总览

```
克隆仓库 → 飞书开放平台建应用 → 准备 .env → 构建镜像(server + sandbox)
→ docker compose up → 首人飞书登录(TOFU Owner) → 创建 Agent
→ 接入设备 / 飞书群 @Bot 对话
```

部署完成后的能力矩阵：

| 能力 | 说明 |
|---|---|
| Web 管理后台 | 多人飞书登录，管理 Agent / 设备 / Workspace |
| 设备接入 | 局域网内用户一行命令把本机 Claude Code / Codex 接入 |
| 云端沙箱 (Docker) | Agent 自动在 Docker 容器中运行，内置 Claude Code + Codex |
| 飞书 Bot | 群聊 @Bot / 私聊 Bot 触发 Agent 执行，结果回复到飞书 |

---

## 1. 前置条件

```bash
docker compose version   # v2.x+
docker info              # 确认 daemon 在运行
```

- 确认部署机器的**内网 IP**（后续以 `YOUR_IP` 代称）：
  ```bash
  hostname -I | awk '{print $1}'
  ```
- 确认端口 `18080`（或你选的端口）未被占用且防火墙放行。
- 如果机器需要通过 HTTP 代理访问外网，记下代理地址（后续以 `YOUR_PROXY` 代称）。
- 确认 Docker socket 的 GID：
  ```bash
  stat -c '%g' /var/run/docker.sock   # 通常是 999 或 docker
  ```

---

## 2. 飞书开放平台配置

> 一个飞书应用同时承担 OAuth 登录和 Bot 对话两个角色。如果你的团队使用 Lark（海外版），流程相同，域名换成 `open.larksuite.com`。

### 2.1 创建应用

1. 登录 [飞书开放平台](https://open.feishu.cn) → 创建**企业自建应用**。
2. **凭证与基本信息**页面，记下：
   - `App ID`（形如 `cli_xxxxxxxxxx`）
   - `App Secret`
   - `Verification Token`
   - Bot 的 `Open ID`（形如 `ou_xxxxxxxx`，开启机器人能力后可见）

### 2.2 配置重定向 URL

**安全设置** → **重定向 URL**，添加：
```
http://YOUR_IP:18080/api/v1/auth/feishu/callback
```

### 2.3 申请权限 (scope)

**权限管理** → 申请以下 scope 并由管理员审批：

| Scope | 用途 |
|---|---|
| `contact:user.base:readonly` | 读取用户基本信息（登录） |
| `contact:user.email:readonly` | 读取用户邮箱（登录） |
| `im:message` | 接收 IM 消息事件（Bot） |
| `im:message.group_at_msg:readonly` | 接收群 @Bot 消息（Bot） |
| `im:message.p2p_msg:readonly` | 接收私聊消息（Bot） |
| `im:message:send_as_bot` | 以 Bot 身份发消息（Bot） |
| `im:chat:readonly` | 读群信息（Bot 出站需要） |

### 2.4 开启机器人能力

**应用能力 → 机器人** → 点击**开启**。

> 不开机器人能力的话 Bot 没法被加进群、也收不到 @Bot 消息。这是最常被漏的一步。

### 2.5 发布版本

**版本管理与发布** → 创建版本并发布 → 让管理员审批。**scope 不审批就不生效。**

---

## 3. 克隆代码

```bash
git clone <your-repo-url> parsar
cd parsar
```

---

## 4. 准备 `.env` 文件

在项目根目录创建 `.env`（已在 `.gitignore` 中，不会被提交）：

```bash
cp .env.example .env
```

编辑 `.env`，填入以下内容：

```bash
# ---- 飞书 OAuth + Bot ----
PARSAR_FEISHU_MOCK=false
PARSAR_FEISHU_APP_ID=cli_xxxxxxxxxx          # 2.1 节记下的 App ID
PARSAR_FEISHU_APP_SECRET=xxxxxxxx            # 2.1 节记下的 App Secret
PARSAR_FEISHU_REDIRECT_URI=http://YOUR_IP:18080/api/v1/auth/feishu/callback
PARSAR_FEISHU_VERIFICATION_TOKEN=xxxxxxxx    # 2.1 节记下的 Verification Token
PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID=ou_xxxxxx  # 2.1 节记下的 Bot Open ID

# ---- 安全 ----
PARSAR_MASTER_KEY=<openssl rand -hex 32 生成>
PARSAR_COOKIE_SECURE=false                   # HTTP 部署必须 false；HTTPS 反代时改 true

# ---- 网络 ----
PARSAR_HOST_IP=YOUR_IP                       # 你的内网 IP，不填则默认 127.0.0.1（单机）
PARSAR_LOCAL_PORT=18080
PARSAR_PG_PORT=15432

# ---- 镜像 ----
PARSAR_SERVER_IMAGE=parsar:local

# ---- Docker sandbox ----
DOCKER_GID=999                               # stat -c '%g' /var/run/docker.sock 的值

# ---- 代理（可选，仅需要代理才能出网的机器） ----
# HTTP_PROXY=http://your-proxy:port
# HTTPS_PROXY=http://your-proxy:port
```

> **PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID** 必须填。不填的话群聊 @Bot 会被静默跳过——server 无法识别 mention 列表里哪个是 Bot 自己，会丢弃所有群消息。私聊不受影响。

---

## 5. 构建镜像

需要构建两个镜像：**server 镜像**（服务本体）和 **sandbox 镜像**（Agent 运行的沙箱容器）。

### 5.1 构建 server 镜像

```bash
# 需要代理时（从 .env 读取）：
source .env
sudo docker build \
  -t parsar:local \
  --build-arg http_proxy="$HTTP_PROXY" \
  --build-arg https_proxy="$HTTPS_PROXY" \
  .

# 无需代理时：
sudo docker build -t parsar:local .
```

构建约 5–10 分钟。验证：

```bash
sudo docker run --rm --entrypoint ls parsar:local /usr/local/share/parsar/daemon
# 预期：parsar-daemon-darwin-amd64  parsar-daemon-darwin-arm64
#       parsar-daemon-linux-amd64   parsar-daemon-linux-arm64
```

### 5.2 构建 sandbox 镜像

sandbox 镜像是 Agent 以 Docker 沙箱模式运行时启动的容器，内含 Claude Code + Codex + parsar-daemon。

```bash
# 需要代理时（从 .env 读取）：
source .env
sudo docker build \
  -f infra/sandbox/Dockerfile.local \
  -t parsar-sandbox:local \
  --build-arg http_proxy="$HTTP_PROXY" \
  --build-arg https_proxy="$HTTPS_PROXY" \
  .

# 无需代理时：
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .
```

> `Dockerfile.local` 从 server 镜像中拷贝 daemon 二进制，从 CDN 下载 Claude Code CLI，无需 GitHub Release。**必须先完成 5.1 再跑 5.2**。

验证：

```bash
sudo docker run --rm --entrypoint /bin/sh parsar-sandbox:local \
  -c "claude --version && parsar-daemon version && codex --version"
# 预期：三个版本号都正常输出
```

---

## 6. 启动服务栈

```bash
sudo docker compose -f docker-compose.local.yml up -d
```

**启动顺序（自动）：**
1. `postgres` — PostgreSQL 16，等待 healthcheck 通过
2. `parsar-init` — 数据库迁移 + 首次引导（TOFU 模式，跳过 bootstrap）
3. `parsar-server` — 绑定 `0.0.0.0:18080`，飞书 WebSocket 入站 + 出站 worker 自动启动

**验证：**

```bash
# 容器状态
sudo docker compose -f docker-compose.local.yml ps

# 健康检查
curl -s http://YOUR_IP:18080/healthz    # 200
curl -s http://YOUR_IP:18080/readyz     # 200

# Bootstrap 状态
curl -s http://YOUR_IP:18080/api/v1/bootstrap/status
# 首次：{"needed":true,"has_owners":false,...}

# 飞书 Bot 连接确认
sudo docker logs parsar-local-server 2>&1 | grep "feishu.*inbound.*ready"
# 预期：feishu websocket inbound client ready
```

---

## 7. 首次登录 — Owner 认领 (TOFU)

1. 浏览器打开 `http://YOUR_IP:18080`。
2. 点击**飞书登录** → 飞书授权页 → 用你的飞书账号登录。
3. 首次登录的用户**自动成为 Workspace Owner**。

> **确保你本人是第一个登录的人。** TOFU 不可逆——首个登录者即 Owner。

---

## 8. 创建 Agent 并验证

### 8.1 Web 界面创建 Agent

1. 登录后进入管理界面 → **Agents** → **新建 Agent**。
2. 选择 connector 类型 `agent_daemon`，daemon mode 选 `sandbox`。
3. 保存后 server 自动为该 Agent 启动一个 Docker 沙箱容器（约 10 秒）。

### 8.2 飞书 Bot 绑定

1. 进入 Agent 详情页 → **Connector** tab → **飞书 Bot 绑定**卡片。
2. 选择**「默认 Bot」** → 保存。

### 8.3 群聊验证

1. 在飞书中把 Bot 加进一个群 → 群里 **@Bot** 发消息。
2. Parsar 收到消息 → 触发 Agent run → Bot 在群里回复。

### 8.4 私聊验证

1. 飞书中直接搜 Bot 名 → 发私聊消息。
2. Bot 回复。

### 8.5 设备接入验证

1. Web 界面 → **设备管理** → **接入新设备** → 填设备名 → **生成连接命令**。
2. 在你的机器终端粘贴执行。几秒后设备状态变为**在线**。

> 接入设备的机器上必须已安装并登录至少一个 Agent CLI（`claude` / `opencode` / `codex`）。

---

## 9. 运维

### 查看日志

```bash
sudo docker logs -f parsar-local-server    # server 日志
sudo docker logs parsar-local-init         # 迁移日志
sudo docker logs parsar-local-postgres     # 数据库日志
```

### 停止 / 清理

```bash
sudo docker compose -f docker-compose.local.yml down       # 停止，保留数据
sudo docker compose -f docker-compose.local.yml down -v    # 连数据卷一起删
```

### 更新版本

```bash
git pull
sudo docker build -t parsar:local .
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .
sudo docker compose -f docker-compose.local.yml up -d --force-recreate
```

### 修改端口

编辑 `.env` 中的 `PARSAR_LOCAL_PORT`，**同时更新**：
- `PARSAR_FEISHU_REDIRECT_URI` 中的端口
- 飞书开放平台上配置的重定向 URL

然后 `sudo docker compose -f docker-compose.local.yml up -d --force-recreate`。

---

## 网络代理

如果部署机需要通过 HTTP 代理才能访问外网（飞书 API、下载依赖），在 `.env` 中取消注释并填入代理地址：

```bash
HTTP_PROXY=http://your-proxy:port
HTTPS_PROXY=http://your-proxy:port
```

`docker-compose.local.yml` 会自动读取这些变量并传给容器。不需要代理的机器留空即可。

构建镜像时也要传代理参数（见第 5 节的 `--build-arg`），因为 `docker build` 不会读取 `.env`。

---

## 排错

| 现象 | 原因 | 处理 |
|---|---|---|
| 飞书登录报 `redirect_uri mismatch` | `.env` 的 `REDIRECT_URI` 与飞书开放平台配置不一致 | 两边保持完全一致（协议、IP、端口、路径） |
| 其他机器无法访问 18080 | 防火墙未放行 | `sudo ufw allow 18080/tcp` 或对应防火墙规则 |
| 接入设备下载 daemon 报 **404** | 用的 GHCR 镜像不含 daemon | 本地构建 server 镜像（5.1 节） |
| Agent 报 **"no runtime yet — ask an admin to rebuild it"** | sandbox 镜像缺少 Agent CLI | 用 `Dockerfile.local` 重新构建 sandbox 镜像（5.2 节），然后 UI 里点 Rebuild |
| 群聊 @Bot **没反应**，私聊正常 | `PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID` 没填 | `.env` 加上 Bot 的 open_id 后重启 server |
| Bot 收到消息但不回复 | 出站 worker 没起来 | 检查 server 日志中 `feishu outbound` 字样；确认 `PARSAR_FEISHU_OUTBOUND=true`（compose 中已设） |
| Server 循环重启 `owner URL not resolvable` | `PARSAR_AGENT_DAEMON_OWNER_URL` 缺失 | 确认 compose 中有 `PARSAR_AGENT_DAEMON_OWNER_URL: "http://parsar-server:8080"` |
| Docker build 时 `go mod download` 超时 | 机器无法直接出网 | 构建时加 `--build-arg http_proxy=...` `--build-arg https_proxy=...` |
| Server unhealthy 但实际可访问 | 内置 HEALTHCHECK 用 HEAD，`/healthz` 只接受 GET | 确认 compose 中 healthcheck 已覆盖为 GET 探针（已默认） |

---

## 架构概览

```
┌─────────────────────────────────────────────────────────────────┐
│                        部署机 (YOUR_IP)                          │
│                                                                 │
│  ┌────────────┐  ┌────────────┐  ┌─────────────────────────┐   │
│  │ PostgreSQL  │  │ parsar-init│  │     parsar-server       │   │
│  │ :5432       │  │ (一次性)   │  │  SPA + API + WS          │   │
│  │ 数据持久卷  │  │ 迁移+引导  │  │  飞书 WS 入站 + 出站      │   │
│  └────────────┘  └────────────┘  │  Docker sandbox 管理      │   │
│                                  └─────────┬───────────────┘   │
│                                            │                   │
│  ┌──────────────────────┐        0.0.0.0:18080                 │
│  │  sandbox 容器 (按需)   │                 │                   │
│  │  Claude Code + Codex  │                 │                   │
│  │  parsar-daemon        │                 │                   │
│  └──────────────────────┘                  │                   │
└────────────────────────────────────────────┼───────────────────┘
                                             │
          ┌──────────────────────────────────┼─────────────┐
          │              局域网 / 飞书         │             │
          │                                  │             │
          │   用户浏览器 ────── HTTP ─────────┘             │
          │   用户设备 ──────── WebSocket ────┘             │
          │   飞书群/私聊 ───── 飞书 WS ──────┘             │
          └────────────────────────────────────────────────┘
```

---

## TL;DR

```bash
git clone <repo> parsar && cd parsar

# 1. 配置 .env
cp .env.example .env
vim .env   # 填入飞书凭证 + master key + PARSAR_HOST_IP + Bot Open ID

# 2. 构建镜像
sudo docker build -t parsar:local .
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .

# 3. 启动
sudo docker compose -f docker-compose.local.yml up -d

# 4. 验证
curl http://YOUR_IP:18080/healthz   # 200
curl http://YOUR_IP:18080/readyz    # 200

# 5. 浏览器 http://YOUR_IP:18080 → 飞书登录(首人=Owner)
#    → 创建 Agent(sandbox 模式) → 绑定默认 Bot
#    → 飞书群 @Bot 对话 / 接入设备 → 跑通
```
