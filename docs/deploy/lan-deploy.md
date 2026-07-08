# Parsar LAN deployment guide — full service for multiple users

> **Audience:** teams that want to deploy Parsar on a developer machine / internal server so that **multiple people** on the LAN can access it through the browser, chat with @Bot in Feishu groups, and pair devices.
> **How this differs from INSTALL.md:** INSTALL.md targets 127.0.0.1 single-user + mock login; this document targets 0.0.0.0 binding + real Feishu OAuth + Feishu Bot + cloud sandbox + LAN reachability.
> **Assumptions:** one Linux machine (also works on macOS), Docker + Docker Compose v2, internet access.

---

## ⚠️  Trust boundary and security assumptions

This document assumes **the network the deploy machine sits on is trusted**
(family / small-team office LAN, single-user dev machine behind VPN). This
compose is **not suitable** for multi-tenant LANs (school / big-corp networks,
public Wi-Fi) or for direct public-internet exposure.

Reasons:

- **The Docker socket is mounted into the server container** (`/var/run/docker.sock`) so it can spin up sandbox containers on demand. This is equivalent to giving the server root-level access to the host; any RCE inside the server becomes host compromise.
- **HTTP without TLS by default**: `PARSAR_COOKIE_SECURE=false` + 0.0.0.0 binding means anyone sniffing the same subnet can capture session cookies.
- **PARSAR_MASTER_KEY is the encryption root for every workspace credential** (Feishu / Slack / Discord bots): leaking it exposes every bot secret in the database in plaintext.

For production / multi-tenant / public-internet deployments, switch to the
K8s + envd e2b-sandbox path (out of scope for this document). This
document's target is "get it running on a trusted LAN to validate
functionality".

---

## Overview

```
clone the repo → create a Feishu app → prepare .env → build images (server + sandbox)
→ docker compose up → first user registers via web form (Owner) → create Agent
→ pair device / @Bot in Feishu groups
```

After deployment, the capability matrix:

| Capability | Description |
|---|---|
| Web admin | Multiple users log in with Feishu, manage Agents / Devices / Workspaces |
| Device pairing | LAN users pair their local Claude Code / Codex with a single command |
| Cloud sandbox (Docker) | Agents auto-run in Docker containers with Claude Code + Codex built in |
| Feishu Bot | Group @Bot / DM Bot triggers Agent runs; results reply into Feishu |

---

## 1. Prerequisites

```bash
docker compose version   # v2.x+
docker info              # confirm daemon is running
```

- Confirm the machine's **LAN IP** (referred to as `YOUR_IP` below):
  ```bash
  hostname -I | awk '{print $1}'
  ```
- Confirm port `18080` (or whichever port you pick) is free and open on the firewall.
- If this machine needs an HTTP proxy for internet access, note the proxy address (referred to as `YOUR_PROXY` below).
- Confirm the Docker socket GID (Linux only; macOS Docker Desktop has no host-side dockerd):
  ```bash
  # Linux
  stat -c '%g' /var/run/docker.sock   # usually 999 or the docker group
  # macOS: skip this step, keep DOCKER_GID=999 default
  ```

---

## 2. Feishu Open Platform configuration

> One Feishu app plays two roles: OAuth login and Bot chat. If your team uses Lark (overseas), the process is identical — swap the domain to `open.larksuite.com`.

### 2.1 Create the app

1. Log in to the [Feishu Open Platform](https://open.feishu.cn) → create a **custom app**.
2. On the **Credentials & Basic Info** page, note:
   - `App ID` (looks like `cli_xxxxxxxxxx`)
   - `App Secret`
   - `Verification Token`
   - The Bot `Open ID` (looks like `ou_xxxxxxxx`; visible after enabling the Bot capability)

### 2.2 Configure the redirect URL

**Security Settings** → **Redirect URL**, add:
```
http://YOUR_IP:18080/api/v1/auth/feishu/callback
```

### 2.3 Request permissions (scopes)

**Permission Management** → request the following scopes and get admin
approval:

| Scope | Purpose |
|---|---|
| `contact:user.base:readonly` | Read basic user info (login) |
| `contact:user.email:readonly` | Read user email (login) |
| `im:message` | Receive IM message events (Bot) |
| `im:message.group_at_msg:readonly` | Receive group @Bot messages (Bot) |
| `im:message.p2p_msg:readonly` | Receive DM messages (Bot) |
| `im:message:send_as_bot` | Send messages as the Bot (Bot) |
| `im:chat:readonly` | Read chat info (Bot outbound needs it) |

### 2.4 Enable the Bot capability

**App Capabilities → Bot** → click **Enable**.

> Without the Bot capability enabled, the Bot cannot be added to a group and no @Bot messages reach you. This is the most commonly missed step.

### 2.5 Publish a version

**Version Management & Release** → create and publish a version → get admin
approval. **Scopes do not take effect until approved.**

---

## 3. Clone the repo

```bash
git clone <your-repo-url> parsar
cd parsar
```

---

## 4. Prepare the `.env` file

Create `.env` in the project root (it is in `.gitignore` and will not be
committed):

```bash
cp .env.example .env
```

Edit `.env` and fill in:

```bash
# ---- Feishu OAuth + Bot ----
PARSAR_FEISHU_MOCK=false
PARSAR_FEISHU_APP_ID=cli_xxxxxxxxxx          # App ID from §2.1
PARSAR_FEISHU_APP_SECRET=xxxxxxxx            # App Secret from §2.1
PARSAR_FEISHU_REDIRECT_URI=http://YOUR_IP:18080/api/v1/auth/feishu/callback
PARSAR_FEISHU_VERIFICATION_TOKEN=xxxxxxxx    # Verification Token from §2.1
PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID=ou_xxxxxx  # Bot Open ID from §2.1

# ---- Security ----
# PARSAR_MASTER_KEY encrypts every Bot credential in the DB. Leave blank on
# first run — parsar-init in step 6 will auto-generate one on `up`, print it
# to the logs, and then fatal-exit; copy the printed key back into .env and
# run `up` again. You can also pre-generate:
#   echo "PARSAR_MASTER_KEY=$(openssl rand -hex 32)" >> .env
# Once set, DO NOT change it: any Bot credentials already stored become
# undecryptable and every Bot must be re-bound.
PARSAR_MASTER_KEY=
PARSAR_COOKIE_SECURE=false                   # must be false on HTTP; true when behind an HTTPS reverse proxy

# ---- Network ----
PARSAR_HOST_IP=YOUR_IP                       # your LAN IP; empty defaults to 127.0.0.1 (single machine)
PARSAR_LOCAL_PORT=18080
PARSAR_PG_PORT=15432

# ---- Image ----
PARSAR_SERVER_IMAGE=parsar:local

# ---- Docker sandbox ----
# Linux: stat -c '%g' /var/run/docker.sock       (usually 999 or docker)
# macOS Docker Desktop: no host-side dockerd; the socket goes through a vsock proxy — leave 999 (group_add is a no-op)
DOCKER_GID=999

# ---- Proxy (optional; only if this machine needs a proxy to reach the internet) ----
# HTTP_PROXY=http://your-proxy:port
# HTTPS_PROXY=http://your-proxy:port
```

> **PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID is required.** Without it, group @Bot messages are silently skipped — the server has no way to identify which entry in the mention list is the Bot itself and drops every group message. DMs are not affected.

---

## 5. Build the images

You need two images: the **server image** (the service itself) and the
**sandbox image** (the container Agents run in).

### 5.1 Build the server image

```bash
# With proxy (read from .env):
source .env
sudo docker build \
  -t parsar:local \
  --build-arg http_proxy="$HTTP_PROXY" \
  --build-arg https_proxy="$HTTPS_PROXY" \
  .

# Without proxy:
sudo docker build -t parsar:local .
```

Build takes ~5–10 minutes. Verify:

```bash
sudo docker run --rm --entrypoint ls parsar:local /usr/local/share/parsar/daemon
# Expected: parsar-daemon-darwin-amd64  parsar-daemon-darwin-arm64
#           parsar-daemon-linux-amd64   parsar-daemon-linux-arm64
```

### 5.2 Build the sandbox image

The sandbox image is the container Agents start when running in
Docker-sandbox mode; it contains Claude Code + Codex + parsar-daemon.

```bash
# With proxy (read from .env):
source .env
sudo docker build \
  -f infra/sandbox/Dockerfile.local \
  -t parsar-sandbox:local \
  --build-arg http_proxy="$HTTP_PROXY" \
  --build-arg https_proxy="$HTTPS_PROXY" \
  .

# Without proxy:
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .
```

> `Dockerfile.local` copies the daemon binary from the server image and
> downloads the Claude Code CLI from a CDN — no GitHub Release required.
> **You must complete 5.1 before running 5.2.**

Verify:

```bash
sudo docker run --rm --entrypoint /bin/sh parsar-sandbox:local \
  -c "claude --version && parsar-daemon version && codex --version"
# Expected: all three version strings print cleanly
```

---

## 6. Bring up the service stack

```bash
sudo docker compose -f docker-compose.local.yml up -d
```

**Startup order (automatic):**
1. `postgres` — PostgreSQL 16, wait for healthcheck.
2. `parsar-init` — master-key validation + database migration.
3. `parsar-server` — binds `0.0.0.0:18080`; Feishu WebSocket inbound + outbound worker start automatically.

> **First run will fatal-exit — that is intentional.**
> If you did not manually pre-fill `PARSAR_MASTER_KEY` in step 4, `parsar-init` generates one, prints it to its logs (`sudo docker logs parsar-local-init`), and then `parsar-server` refuses to start because env still has no key. **Copy the printed `PARSAR_MASTER_KEY=...` line from the init log into `.env`** and run `up -d` again to complete startup.
> On subsequent starts, if the key does not change, parsar-init only runs migrations and leaves the key alone.

**Verify:**

```bash
# Container status
sudo docker compose -f docker-compose.local.yml ps

# Health checks
curl -s http://YOUR_IP:18080/healthz    # 200
curl -s http://YOUR_IP:18080/readyz     # 200

# Bootstrap status
curl -s http://YOUR_IP:18080/api/v1/bootstrap/status
# First run: {"needed":true,"has_owners":false,...}

# Feishu Bot connection confirmation
sudo docker logs parsar-local-server 2>&1 | grep "feishu.*inbound.*ready"
# Expected: feishu websocket inbound client ready
```

---

## 7. First login — Register the Owner

1. Open `http://YOUR_IP:18080` in your browser.
2. The system detects no owner exists and shows the **registration form**.
3. Fill in your name, email, password, and workspace name → submit.
4. You are now the **Workspace Owner**.

---

## 8. Create an Agent and verify

### 8.1 Create the Agent in the web UI

1. After login, go to the admin UI → **Agents** → **New Agent**.
2. Pick connector type `agent_daemon`; daemon mode `sandbox`.
3. On save the server auto-starts a Docker sandbox container for the Agent (~10 seconds).

### 8.2 Bind the Feishu Bot

1. Go to the Agent detail page → **Connector** tab → **Feishu Bot binding** card.
2. Pick **"Default Bot"** → Save.

### 8.3 Group-chat verification

1. In Feishu, add the Bot to a group → **@Bot** with a message.
2. Parsar receives the message → triggers an Agent run → Bot replies in the group.

### 8.4 DM verification

1. In Feishu, search the Bot name directly → send a DM.
2. Bot replies.

### 8.5 Device-pairing verification

1. Web UI → **Device management** → **Pair new device** → enter a device name → **Generate connect command**.
2. Paste and run it in a terminal on your machine. After a few seconds the device status becomes **online**.

> The machine you are pairing must have at least one Agent CLI (`claude` / `opencode` / `codex`) installed and logged in.

---

## 9. Operations

### View logs

```bash
sudo docker logs -f parsar-local-server    # server logs
sudo docker logs parsar-local-init         # migration logs
sudo docker logs parsar-local-postgres     # DB logs
```

### Stop / clean up

```bash
sudo docker compose -f docker-compose.local.yml down       # stop, keep data
sudo docker compose -f docker-compose.local.yml down -v    # also delete data volumes
```

### Upgrade

```bash
git pull
sudo docker build -t parsar:local .
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .
sudo docker compose -f docker-compose.local.yml up -d --force-recreate
```

### Change port

Edit `PARSAR_LOCAL_PORT` in `.env` and **also update**:
- the port in `PARSAR_FEISHU_REDIRECT_URI`
- the redirect URL configured on the Feishu Open Platform

Then `sudo docker compose -f docker-compose.local.yml up -d --force-recreate`.

---

## Network proxy

If the deploy machine needs an HTTP proxy to reach the internet (Feishu API,
dependency downloads), uncomment and fill the proxy address in `.env`:

```bash
HTTP_PROXY=http://your-proxy:port
HTTPS_PROXY=http://your-proxy:port
```

`docker-compose.local.yml` reads these variables and passes them to the
container. Leave them blank on machines that do not need a proxy.

You must also pass the proxy args at image-build time (see the `--build-arg`
snippets in §5) because `docker build` does not read `.env`.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Server fatal-exits with `secret.master_key is required in production` | `.env` has an empty `PARSAR_MASTER_KEY` | Let `parsar-init` run once → copy the generated key from its logs into `.env` → `up -d` again |
| Feishu login reports `redirect_uri mismatch` | `.env`'s `REDIRECT_URI` does not match the Feishu console | Keep both sides completely identical (scheme, IP, port, path) |
| Other machines cannot reach 18080 | Firewall blocking it | `sudo ufw allow 18080/tcp` or the equivalent firewall rule |
| Device pairing downloads daemon and hits **404** | The GHCR image has no embedded daemon | Build the server image locally (§5.1) |
| Agent reports **"no runtime yet — ask an admin to rebuild it"** | The sandbox image lacks the Agent CLI | Rebuild the sandbox image via `Dockerfile.local` (§5.2), then click Rebuild in the UI |
| Sandbox comes up but the daemon cannot reach the server | Compose project name is not `parsar`, so network names do not match | Use the repo's `docker-compose.local.yml` (has `name: parsar` pinned at the top); do not override with `-p <other name>` |
| Group @Bot **no response**, DM works fine | `PARSAR_FEISHU_DEFAULT_BOT_OPEN_ID` not set | Add the Bot's open_id to `.env` and restart the server |
| Bot receives messages but does not reply | Outbound worker is not up | Grep server logs for `feishu outbound`; confirm `PARSAR_FEISHU_OUTBOUND=true` (compose already sets it) |
| Server crash-loops with `owner URL not resolvable` | `PARSAR_AGENT_DAEMON_OWNER_URL` missing | Confirm compose has `PARSAR_AGENT_DAEMON_OWNER_URL: "http://parsar-server:8080"` |
| Docker build times out on `go mod download` | Machine cannot reach the internet | Add `--build-arg http_proxy=...` `--build-arg https_proxy=...` at build time |
| Server unhealthy but actually reachable | The built-in HEALTHCHECK uses HEAD; `/healthz` only accepts GET | Confirm compose overrides the healthcheck to GET (defaulted) |

---

## Architecture overview

```
┌─────────────────────────────────────────────────────────────────┐
│                       Deploy machine (YOUR_IP)                  │
│                                                                 │
│  ┌────────────┐  ┌────────────┐  ┌─────────────────────────┐   │
│  │ PostgreSQL │  │ parsar-init│  │     parsar-server        │   │
│  │ :5432      │  │ (one-shot) │  │  SPA + API + WS          │   │
│  │ data vol   │  │ migrate+   │  │  Feishu WS inbound +     │   │
│  │            │  │ onboard    │  │  outbound worker         │   │
│  └────────────┘  └────────────┘  │  Docker sandbox mgr      │   │
│                                  └─────────┬───────────────┘   │
│                                            │                   │
│  ┌──────────────────────┐        0.0.0.0:18080                 │
│  │  sandbox container   │                 │                   │
│  │  (on demand)         │                 │                   │
│  │  Claude Code + Codex │                 │                   │
│  │  parsar-daemon       │                 │                   │
│  └──────────────────────┘                  │                   │
└────────────────────────────────────────────┼───────────────────┘
                                             │
          ┌──────────────────────────────────┼─────────────┐
          │              LAN / Feishu        │             │
          │                                  │             │
          │   User browser ──── HTTP ────────┘             │
          │   User device ────── WebSocket ──┘             │
          │   Feishu group/DM ── Feishu WS ──┘             │
          └────────────────────────────────────────────────┘
```

---

## TL;DR

```bash
git clone <repo> parsar && cd parsar

# 1. Configure .env
cp .env.example .env
vim .env   # fill in Feishu credentials + master key + PARSAR_HOST_IP + Bot Open ID

# 2. Build images
sudo docker build -t parsar:local .
sudo docker build -f infra/sandbox/Dockerfile.local -t parsar-sandbox:local .

# 3. Bring it up
sudo docker compose -f docker-compose.local.yml up -d

# 4. Verify
curl http://YOUR_IP:18080/healthz   # 200
curl http://YOUR_IP:18080/readyz    # 200

# 5. Browser http://YOUR_IP:18080 → register first user (Owner)
#    → create Agent (sandbox mode) → bind default Bot
#    → @Bot in a Feishu group / pair a device → verified
```
