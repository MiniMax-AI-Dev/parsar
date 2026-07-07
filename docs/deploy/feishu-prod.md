# Feishu production login and event-subscription deployment guide

For production deployments that integrate Parsar with a real Feishu tenant.
The goal is to make login, callbacks, event subscriptions, and cookie
sessions all verifiable and debuggable under HTTPS.

## 1. Feishu Open Platform configuration

1. Log in to the Feishu Open Platform and create a custom app.
2. Record the app credentials: `App ID` and `App Secret`.
3. In "Permission Management" request and publish the following OAuth scopes:
   - `contact:user.id:readonly`
   - `contact:user.base:readonly`
   - `contact:user.email:readonly`
4. In "Security Settings / Redirect URL" configure the Parsar callback URL:
   - `https://<your-domain>/api/v1/auth/feishu/callback`
5. Publish the app or have an admin complete the permission approval.
   Without the email scope, login fails in the callback phase.

## 2. Event-subscription configuration

1. Open the app's "Event Subscription" page.
2. Fill in the request URL:
   - `https://<your-domain>/api/v1/feishu/events/message`
3. Configure the Verification Token and mirror it into Parsar:
   - `PARSAR_FEISHU_VERIFICATION_TOKEN=<same-token>`
4. If event encryption is enabled, record the Encrypt Key and mirror it into
   Parsar:
   - `PARSAR_FEISHU_ENCRYPT_KEY=<encrypt-key>`
5. When you click Save in the Feishu console, Feishu sends a URL Challenge.
   Once Parsar validates the token, it returns:
   - `{"challenge":"..."}`
6. Subscribe to message events (e.g. group message received / @-message
   events). Exact event names follow the current Feishu console UI.

## 3. Parsar env-variable checklist

### 3.1 Required in production

```bash
DATABASE_URL=postgres://...
PARSAR_MASTER_KEY=<32+ chars random secret>
PARSAR_COOKIE_SECURE=true

PARSAR_FEISHU_APP_ID=cli_xxx
PARSAR_FEISHU_APP_SECRET=xxx
PARSAR_FEISHU_REDIRECT_URI=https://<your-domain>/api/v1/auth/feishu/callback
PARSAR_FEISHU_VERIFICATION_TOKEN=<token from Feishu event subscription>
```

Without `PARSAR_FEISHU_MOCK=true`, any missing item above causes the server
to fail to start, which prevents silently missing routes in production.

### 3.2 Optional in production

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

### 3.3 Local development only

```bash
PARSAR_FEISHU_MOCK=true
PARSAR_FEISHU_MOCK_EMAIL=admin@example.com
PARSAR_FEISHU_MOCK_NAME="Dev Admin"
PARSAR_FEISHU_MOCK_UNION_ID=on_mock_union_admin
PARSAR_FEISHU_MOCK_OPEN_ID=ou_feishu_admin
PARSAR_DEV_AUTH=true
PARSAR_COOKIE_SECURE=false
```

Mock mode uses MockClient and skips Feishu webhook token verification — for
local e2e / development only.

## 4. HTTPS reverse-proxy examples

Parsar itself does **not** ship a reverse proxy: compose binds the server to
`127.0.0.1`, and you place nginx / Caddy / Cloudflare Tunnel in front to
terminate TLS. Beyond regular HTTP, the reverse proxy must correctly forward
two kinds of long-lived connections, otherwise the "one command in the web
UI pairs a device" flow breaks:

- **WebSocket** `/agent-daemon/ws` — the dial-in long connection used by the host daemon (`Upgrade` / `Connection` headers + long read timeout).
- **SSE** `/api/v1/...` — the Agent runtime's streaming output (must disable proxy buffering, otherwise the frontend never sees incremental output).

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

  # daemon dial-in WebSocket. Must upgrade the protocol and stretch the read
  # timeout well beyond the heartbeat interval, otherwise nginx's default
  # 60s idle timeout will cut idle connections.
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

  # Agent streaming output (SSE). Disable buffering so tokens flush in real time.
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

Caddy's `reverse_proxy` handles WebSocket upgrades out of the box; you only
need to disable response buffering for SSE (`flush_interval -1` flushes on
every write).

```caddyfile
parsar.example.com {
  reverse_proxy 127.0.0.1:8080 {
    header_up X-Forwarded-Proto https
    # SSE: disable buffering, byte-level flush. WebSocket upgrade is automatic.
    flush_interval -1
  }
}
```

### 4.3 Cloudflare Tunnel

To avoid exposing inbound ports to the public internet, use `cloudflared` to
reverse-tunnel `127.0.0.1:8080` to a Cloudflare-managed hostname. Cloudflare
edges support WebSocket and SSE by default, no extra buffering config needed.

```yaml
# ~/.cloudflared/config.yml
tunnel: <tunnel-uuid>
credentials-file: /root/.cloudflared/<tunnel-uuid>.json

ingress:
  - hostname: parsar.example.com
    service: http://127.0.0.1:8080
    originRequest:
      # SSE / WS are long connections; disable idle timeout (the default 100s
      # would cut them).
      connectTimeout: 30s
      noHappyEyeballs: false
  - service: http_status:404
```

```bash
cloudflared tunnel route dns <tunnel-uuid> parsar.example.com
cloudflared tunnel run <tunnel-uuid>
```

### 4.4 Public URL and Host header (security)

Regardless of which reverse proxy you use, **the only trusted source for
minting the "one-line pair" command is the server's own
`PARSAR_PUBLIC_URL`** — not the request's `Host` / `X-Forwarded-Host`
headers. Those headers are client-controlled and can be forged into an
attacker address, which would redirect somebody else's daemon pairing to
the attacker. Therefore:

- You must explicitly set `PARSAR_PUBLIC_URL=https://parsar.example.com`
  (matching the reverse proxy's `server_name` / hostname). The server uses
  this value to fill in the one-line command and callback URL.
- The reverse proxy still forwards `Host` / `X-Forwarded-*` for logging and
  regular routing; Parsar does **not** read those headers when minting the
  command (see `bootstrap.WithPublicURL`), so header forgery does not
  compromise the pair command's correctness.

Production must keep `PARSAR_COOKIE_SECURE=true`. If it is set to false in
production, the server still starts but prints the warning `running prod
auth on HTTP — cookies will leak`.

## 5. Startup and health checks

```bash
export PARSAR_COOKIE_SECURE=true
export PARSAR_FEISHU_APP_ID=cli_xxx
export PARSAR_FEISHU_APP_SECRET=xxx
export PARSAR_FEISHU_REDIRECT_URI=https://parsar.example.com/api/v1/auth/feishu/callback
export PARSAR_FEISHU_VERIFICATION_TOKEN=xxx

parsar-server
```

Check server health:

```bash
curl -i https://parsar.example.com/api/v1/health
```

Expected: `200`, body contains `{"status":"ok","name":"parsar"}`.

Check that session protection is enforced:

```bash
curl -i https://parsar.example.com/api/v1/me
```

Should return `401` when unauthenticated. If it returns business data, the
auth middleware or the proxy path is misconfigured.

Check the Feishu Challenge:

```bash
curl -i https://parsar.example.com/api/v1/feishu/events/message \
  -H 'Content-Type: application/json' \
  -d '{"type":"url_verification","challenge":"hello","token":"<verification-token>"}'
```

Expected: `200`, body is `{"challenge":"hello"}`.

Check token enforcement:

```bash
curl -i https://parsar.example.com/api/v1/feishu/events/message \
  -H 'Content-Type: application/json' \
  -d '{"event":{"message":{"message_id":"om"}}}'
```

Expected: `401`.

## 6. Troubleshooting

### 6.1 `/api/v1/feishu/events/message` returns 401

- Missing `token` field in the request body.
- Feishu console Verification Token does not match `PARSAR_FEISHU_VERIFICATION_TOKEN`.
- Production env was updated but the server was not restarted.

### 6.2 `/api/v1/feishu/events/message` returns 400

- Malformed JSON.
- Feishu event encryption is enabled but `PARSAR_FEISHU_ENCRYPT_KEY` is not configured.
- The Encrypt Key does not match the Feishu console, so decryption fails.
- After decryption, the event structure is not one of the message event structures Parsar currently supports.

### 6.3 Login lands on the wrong redirect

- Check that the Feishu console Redirect URL is exactly equal to `PARSAR_FEISHU_REDIRECT_URI`.
- Check that `PARSAR_LOGIN_REDIRECT_URL` points to the web origin the user actually visits.
- The proxy layer must not rewrite `/api/v1/auth/feishu/callback`.

### 6.4 Cookies do not stick / still 401 after login

- Production must set `PARSAR_COOKIE_SECURE=true` and users must access over HTTPS.
- The domain must match the user's browser domain (avoid callback on domain A while the page is on domain B).
- Use browser DevTools to check whether `Set-Cookie` is being blocked by Secure / SameSite / domain rules.

### 6.5 Server fails to start

- If the error demands `PARSAR_FEISHU_APP_ID/APP_SECRET/REDIRECT_URI` and Verification Token, you are in production mode.
- For local development, explicitly set `PARSAR_FEISHU_MOCK=true`.
- Do not use mock in production; supply the env vars and restart.
