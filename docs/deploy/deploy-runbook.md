# Parsar deploy runbook

For operators deploying Parsar to production. The goal: starting from an empty
database and a config file, complete a real initialization **without** relying
on the development shortcuts of `seed-dev` fixtures, hard-coded fixture UUIDs,
or the `X-Parsar-Dev-User-ID` dev shim.

Audience: any self-hosted open-source deployment — every deployment profile
(single-node docker, K8s, bare metal) follows this document.

> **Related documents**
> - [feishu-prod.md](./feishu-prod.md) — Feishu OIDC + event-subscription production config.
> - [feishu-bot-per-agent.md](./feishu-bot-per-agent.md) — expose a single Agent as a Feishu bot (one per Feishu-connected Agent).
> - [health-and-smoke.md](./health-and-smoke.md) — `/healthz`, `/readyz`, `smoke.sh` checks.
> - [config.example.yaml](./config.example.yaml) — the fully-commented YAML template.
> - [`deploy/compose/compose.selfhost.yml`](../../deploy/compose/compose.selfhost.yml) — single-node compose starting point.
> - [`deploy/compose/.env.example`](../../deploy/compose/.env.example) — env injection template.

---

## 1. Overall startup order

```text
1. Prepare Postgres:   empty DB reachable + credentials in place
2. Prepare config / env: see §2
3. Run DB migrations             (parsar-migrate or docker run parsar:<tag> parsar-migrate or make migrate-dev)
4. Start the server              (parsar-server  or docker run parsar:<tag>)
5. Health checks pass            (/healthz + /readyz both return 200)
6. Create the first owner + workspace (HTTP API or CLI, pick one)
7. Turn bootstrap off            (remove PARSAR_BOOTSTRAP_TOKEN from env and restart)
8. Run smoke-core to validate the minimum closed loop (scripts/smoke.sh --core)
9. Normal operation
```

> Migrations must complete before the server takes its first business request.
> `/healthz` is liveness and does not touch the schema; `/readyz` only checks
> DB connectivity. Any conversation / agent call on an empty un-migrated DB
> will 500.

> `parsar-server` and `parsar-migrate` are the production-image binaries on
> `$PATH` (`/usr/local/bin`), so you invoke them by **bare name**, not
> `./parsar-...` — the image's `WORKDIR` is `/var/lib/parsar`, and those files
> are not there (they are built from the repo-root `Dockerfile` + `make
> docker-build`). Local dev does not need these two binaries — use `make
> server` / `make migrate-dev`, which go through `go run ./cmd/...`.

> The smoke-core in step 8 is not decoration: it re-runs the three step-5
> probes and additionally confirms that `/api/v1/bootstrap/status` is
> exposed, that `dev_auth_enabled=false` (a hard production invariant), that
> the bootstrap state has converged to "an owner exists", and that a second
> POST to `/api/v1/bootstrap` must return 409 (the door is bolted shut). Full
> rules in [health-and-smoke.md](./health-and-smoke.md#smoke-script).

Docker Compose / K8s deploy templates are in §7.

---

## 2. Configuration load order

`server/internal/config` is the source of truth for configuration. Load
precedence (later wins):

1. Built-in defaults (`Default()`).
2. Optional YAML file, specified via `PARSAR_CONFIG_FILE=<absolute path>`.
   - The path must be absolute or start with `~/`; **relative paths are
     rejected** (to prevent accidental CWD misreads).
   - When the env var is unset, the server reads no file at all. **There is
     no "auto-read `./config.yaml`" fallback.**
3. Environment variables (always the highest precedence, so secrets can be
   injected cleanly).

Startup validation:

- `database.url` must be non-empty in the production profile.
- `secret.master_key` must be non-empty in the production profile.
- `auth.dev_auth=true` is only permitted in the dev profile.
- `auth.cookie.secure` is derived from the deployment profile.

Profile inference: if either `auth.dev_auth=true` or
`gateway.feishu.mock=true` is true, the whole process runs in dev profile;
otherwise it validates as production.

### Example YAML

See `docs/deploy/config.example.yaml` (a fully-commented deploy template).
The example file only holds placeholders — **no real credential ever enters
the repo**.

### Key env variables at a glance

| Purpose | Env name | Default |
|---|---|---|
| Listen address | `PARSAR_ADDR` | `:8080` |
| External URL (for building callbacks) | `PARSAR_PUBLIC_URL` | empty |
| Runtime data directory | `PARSAR_DATA_DIR` | `~/.parsar` |
| Postgres connection | `DATABASE_URL` | (required) |
| Secret master key | `PARSAR_MASTER_KEY` | (required in production) |
| Bootstrap token | `PARSAR_BOOTSTRAP_TOKEN` | empty (HTTP bootstrap off) |
| Dev auth toggle | `PARSAR_DEV_AUTH` | `false` (must be false in production) |
| Runtime profile | `PARSAR_RUNTIME_PROFILE` | `managed` for managed deployments where the platform manages cloud sandboxes |
| MCP catalog override | `PARSAR_MCP_CATALOG_URL` | empty (use the catalog embedded in the server image) |

Feishu OAuth / event-related env vars are documented in
[feishu-prod.md](./feishu-prod.md).

---

## 3. Bootstrap: first owner + workspace

On a fresh empty DB, `GET /api/v1/bootstrap/status` returns:

```json
{
  "needed": true,
  "has_owners": false,
  "owner_count": 0,
  "http_enabled": false,
  "dev_auth_enabled": false
}
```

`needed=true` means setup has not completed. Installer UIs can key off this
status to decide the next step.

There are **two paths** to create the first owner + workspace:

### 3.1 Path A: HTTP API (remote deployments)

Best when you cannot log directly onto the target machine (K8s, hosted
platforms).

```bash
# 1. Generate a one-shot strong token (32 random bytes)
export PARSAR_BOOTSTRAP_TOKEN="$(openssl rand -hex 32)"

# 2. Export the token in the server's startup env, then start the server
#    (In production this is typically injected via a K8s secret / systemd
#    EnvironmentFile.)

# 3. Call the bootstrap endpoint
curl -sf -X POST https://parsar.example.com/api/v1/bootstrap \
  -H "Authorization: Bearer ${PARSAR_BOOTSTRAP_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{
    "email":          "admin@example.com",
    "name":           "First Admin",
    "workspace_name": "Acme"
  }'
```

Success returns 201:

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

Afterwards:

- `GET /api/v1/bootstrap/status` returns `needed=false`, `has_owners=true`.
- A second POST returns 409 + `bootstrap_closed`.
- **Remove `PARSAR_BOOTSTRAP_TOKEN` from env immediately** and restart the
  process for it to take effect. (A leftover token in env is a long-lived
  credential that could be stolen, even though it can no longer trigger
  bootstrap.)

### 3.2 Path B: CLI (local / inside the container)

Best when you can `kubectl exec` / `docker exec` into the target machine.
**Requires no token.**

```bash
export DATABASE_URL="postgres://parsar:parsar@127.0.0.1:5432/parsar?sslmode=disable"

go run ./server/cmd/parsar-bootstrap \
  --email=admin@example.com \
  --workspace="Acme" \
  --name="First Admin"
```

Or via the Makefile:

```bash
DATABASE_URL=postgres://... \
PARSAR_BOOTSTRAP_EMAIL=admin@example.com \
PARSAR_BOOTSTRAP_WORKSPACE="Acme" \
PARSAR_BOOTSTRAP_NAME="First Admin" \
make bootstrap
```

Exit codes:

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Argument error (missing --email / --workspace / DATABASE_URL) |
| 2 | Owner already exists, bootstrap is closed |
| 3 | Input validation failed |
| 4 | DB connect / commit failed |
| 5 | Other error |

### 3.3 Bootstrap consistency

Regardless of the path, both funnel into `store.ProvisionFirstOwner` inside a
**single transaction**:

1. Verify "no active workspace owner exists" (gate).
2. UpsertUserByEmail.
3. CreateWorkspace (auto slug + operator-supplied name).
4. Insert into workspace_members(owner).
5. Write a `bootstrap.first_owner_created` audit record.

Two concurrent calls both succeed only once; the second returns
`ErrBootstrapClosed`.

---

## 4. Relationship with seed-dev / dev_auth

| | For | Status |
|---|---|---|
| `PARSAR_DEV_AUTH=true` + `X-Parsar-Dev-User-ID` header | Local dev shim that skips cookie login | Must be off in production; Validate() rejects it at startup |
| `PARSAR_FEISHU_MOCK=true` | Local dev shim that skips real Feishu | Same as above |
| `PARSAR_BOOTSTRAP_TOKEN` + HTTP bootstrap | **Legitimate production init path** | Single-use, remove after use |
| `parsar-bootstrap` CLI | **Legitimate production init path** | Local access only |
| `scripts/smoke.sh` (lite) | Post-deploy liveness: `/healthz`, `/readyz`, `/api/v1/health` | Works for any deploy profile |
| `scripts/smoke.sh --core` | lite + bootstrap-path validation: readable bootstrap status + `dev_auth_enabled=false` + (optional) provision + closed-door idempotency | Works for any deploy profile; also validates the POST path when `--bootstrap-token` is set |

---

## 5. What's still missing before "deployment ready"

This track (Bootstrap + Config) only covers the **cold-start data + config
layer**. To make a deployment truly production-ready you still need:

| Item | Current status | Owner |
|---|---|---|
| **Production artifact / OCI image** | **Landed; see repo-root `Dockerfile` + `make docker-build`** | — |
| Sandbox runner (E2B) | Phase 4 in progress | Another session |
| Admin UI (installer step-by-step) | Phase 4 follow-up | Another session |
| Real auth (Feishu OIDC production config) | Landed; see `feishu-prod.md` | — |
| Real auth (other OAuth providers: GitHub / Google / email magic link) | Not implemented | Later phase |
| Smoke — post-deploy liveness (/healthz, /readyz, /api/v1/health) | Landed; see the lite mode in `health-and-smoke.md` | — |
| Smoke — bootstrap path (status / dev_auth shim / provision / idempotency) | Landed; see the core mode in `health-and-smoke.md` | — |
| Smoke — end-to-end AgentRun / audit / usage | Missing `/api/v1/workspaces/{wid}/{agent-runs,audit-records,usage}` and other cookie-session entry points; smoke-core marks this SKIP/TODO | Later phase |
| Real audit sink (Kafka / self-hosted storage) | In-memory + Postgres sink for now; the interface is already abstracted | Later phase |
| Memory L0-L3 | Not implemented | Later phase |
| Capability marketplace | Workspace-published Skill market and repository-backed stdio / Streamable HTTP MCP Connector Directory are available | — |

**Invariants delivered by this track:**

- No longer depends on seed-dev fixture UUIDs.
- No longer depends on the `X-Parsar-Dev-User-ID` shim.
- Config is neither auto-read from nor written to CWD.
- `make check` still passes.
- The production profile fails to start (rather than silently degrading) when
  a critical secret (master key / DATABASE_URL) is missing.

---

## 6. Security notes

- `PARSAR_BOOTSTRAP_TOKEN`, `PARSAR_MASTER_KEY`, `PARSAR_FEISHU_APP_SECRET`,
  etc., must **never enter the repo** — nor be baked as defaults into example
  YAML files. Example files hold only `<placeholder>`.
- The HTTP bootstrap token is compared with
  `crypto/subtle.ConstantTimeCompare` to avoid timing attacks.
- The `bootstrap.first_owner_created` audit event carries `user_email` and
  `workspace_slug` so the audit log records exactly when install happened.
- `GET /api/v1/bootstrap/status` is public by design; it exposes only the
  boolean state + owner count, no identity information.

---

## 7. Docker Compose deploy template

The repo ships a starter compose that covers steps 1–4 and 7 of §1:

```text
deploy/compose/
├── README.md                  directory overview + three deploy shapes
├── compose.selfhost.yml       parsar-server + postgres, two services
└── .env.example               env template (all placeholders)
```

### 7.1 End-to-end run (single node + bundled Postgres)

```bash
# 1. Prepare env
cp deploy/compose/.env.example deploy/compose/.env
# Edit deploy/compose/.env:
#   - PARSAR_SERVER_IMAGE   path to your image
#   - PARSAR_PUBLIC_URL     external URL behind the reverse proxy
#   - PARSAR_PG_PASSWORD    openssl rand -hex 24
#   - PARSAR_MASTER_KEY     openssl rand -hex 32
#   - PARSAR_BOOTSTRAP_TOKEN openssl rand -hex 32 (remove after use)

# 2. (Optional) Prepare YAML config
sudo install -d /etc/parsar
sudo cp docs/deploy/config.example.yaml /etc/parsar/config.yaml
# Replace each <placeholder> with a real value — real credentials still flow
# through env, not the file.

# 3. Bring up postgres + server
docker compose -f deploy/compose/compose.selfhost.yml --env-file deploy/compose/.env up -d

# 4. Run migrations (executed inside the container, sharing DATABASE_URL)
docker compose -f deploy/compose/compose.selfhost.yml --env-file deploy/compose/.env \
  exec parsar-server parsar-migrate

# 5. Smoke check
scripts/smoke.sh --api-url http://127.0.0.1:8080

# 6. Bootstrap the first owner (§3.1 via HTTP or §3.2 via CLI)
curl -sf -X POST http://127.0.0.1:8080/api/v1/bootstrap \
  -H "Authorization: Bearer ${PARSAR_BOOTSTRAP_TOKEN}" \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@example.com","name":"First Admin","workspace_name":"Acme"}'

# 7. Close bootstrap: remove PARSAR_BOOTSTRAP_TOKEN from .env, then
docker compose -f deploy/compose/compose.selfhost.yml --env-file deploy/compose/.env \
  up -d --force-recreate parsar-server
```

### 7.2 Validate compose file syntax

After each edit of `compose.selfhost.yml` or `.env.example`, run:

```bash
docker compose -f deploy/compose/compose.selfhost.yml --env-file deploy/compose/.env config >/dev/null
```

`docker compose config` expands every `${VAR}` and validates the YAML /
schema; a zero exit code means the file parses cleanly for the docker
engine. CI should include this in the lint stage.

### 7.3 K8s / other orchestrators

`compose.selfhost.yml` is not a K8s manifest, but it maps directly:

- env block → Deployment.spec.template.spec.containers[].env
- healthcheck → `livenessProbe` on `/healthz`, `readinessProbe` on `/readyz`
  (see [health-and-smoke.md §3](./health-and-smoke.md#kubernetes-probe-configuration))
- volumes → ConfigMap (config.yaml) + PersistentVolumeClaim (runtime data)
- `.env` → Secret resource; env injected via `envFrom: secretRef:`

K8s manifests / Helm charts / kustomize overlays are maintained by each
deployer against their own environment; they do not live in the open-source
repo.
