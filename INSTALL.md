# INSTALL — Local Quickstart (for AI coding agents)

> **Audience:** an AI coding agent (Claude Code, Cursor, Codex, etc.) the
> user has just opened inside a fresh clone of this repo.
> **Human readers:** skip to `docs/deploy/deploy-runbook.md` for the
> manual version.
>
> **What this gives the user:** a working parsar instance on
> `http://localhost:8080` in roughly 5 minutes of wall-clock time
> (mostly waiting for the first `docker build`). Mock Feishu auth is
> on by default so the user can poke at the UI without registering a
> real OAuth app.

---

## Hard rules for the agent

1. **Never invent values.** If something below says "ask the user", ask
   the user — do not guess a port, a path, an email, or a workspace name.
2. **Never commit `.env`** — it is already in `.gitignore` and contains
   real secrets after step 2.
3. **Never write inside the repo working tree** for runtime state. Use
   the paths the user picks in step 1; defaults below are
   `/tmp/parsar-data` + `/tmp/parsar-config` (no sudo, lost on
   reboot — fine for a quickstart).
4. **Verify each step before moving on.** Every step has an "Expected"
   block; if reality doesn't match, run the "If it fails" block before
   asking the user.
5. **Print only what helps the user.** Suppress noisy command output
   except when surfacing a real error.

---

## Platform notes — read once, then act

This runbook is tested on **macOS (Docker Desktop)** and **Linux**
(Ubuntu / Debian / Amazon Linux with `docker-ce`). Windows / WSL2 is
not tested — assume rough edges.

### macOS

- Docker Desktop must be **running** before step 3 (build) — the daemon
  is not auto-started on first install. Verify with `docker info` →
  should print a server section, not just a client section.
- Default Docker Desktop resource limits (2 CPU / 2 GB RAM) are enough
  for runtime but may stretch the first build to 8-10 min. If the user
  later complains about slowness, suggest raising to 4 CPU / 4 GB in
  Docker Desktop → Settings → Resources.
- Apple Silicon: the Dockerfile builds `linux/arm64` natively, no
  emulation needed. If the agent ever sees an `exec format error` at
  runtime, the image was built on x86 CI and pulled on M-series —
  rebuild locally with `make docker-build`.

### Linux

- The current shell user **must be in the `docker` group**, otherwise
  step 3 fails with `permission denied while trying to connect to the
  Docker daemon socket`. Check with `id -nG | tr ' ' '\n' | grep -x
  docker`. If missing, instruct the user to run `sudo usermod -aG docker
  $USER` and **log out and back in** (group membership is per-session).
- Use `docker compose` (v2 plugin), NOT the standalone `docker-compose`
  binary. If `docker compose version` errors with `is not a docker
  command`, install the v2 plugin: `sudo apt-get install
  docker-compose-plugin` on Debian-family, or follow Docker's official
  install guide for the distro.
- SELinux-enabled hosts (RHEL / Rocky / Amazon Linux 2023) may block
  bind-mounts. If step 4 logs show `Permission denied` reading
  `init.sql`, append `:Z` to the mount line in the compose file or
  temporarily `sudo setenforce 0` (re-enable after verifying it was the
  cause).

### Both

- `lsof` behaves the same on macOS and Linux for the port-check lines
  below. `sed -i.bak` in step 8 is portable across both (the `.bak`
  argument is required for BSD sed on macOS, harmless on GNU sed).

---

## Prerequisites — check first, before step 1

Run these and report any miss to the user before doing anything else:

```bash
docker compose version       # need v2+
make --version               # any GNU make
openssl version              # any modern openssl
command -v curl
command -v python3           # used by smoke.sh
```

Also check ports:

```bash
lsof -iTCP:8080 -sTCP:LISTEN   # should print nothing
lsof -iTCP:5432 -sTCP:LISTEN   # should print nothing
```

If a port is busy, ask the user for an alternative in step 1.

Free disk: image build needs ~3 GB. `df -h .` and warn if free space < 5 GB.

---

## Step 1 — Ask the user three things

Use your AskUserQuestion (or equivalent) tool. The first two are
optional; use defaults unless `lsof` above already showed the default
port is busy.

| Question | Default | When to ask |
|---|---|---|
| Host port for parsar web/api? | `8080` | If port 8080 is busy |
| Host port for postgres? | `5432` | If port 5432 is busy |
| Where to put runtime data + config? | `/tmp/parsar-data` + `/tmp/parsar-config` (no sudo) | Always — also offer `/var/lib/parsar` + `/etc/parsar` if user wants the install to survive reboot |

Remember the answers; you'll need them in step 2.

---

## Step 2 — Prepare directories + `.env`

Substitute the four shell variables at the top with what the user picked
in step 1.

```bash
HOST_PORT=8080                            # from step 1, question 1
PG_HOST_PORT=5432                         # from step 1, question 2
DATA_DIR=/tmp/parsar-data               # from step 1, question 3
CONFIG_DIR=/tmp/parsar-config           # from step 1, question 3

mkdir -p "$DATA_DIR" "$CONFIG_DIR"
cp docs/deploy/config.example.yaml "$CONFIG_DIR/config.yaml"
cp deploy/compose/.env.example deploy/compose/.env

# Generate secrets fresh — never reuse across machines.
PG_PASSWORD="$(openssl rand -hex 24)"
MASTER_KEY="$(openssl rand -hex 32)"
BOOTSTRAP_TOKEN="$(openssl rand -hex 32)"

python3 - <<PY
import re
path = "deploy/compose/.env"
with open(path) as f: s = f.read()
subs = {
    "PARSAR_SERVER_IMAGE":      "parsar:dev",
    "PARSAR_PUBLIC_URL":        "http://localhost:${HOST_PORT}",
    "PARSAR_PG_PASSWORD":       "${PG_PASSWORD}",
    "PARSAR_MASTER_KEY":        "${MASTER_KEY}",
    "PARSAR_BOOTSTRAP_TOKEN":   "${BOOTSTRAP_TOKEN}",
    "PARSAR_CONFIG_HOST_PATH":  "${CONFIG_DIR}/config.yaml",
    "PARSAR_DATA_HOST_PATH":    "${DATA_DIR}",
    "PARSAR_HOST_PORT":         "${HOST_PORT}",
    "PARSAR_PG_HOST_PORT":      "${PG_HOST_PORT}",
}
for k, v in subs.items():
    s = re.sub(rf"^{re.escape(k)}=.*", f"{k}={v}", s, flags=re.M)
with open(path, "w") as f: f.write(s)
print("OK")
PY
```

**Verify:**

```bash
grep -E "PARSAR_SERVER_IMAGE|PARSAR_PUBLIC_URL|PARSAR_PG_PASSWORD|PARSAR_MASTER_KEY|PARSAR_BOOTSTRAP_TOKEN|PARSAR_CONFIG_HOST_PATH|PARSAR_DATA_HOST_PATH|PARSAR_HOST_PORT|PARSAR_PG_HOST_PORT" deploy/compose/.env
```

**Expected:** every variable shows a real value (not `<placeholder>`).
`PARSAR_FEISHU_MOCK=true` should also be present — it's the default in
the example file. **If it isn't**, add the line manually:

```bash
echo "PARSAR_FEISHU_MOCK=true" >> deploy/compose/.env
```

---

## Step 3 — Build the server image

```bash
make docker-build PARSAR_IMAGE=parsar PARSAR_IMAGE_TAG=dev
```

**Expected:** 5-10 min on first build. Ends with a line like
`naming to docker.io/library/parsar:dev`.

**If it fails:**

| Symptom | Cause | Fix |
|---|---|---|
| `Cannot connect to the Docker daemon` | docker engine not running | Start Docker Desktop / `systemctl start docker`; retry |
| `no space left on device` | docker disk full | `docker system prune -af`; retry |
| `pnpm install` or `apt-get` network timeout | DNS / VPN issue | Wait, retry; layer cache means it picks up where it stopped |
| `ERROR: failed to solve: ... port is already allocated` | unrelated build artifact | `docker ps -a | grep parsar` then `docker rm -f` any leftovers; retry |
| `denied: requested access to the resource is denied` | trying to pull from a private registry | This shouldn't happen on a clean clone; check if user fiddled with `Dockerfile` or `compose.example.yml` |

After the build, verify the image exists:

```bash
docker image inspect parsar:dev --format '{{.Id}}'    # any sha256: line is fine
```

---

## Step 4 — Postgres up + healthy

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env up -d postgres
sleep 6
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env ps postgres
```

**Expected:** the `STATUS` column shows `Up X seconds (healthy)`.

**If unhealthy:**

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env logs postgres | tail -30
```

| Log says | Fix |
|---|---|
| `FATAL: password authentication failed` | leftover volume from a previous run with a different password. `docker compose ... down -v` then re-run step 4. **This deletes any old data — confirm with user first.** |
| `could not bind IPv4 address` | the host port from step 1 is busy. Pick a different `PARSAR_PG_HOST_PORT`, re-run step 2 substitution, re-run step 4. |
| keeps restarting with no obvious error | give it 30 more seconds; alpine postgres init can be slow on first run. |

---

## Step 5 — Run the schema migration

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env run --rm parsar-server parsar-migrate
```

**Expected:** exit code 0. May print nothing (idempotent — if schema is
already there, no output).

**Verify:**

```bash
docker exec parsar-postgres psql -U parsar -d parsar -c '\dt' | grep -c "^ public"
```

**Expected:** `30` (thirty tables created).

**If 0 tables:** migration silently failed.

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env run --rm parsar-server parsar-migrate 2>&1
```

Read the output. If it mentions `dial tcp ... connect: connection
refused`, postgres isn't reachable from the run container — re-check
step 4 status, then retry step 5.

---

## Step 6 — Server up + healthy

```bash
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env up -d parsar-server
sleep 8
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env ps parsar-server
```

**Expected:** `STATUS` shows `Up X seconds (healthy)`.

**If `Restarting (1) X seconds ago`:**

```bash
docker logs parsar-server 2>&1 | tail -30
```

| Log says | Fix |
|---|---|
| `PARSAR_FEISHU_APP_ID/APP_SECRET/REDIRECT_URI ... required when not in mock mode` | `PARSAR_FEISHU_MOCK=true` is missing or set to false. Edit `deploy/compose/.env`, re-run step 6 with `--force-recreate`. |
| `agent_daemon owner URL not resolvable` | `PARSAR_AGENT_DAEMON_OWNER_URL` not transparent to the container. `grep PARSAR_AGENT_DAEMON_OWNER_URL deploy/compose/.env` — if empty, leave empty (compose has a `:-http://parsar-server:8080` default that should engage). If still failing, set explicitly: `echo "PARSAR_AGENT_DAEMON_OWNER_URL=http://parsar-server:8080" >> deploy/compose/.env`, then re-run step 6 with `--force-recreate`. |
| `create workspace: ERROR: ... visibility_check` | You're on an outdated branch missing stage 2 fixup #2. `git pull origin main` then redo step 3+ from scratch. |
| `PARSAR_MASTER_KEY is required for secrets` | step 2's substitution didn't land. Re-run step 2's python block. |
| anything else | give the user the last 30 log lines verbatim and stop. |

After fixing, re-run step 6 with `--force-recreate parsar-server`.

---

## Step 7 — Smoke test (proves the whole chain works)

```bash
BOOT_TOKEN="$(grep '^PARSAR_BOOTSTRAP_TOKEN=' deploy/compose/.env | cut -d= -f2)"
HOST_PORT="$(grep '^PARSAR_HOST_PORT=' deploy/compose/.env | cut -d= -f2)"
bash scripts/smoke.sh --core \
  --api-url "http://localhost:${HOST_PORT}" \
  --bootstrap-token "$BOOT_TOKEN"
```

**Expected:** last line `[smoke] all required probes passed`, summary
`pass=8 fail=0 skip=1`. The 1 SKIP is `runtime_chain` — a known limit
of the unauthenticated smoke surface, not a deployment failure.

**If any `FAIL`:** the smoke script prints the exact endpoint, HTTP
status, and response body. Fix the root cause it points to, then re-run.

---

## Step 8 — Close the bootstrap door

The bootstrap token is a one-shot owner-provision credential. Once
step 7 proved the chain works (and provisioned the first owner),
remove it from `.env` and restart the server so the door latches shut.

```bash
sed -i.bak 's/^PARSAR_BOOTSTRAP_TOKEN=.*/PARSAR_BOOTSTRAP_TOKEN=/' deploy/compose/.env
rm -f deploy/compose/.env.bak
docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env up -d --force-recreate parsar-server
sleep 6
```

**Verify:**

```bash
curl -s http://localhost:"$HOST_PORT"/api/v1/bootstrap/status
```

**Expected:** JSON with `"http_enabled":false`.

---

## Step 9 — Report success to the user

Tell the user (use these exact words; substitute `$HOST_PORT`):

> Done. Open http://localhost:$HOST_PORT in your browser, click 「飞书登录」,
> and you'll be logged in as `admin@example.com` (mock auth — anyone hitting
> this endpoint becomes the same user, fine for local poking).
>
> Useful follow-up commands:
>
> - View logs:   `docker logs -f parsar-server`
> - Stop:        `docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env down`
> - Wipe data:   add `-v` to the down command above (deletes the postgres volume)
> - Rebuild + redeploy after code change:
>     `make docker-build && docker compose -f deploy/compose/compose.example.yml --env-file deploy/compose/.env up -d --force-recreate`
>
> To go to production: see `docs/deploy/feishu-prod.md` for registering a
> real Feishu OAuth app, then set `PARSAR_FEISHU_MOCK=false` and fill the
> four `PARSAR_FEISHU_*` variables.

---

## Self-check for the agent

Before declaring "done", confirm all of these:

- [ ] `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:$HOST_PORT/healthz` → `200`
- [ ] `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:$HOST_PORT/api/v1/health` → `200`
- [ ] `curl -s -o /dev/null -w "%{http_code}\n" http://localhost:$HOST_PORT/` → `200` (SPA)
- [ ] `curl -s http://localhost:$HOST_PORT/api/v1/bootstrap/status` returns JSON with `"has_owners":true`
- [ ] `deploy/compose/.env` exists, is `git status`-clean (covered by `.gitignore`), and `PARSAR_BOOTSTRAP_TOKEN=` is empty

If any check fails, do not tell the user "done" — diagnose and fix
first, or hand the user a verbatim error.
