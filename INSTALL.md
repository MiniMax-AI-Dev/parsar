# INSTALL — Parsar Local Quickstart

> **Audience:** developers and AI coding agents (Claude Code, Cursor, Codex, etc.) who want to run Parsar on their own machine or evaluate it.
> **What you get:** a working Parsar in a few minutes (mock login, no Feishu or secrets required), with your local Claude Code / OpenCode / Codex paired in as an **online device** — the entire flow is "copy one command from the browser → paste in a terminal → device online".
> **Not covered here:** self-hosting / production deploys (real Feishu OIDC, custom ports and secrets, bootstrap tokens) live on a separate path — see `deploy/compose/compose.selfhost.yml` and `docs/deploy/`.

---

## Overview

```
clone → (pull/build image) → docker compose up → log in via browser → pair device → device online
```

The local stack is all-in-one: `postgres` + a one-shot `init` (migrate + first owner) + `parsar-server` (mock login). **Profile not fork:** it uses the **same image / migrations / SPA / install.sh** as the self-host edition; only the compose file and env values differ.

---

## 0 · Prerequisites

```bash
docker compose version                                       # v2+ required
claude --version || opencode --version || codex --version    # at least one, and logged in
```

- **Docker** installed and running (on macOS open Docker Desktop first so `docker info` shows the server section).
- At least one Agent CLI (Claude Code / OpenCode / Codex) installed and **logged in** on this machine. The daemon does not embed any model login of its own; it reuses this CLI's login state and subscription, and it can see the real repositories on your host.

---

## 1 · Clone the repo

```bash
git clone <your-repo-url> parsar
cd parsar
# The local-edition deliverable currently lives on feature/deliverables-design;
# skip this step once it lands on the main branch and use the default branch.
git checkout feature/deliverables-design
```

---

## 2 · Pick an image and set env vars

By default the next step's `docker compose` pulls a prebuilt image from GHCR — no manual pull needed:

```bash
# Default ports are 18080 / 15432; change these if they clash (e.g. 18088 / 15488)
export PARSAR_LOCAL_PORT=18080
export PARSAR_PG_PORT=15432
```

> **Current status (remove this note once the image is published to GHCR):**
> The key that makes device pairing zero-config offline — the **four platform daemon binaries baked into the image** — has not yet been published alongside the image on GHCR. Until it is, pulling the GHCR image directly gives you an image **without** the embedded daemon, so **step 4 will fail (404) when it tries to download the daemon**. In the meantime, build locally as a fallback:
> ```bash
> make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=local   # first build takes ~5–10 min
> export PARSAR_SERVER_IMAGE=parsar:local
> ```
> Verify the built image really contains the daemon:
> ```bash
> docker run --rm --entrypoint ls parsar:local /usr/local/share/parsar/daemon
> # Expected: four files parsar-daemon-{darwin,linux}-{amd64,arm64}
> ```
> Once the image is published to GHCR this note (and the local-build step above) go away — just skip to step 3 and use the default image.

---

## 3 · Bring the local stack up with one command

```bash
docker compose -f docker-compose.local.yml up -d
```

The first run pulls / uses the `parsar-server` image, runs the database migrations, and provisions the first workspace automatically (owner = the mock identity `admin@example.com`).

**Expected:** three containers; `parsar-local-server` and `parsar-local-postgres` become healthy within ~15 seconds; `parsar-local-init` exits after it finishes.

**Verify:**
```bash
docker compose -f docker-compose.local.yml ps
docker inspect parsar-local-init --format 'init exit={{.State.ExitCode}}'
# Expected: 0; a repeated `up` still shows 0 (parsar-bootstrap exits 2 when
# already bootstrapped, and compose collapses that back to 0 via `|| [ $? -eq 2 ]`)
```

Quick end-to-end sanity check from the CLI (30 seconds before you touch the browser):
```bash
B="http://127.0.0.1:${PARSAR_LOCAL_PORT}"
for p in /healthz /api/v1/health / ; do
  printf '%-18s -> %s\n' "$p" "$(curl -fsS -o /dev/null -w '%{http_code}' "$B$p")"
done
curl -fsS "$B/api/v1/bootstrap/status"; echo
```
**Expected:**
```
/healthz           -> 200
/api/v1/health     -> 200
/                  -> 200
{"needed":false,"has_owners":true,"owner_count":1, ...}
```

---

## 4 · Browser E2E acceptance (north star: pair your device)

1. Open **http://127.0.0.1:18080** in your browser (use your own port if you changed it).
2. Click **Log in** (the button may read "Feishu login"; mock mode needs no username / password, it goes straight through) — identity `admin@example.com`, landing on `Local Workspace`.
3. Go to **Devices / runtime management** → click **"Pair new device"** → enter a device name → click **"Generate connect command"**.
4. Copy the **single** command in the dialog → paste it into **another terminal on the same host**. The command looks like:
   ```bash
   curl -fsSL http://127.0.0.1:18080/api/v1/parsar-daemon/install.sh \
     | PARSAR_DAEMON_CONNECT_URL=http://127.0.0.1:18080 \
       PARSAR_DAEMON_CONNECT_TOKEN=<one-shot token> \
       PARSAR_DAEMON_CONNECT_DEVICE_NAME=<device name> bash
   ```
   - The server URL is filled in automatically from `PARSAR_PUBLIC_URL`, **do not edit it by hand**.
   - The script: downloads the platform-appropriate daemon from your server → `chmod` → runs `connect` in the background; the token is passed via env vars rather than argv, so it never appears in `ps` output or access logs. You **never touch** the binary, its path, or the token.
5. Within a few seconds the device flips from `pending_pairing` → **"online"**. **E2E passed.**
   If you want to confirm the device can actually do work: create an Agent and run through one issue.

---

## 5 · Stop / clean up

```bash
docker compose -f docker-compose.local.yml down       # stop, keep the data volume
docker compose -f docker-compose.local.yml down -v    # also drop the Postgres data
```

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Device pairing downloads daemon and hits **404** | The GHCR image you pulled does not contain the embedded daemon (not published yet) | Use the local build from step 2: `export PARSAR_SERVER_IMAGE=parsar:local` then `up -d --force-recreate` |
| Port reports `address already in use` | 18080 / 15432 is taken | Change `PARSAR_LOCAL_PORT` / `PARSAR_PG_PORT` and `up -d` again |
| `server` crash-loops with `agent_daemon owner URL not resolvable` | Missing `PARSAR_AGENT_DAEMON_OWNER_URL` (the local compose already defaults it to `http://parsar-server:8080`) | If you edited / removed that env, restore it |
| `server` stays unhealthy but is actually reachable | The image's built-in HEALTHCHECK uses HEAD, but `/healthz` only accepts GET (the local compose overrides the probe to GET) | If you edited compose, confirm the healthcheck uses GET |
| Device is online but no `kind` is available when creating an Agent | No Agent CLI is installed / logged in on this host | Log into `claude` / `opencode` / `codex` on this machine — the daemon picks it up automatically |
| macOS `exec format error` | Built on x86 but running on Apple Silicon | Re-run `make docker-build` locally to build for the native architecture |

Logs: `docker logs -f parsar-local-server`

---

## TL;DR

```bash
git clone <repo> parsar && cd parsar && git checkout feature/deliverables-design
export PARSAR_LOCAL_PORT=18080 PARSAR_PG_PORT=15432
# Current phase (GHCR has no image with the embedded daemon yet) → build locally;
# once published these two lines can go and the default image works.
make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=local
export PARSAR_SERVER_IMAGE=parsar:local
docker compose -f docker-compose.local.yml up -d
# Browser http://127.0.0.1:18080 → log in → Pair new device → copy command → run in another terminal → device online
```
