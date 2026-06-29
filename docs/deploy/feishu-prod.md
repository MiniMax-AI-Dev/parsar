# Feishu 生产登录与事件订阅部署指南

本文面向把 Parsar 接入真实飞书租户的生产部署。目标是让登录、回调、事件订阅和 Cookie 会话都在 HTTPS 下可验证、可排错。

## 1. 飞书开放平台配置

1. 登录飞书开放平台，创建企业自建应用。
2. 记录应用凭证：`App ID` 与 `App Secret`。
3. 在「权限管理」申请并发布以下 OAuth scope：
   - `contact:user.id:readonly`
   - `contact:user.base:readonly`
   - `contact:user.email:readonly`
4. 在「安全设置 / 重定向 URL」配置 Parsar 回调地址：
   - `https://<your-domain>/api/v1/auth/feishu/callback`
5. 发布应用或让管理员完成权限审批。缺少 email scope 时，登录会在回调阶段失败。

## 2. 事件订阅配置

1. 打开应用「事件订阅」。
2. 请求地址填写：
   - `https://<your-domain>/api/v1/feishu/events/message`
3. 配置 Verification Token，并同步到 Parsar：
   - `PARSAR_FEISHU_VERIFICATION_TOKEN=<same-token>`
4. 如开启事件加密，记录 Encrypt Key，并同步到 Parsar：
   - `PARSAR_FEISHU_ENCRYPT_KEY=<encrypt-key>`
5. 在飞书后台点「保存」时，飞书会发送 URL Challenge。Parsar 验 token 后会返回：
   - `{"challenge":"..."}`
6. 订阅消息事件，例如接收群消息 / 被 @ 消息。具体事件名称按飞书后台当前 UI 为准。

## 3. Parsar 环境变量清单

### 3.1 生产必需

```bash
DATABASE_URL=postgres://...
PARSAR_MASTER_KEY=<32+ chars random secret>
PARSAR_COOKIE_SECURE=true

PARSAR_FEISHU_APP_ID=cli_xxx
PARSAR_FEISHU_APP_SECRET=xxx
PARSAR_FEISHU_REDIRECT_URI=https://<your-domain>/api/v1/auth/feishu/callback
PARSAR_FEISHU_VERIFICATION_TOKEN=<token from Feishu event subscription>
```

未设置 `PARSAR_FEISHU_MOCK=true` 时，以上 Feishu OAuth 与 Verification Token 缺一项都会让 server 启动失败，避免生产环境静默缺路由。

### 3.2 生产可选

```bash
PARSAR_ADDR=:8080
PARSAR_LOGIN_REDIRECT_URL=https://<your-domain>/
PARSAR_FEISHU_SCOPE="contact:user.id:readonly contact:user.base:readonly contact:user.email:readonly"
PARSAR_FEISHU_AUTHORIZE_BASE=https://accounts.feishu.cn
PARSAR_FEISHU_API_BASE=https://open.feishu.cn
PARSAR_FEISHU_ENCRYPT_KEY=<only required when Feishu event encryption is enabled>

# Agent Bot chat loop (QR-provisioned Bot apps)
PARSAR_FEISHU_WEBSOCKET=true
PARSAR_FEISHU_OUTBOUND=true
PARSAR_FEISHU_WS_REFRESH_SECONDS=30
PARSAR_FEISHU_OPENAPI_BASE_URL=https://open.feishu.cn
```

### 3.3 仅本地开发

```bash
PARSAR_FEISHU_MOCK=true
PARSAR_FEISHU_MOCK_EMAIL=admin@example.com
PARSAR_FEISHU_MOCK_NAME="Dev Admin"
PARSAR_FEISHU_MOCK_UNION_ID=on_mock_union_admin
PARSAR_FEISHU_MOCK_OPEN_ID=ou_feishu_admin
PARSAR_DEV_AUTH=true
PARSAR_COOKIE_SECURE=false
```

Mock 模式会使用 MockClient，并跳过 Feishu webhook token 验证，仅用于本地 e2e / 开发。

## 4. HTTPS 反向代理示例

Parsar 自身**不内置**反代:compose 只把 server 绑到 `127.0.0.1`,由你在前面放一层 nginx / Caddy / Cloudflare Tunnel 终止 TLS。反代除了常规 HTTP,还必须正确转发两类长连接,否则「网页一行命令接入设备」会失败:

- **WebSocket** `/agent-daemon/ws` —— 宿主机 daemon 出站拨入用的长连接(`Upgrade`/`Connection` 头 + 长读超时)。
- **SSE** `/api/v1/...` —— agent 运行时的流式输出(必须关闭代理缓冲,否则前端看不到增量)。

### 4.1 Nginx

```nginx
map $http_upgrade $connection_upgrade {
  default upgrade;
  ''      close;
}

server {
  listen 443 ssl http2;
  server_name parsar.example.com;

  ssl_certificate /etc/letsencrypt/live/parsar.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/parsar.example.com/privkey.pem;

  # daemon dial-in WebSocket。必须升级协议,并把读超时拉长到远超
  # 心跳间隔,否则空闲连接会被 nginx 默认 60s 掐断。
  location /agent-daemon/ws {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
  }

  # agent 流式输出 (SSE)。关闭缓冲,让 token 实时下发。
  location /api/v1/ {
    proxy_pass http://127.0.0.1:8080;
    proxy_http_version 1.1;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_buffering off;
    proxy_cache off;
    proxy_read_timeout 3600s;
  }

  location / {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto https;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  }
}
```

### 4.2 Caddy

Caddy 的 `reverse_proxy` 默认就会处理 WebSocket 升级,只需为 SSE 关闭响应缓冲(`flush_interval -1` 表示每次写都立即 flush)。

```caddyfile
parsar.example.com {
  reverse_proxy 127.0.0.1:8080 {
    header_up X-Forwarded-Proto https
    # SSE:禁用缓冲,逐字节 flush。WebSocket 升级 Caddy 自动处理。
    flush_interval -1
  }
}
```

### 4.3 Cloudflare Tunnel

不想在公网暴露入站端口时,用 `cloudflared` 把本机 `127.0.0.1:8080` 反向打洞到一个 Cloudflare 托管域名。Cloudflare 边缘默认支持 WebSocket 与 SSE,无需额外缓冲配置。

```yaml
# ~/.cloudflared/config.yml
tunnel: <tunnel-uuid>
credentials-file: /root/.cloudflared/<tunnel-uuid>.json

ingress:
  - hostname: parsar.example.com
    service: http://127.0.0.1:8080
    originRequest:
      # SSE / WS 是长连接,关掉空闲超时(默认 100s 会掐断)。
      connectTimeout: 30s
      noHappyEyeballs: false
  - service: http_status:404
```

```bash
cloudflared tunnel route dns <tunnel-uuid> parsar.example.com
cloudflared tunnel run <tunnel-uuid>
```

### 4.4 公网 URL 与 Host 头(安全)

无论用哪种反代,**铸「一行接入命令」的唯一可信来源是 server 自己的 `PARSAR_PUBLIC_URL`**,而不是请求里的 `Host` / `X-Forwarded-Host` 头——后者由客户端控制,可被伪造成攻击者地址,从而把别人的 daemon 配对引流走。所以:

- 必须显式设置 `PARSAR_PUBLIC_URL=https://parsar.example.com`(与上面反代的 `server_name` / hostname 一致),server 据此回填网页里的一行命令与回调地址。
- 反代照常透传 `Host` / `X-Forwarded-*` 用于日志和常规路由即可;Parsar 在铸命令时**不读这些头**(见 `bootstrap.WithPublicURL`),因此即使头被伪造也不影响接入命令的正确性。

生产必须保持 `PARSAR_COOKIE_SECURE=true`。如果生产模式下设置为 false，server 会启动但打印 `running prod auth on HTTP — cookies will leak` 警告。

## 5. 启动与健康检查

```bash
export PARSAR_COOKIE_SECURE=true
export PARSAR_FEISHU_APP_ID=cli_xxx
export PARSAR_FEISHU_APP_SECRET=xxx
export PARSAR_FEISHU_REDIRECT_URI=https://parsar.example.com/api/v1/auth/feishu/callback
export PARSAR_FEISHU_VERIFICATION_TOKEN=xxx

parsar-server
```

检查 server 健康：

```bash
curl -i https://parsar.example.com/api/v1/health
```

期望：`200`，body 包含 `{"status":"ok","name":"parsar"}`。

检查会话保护是否生效：

```bash
curl -i https://parsar.example.com/api/v1/me
```

未登录时应返回 `401`。如果返回业务数据，说明认证中间件或代理路径配置有误。

检查 Feishu Challenge：

```bash
curl -i https://parsar.example.com/api/v1/feishu/events/message \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"hello","token":"<verification-token>"}'
```

期望：`200` 且 body 为 `{"challenge":"hello"}`。

检查 token 拦截：

```bash
curl -i https://parsar.example.com/api/v1/feishu/events/message \
  -H 'Content-Type: application/json' \
  -d '{"event":{"message":{"message_id":"om"}}}'
```

期望：`401`。

## 6. 排错

### 6.1 `/api/v1/feishu/events/message` 返回 401

- 请求体缺 `token` 字段。
- 飞书后台 Verification Token 与 `PARSAR_FEISHU_VERIFICATION_TOKEN` 不一致。
- 生产 env 未重启生效。

### 6.2 `/api/v1/feishu/events/message` 返回 400

- JSON 不合法。
- 开启了飞书事件加密，但未配置 `PARSAR_FEISHU_ENCRYPT_KEY`。
- Encrypt Key 与飞书后台不一致，导致解密失败。
- 解密后事件结构不是 Parsar 当前支持的消息事件结构。

### 6.3 登录后 redirect 不对

- 检查飞书后台 Redirect URL 是否完全等于 `PARSAR_FEISHU_REDIRECT_URI`。
- 检查 `PARSAR_LOGIN_REDIRECT_URL` 是否指向用户实际访问的 Web origin。
- 代理层不要改写 `/api/v1/auth/feishu/callback` 路径。

### 6.4 Cookie 不生效 / 登录后仍 401

- 生产设置 `PARSAR_COOKIE_SECURE=true`，并确保用户通过 HTTPS 访问。
- 域名必须与用户访问域一致，避免 callback 在 A 域、页面在 B 域。
- 浏览器开发者工具检查 `Set-Cookie` 是否被 Secure / SameSite / domain 策略拦截。

### 6.5 server 启动失败

- 如果错误提示要求 `PARSAR_FEISHU_APP_ID/APP_SECRET/REDIRECT_URI` 和 Verification Token，说明当前是生产模式。
- 本地开发请显式设置 `PARSAR_FEISHU_MOCK=true`。
- 生产不要用 mock；补齐 env 后重启。
