# Health endpoints and self-hosted smoke

This page is for **operators deploying Parsar to their own
environment** — Kubernetes, plain Docker, or a bare-metal host.

Goal: give you a stable, contract-frozen way to check whether a
running Parsar process is alive and ready to take traffic, without
depending on dev-only seed data, mock IM endpoints, or the
`X-Parsar-Dev-User-ID` header (the latter is a **dev-only** shim
gated behind `PARSAR_DEV_AUTH=true` and must stay off in
production).

## TL;DR

| Endpoint           | Purpose          | Touches DB | Status meaning             |
|--------------------|------------------|------------|----------------------------|
| `GET /healthz`     | Liveness probe   | No         | 200 = process alive        |
| `GET /readyz`      | Readiness probe  | Yes (Ping) | 200 = ready to serve real traffic; 503 otherwise |
| `GET /api/v1/health` | Legacy alias   | No         | 200 = process alive (backwards-compat) |
| `GET /api/v1/bootstrap/status` | First-owner posture (public) | Yes (count) | 200 = posture published (used by smoke-core only) |

Quick smoke after deployment:

```bash
# lite (default): three black-box health probes only.
scripts/smoke.sh --api-url http://<your-server>:<port>

# core: lite probes + bootstrap chain (first-owner state, dev-auth
# off, optional provision + idempotency).
scripts/smoke.sh --core --api-url http://<your-server>:<port>
scripts/smoke.sh --core --bootstrap-token "$PARSAR_BOOTSTRAP_TOKEN"

# Makefile shortcut (lite only — pass flags via PARSAR_API_URL):
PARSAR_API_URL=http://<your-server>:<port> make smoke
```

Exit code 0 ⇒ no FAIL rows. SKIP rows are allowed and document what
the operator would have to wire up to exercise the missing check.
Exit code 1 ⇒ at least one FAIL row (script prints which endpoint,
HTTP status, and full response body so you can immediately see what
the server complained about).

## Endpoint contract

### `GET /healthz` — liveness

Returns 200 the moment the HTTP server can answer a request. **Never
touches the database** or any other dependency.

Why: K8s `livenessProbe` failures restart the pod. A flaky downstream
dependency must not trigger a restart, because restarting amplifies
pressure on the very dependency that is struggling. If the DB is
down, the operator wants to drain traffic from the pod (readiness),
not thrash the process (liveness).

Response body (200):

```json
{"status": "ok", "name": "parsar"}
```

### `GET /readyz` — readiness

Returns 200 iff every declared dependency is reachable right now;
503 otherwise. The `checks` array lists each dependency and its
status.

Why: K8s `readinessProbe` failures only stop traffic at the load
balancer. When the DB goes down, you want all replicas to drain
gracefully so existing flows finish without taking new work that
will fail.

Current checks:

- `database` — runs `pgxpool.Pool.Ping()` against the configured
  Postgres. The per-probe timeout is 2 seconds, so a hung DB cannot
  wedge the probe handler. The underlying error message is returned
  verbatim in the `error` field for quick triage.

Response body (200, all checks ok):

```json
{
  "status": "ok",
  "name": "parsar",
  "checks": [
    {"name": "database", "status": "ok"}
  ]
}
```

Response body (503, DB unreachable):

```json
{
  "status": "degraded",
  "name": "parsar",
  "checks": [
    {
      "name": "database",
      "status": "fail",
      "error": "failed to connect ... dial tcp: connect: connection refused"
    }
  ]
}
```

Response body (503, no DB wired at startup):

```json
{
  "status": "degraded",
  "name": "parsar",
  "checks": [
    {
      "name": "database",
      "status": "not_configured",
      "error": "database pool is not initialised; check DATABASE_URL and that the database accepts connections at startup"
    }
  ]
}
```

A Parsar process with no database cannot serve real traffic, so
`/readyz` returns 503 even when `/healthz` returns 200 — the
operator gets a clear signal to fix the missing config without K8s
entering a restart loop.

Per-check `status` values:

- `ok` — dependency responded within the deadline.
- `fail` — dependency replied with an error or the probe timed out
  (`error` is populated).
- `not_configured` — dependency was never wired into the process
  (e.g. `DATABASE_URL` unset or initial Ping failed at startup).

### `GET /api/v1/health` — legacy alias

Identical contract to `/healthz`. Kept so existing scripts
(`scripts/dev-server-up.sh`, `scripts/smoke.sh`) keep working
unchanged. New deployments should target `/healthz` and `/readyz`.

## Kubernetes probe configuration

Minimal example for a Parsar Deployment. Pick numbers that match
your traffic SLO; the values below are reasonable defaults for a
small-to-medium self-hosted deployment.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: parsar
spec:
  template:
    spec:
      containers:
        - name: parsar
          image: parsar:<your-tag>
          ports:
            - containerPort: 8080
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8080
            # Liveness is intentionally generous: process startup
            # includes a goose migration check + an audit ingester
            # spin-up + an optional opencode connector init.
            initialDelaySeconds: 15
            periodSeconds: 10
            timeoutSeconds: 3
            failureThreshold: 3
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8080
            # Readiness is tighter: as soon as the DB is reachable,
            # the pod can take traffic; as soon as it drops, the pod
            # leaves the load balancer.
            initialDelaySeconds: 5
            periodSeconds: 5
            timeoutSeconds: 3
            failureThreshold: 2
            successThreshold: 1
```

Notes:

- Do **not** point `livenessProbe` at `/readyz`. A DB outage would
  then restart-loop the pod and starve the very DB that is
  recovering.
- Do **not** point `readinessProbe` at `/healthz`. A DB outage would
  then leave the pod in the load balancer rotation, returning 5xx
  to every real request.
- The 2 s in-handler timeout is per probe per dependency; pick
  `timeoutSeconds` ≥ the in-handler timeout plus network jitter.

## Smoke script

`scripts/smoke.sh` is a single-binary-dependency curl
sequence (curl + python3, nothing else) that ships in two modes:

- **lite** (default) — the three health probes above. Designed for
  the post-deploy "is the pod alive and the DB reachable" check.
- **core** — lite plus the minimum product-loop assertions a fresh
  fresh install must satisfy: bootstrap-status reachable, dev_auth
  shim off, setup-state recognized, and (when the operator supplies
  the bootstrap token) the first-owner provision + idempotency loop.

```text
Usage:
  scripts/smoke.sh [--mode lite|core] [options]

Modes:
  lite (default)      Three black-box health probes only.
  core                Lite probes + bootstrap chain assertions.

Options:
  --mode MODE         lite (default) or core.
  --lite              Shortcut for --mode lite.
  --core              Shortcut for --mode core.
  --api-url URL       Base URL of the deployed Parsar server.
                      Default: $PARSAR_API_URL or http://127.0.0.1:8080.
  --timeout SECONDS   Per-request timeout (default 5).
  --bootstrap-token T Token to POST /api/v1/bootstrap with. May also
                      be supplied via $PARSAR_BOOTSTRAP_TOKEN. When
                      omitted, the provisioning + idempotency checks
                      SKIP with a clear reason. Never written to
                      evidence files.
  --bootstrap-email E Email for the first owner (default smoke@example.com).
  --bootstrap-workspace N
                      Workspace display name (default "Smoke Workspace").
  --bootstrap-name N  Owner display name (default "Internal Smoke Owner").
  -h, --help          Show help.

Exit codes:
  0   no FAIL rows. PASS + SKIP rows allowed.
  1   at least one FAIL row (URL, status and body printed).
```

What it does NOT do:

- It does not authenticate against any session-protected endpoint.
  The three health endpoints + `/api/v1/bootstrap/status` are
  intentionally unauthenticated so K8s probes / monitoring tools /
  first-run installer UIs can hit them. `POST /api/v1/bootstrap`
  uses its own Bearer token (the bootstrap token), not a session
  cookie.
- It does not seed dev data, start mocks, or talk to a database
  directly. It is a black-box smoke against a deployed process.
- It does not write to the current working directory. All evidence
  (status code per probe, response body) lands under
  `~/.parsar/smoke/<UTC-timestamp>/` — matching the
  project-wide hard rule that runtime state lives under `~/.parsar/`.
- It does not write the bootstrap token, the master key, or any
  other secret to the evidence directory. The token only appears as
  an `Authorization: Bearer …` header on the wire.

Per-run evidence layout (core mode shown; lite omits the
`bootstrap_*` files):

```text
~/.parsar/smoke/20260530T064929Z/
├── healthz.body / .status / .curl.err
├── readyz.body / .status / .curl.err
├── v1_health.body / .status / .curl.err
├── bootstrap_status.body / .status / .curl.err
├── bootstrap_provision.body / .status / .curl.err  # only when --bootstrap-token supplied
├── bootstrap_provision.completed                   # sentinel; written only on PASS
└── bootstrap_idempotency.body / .status / .curl.err
```

### Core-mode checks

| Label | What it asserts | Failure means |
|---|---|---|
| `bootstrap_status` | `GET /api/v1/bootstrap/status` returns 200 with the five typed fields (`needed`, `has_owners`, `owner_count`, `http_enabled`, `dev_auth_enabled`) | bootstrap router is not wired or the openapi contract drifted; downstream `bootstrap_*` checks all SKIP |
| `bootstrap_dev_auth_disabled` | `dev_auth_enabled == false` | operator left `PARSAR_DEV_AUTH=true` on; the `X-Parsar-Dev-User-ID` shim is exploitable from outside |
| `bootstrap_state_recognized` | payload says either `needed=true, owner_count=0` or `needed=false, owner_count>=1` | upstream wiring is inconsistent (e.g. owner count != ownership boolean) |
| `bootstrap_provision` | `POST /api/v1/bootstrap` with the supplied token returns 201 and `setup_complete=true` | token wrong, body malformed, or server failed mid-transaction; SKIP when token absent or install already provisioned |
| `bootstrap_idempotency` | second `POST /api/v1/bootstrap` returns 409 `bootstrap_closed` | the door did not latch — running the smoke a second time could create a second owner; SKIP when no token |
| `runtime_chain` (TODO) | always SKIP today: AgentRun / audit / usage are still served under `/dev/*` behind the session middleware, not reachable from an unauthenticated smoke | tracked as a known gap until workspace-scoped business APIs are promoted to `/api/v1/workspaces/{wid}/…` |

Example session — lite happy path:

```text
$ scripts/smoke.sh --api-url http://127.0.0.1:8080
[smoke] target http://127.0.0.1:8080
[smoke] mode lite
[smoke] evidence dir /home/ops/.parsar/smoke/20260530T064923Z
[smoke] PASS healthz http://127.0.0.1:8080/healthz — http=200
[smoke] PASS readyz http://127.0.0.1:8080/readyz — http=200
[smoke] PASS v1_health http://127.0.0.1:8080/api/v1/health — http=200
[smoke] summary mode=lite pass=3 fail=0 skip=0
[smoke] all required probes passed
$ echo $?
0
```

Example session — core happy path on a fresh install (token supplied,
provision + idempotency both PASS):

```text
$ scripts/smoke.sh --core --api-url http://127.0.0.1:8080 --bootstrap-token "$PARSAR_BOOTSTRAP_TOKEN"
[smoke] target http://127.0.0.1:8080
[smoke] mode core
[smoke] evidence dir /home/ops/.parsar/smoke/20260530T064929Z
[smoke] PASS healthz ... — http=200
[smoke] PASS readyz ... — http=200
[smoke] PASS v1_health ... — http=200
[smoke] PASS bootstrap_status ... — http=200 needed=true has_owners=false owners=0 http_enabled=true dev_auth_enabled=false
[smoke] PASS bootstrap_dev_auth_disabled — dev_auth_enabled=false
[smoke] PASS bootstrap_state_recognized — install awaiting first owner (needed=true, owner_count=0)
[smoke] PASS bootstrap_provision — POST /api/v1/bootstrap returned 201 setup_complete=true
[smoke] PASS bootstrap_idempotency — second POST returned 409 bootstrap_closed (door correctly latched)
[smoke] SKIP runtime_chain — AgentRun / audit / usage workflow not exposed under /api/v1/*; ...
[smoke] summary mode=core pass=8 fail=0 skip=1
[smoke] all required probes passed
$ echo $?
0
```

Example session — core mode on an install that has already been
provisioned (provision SKIPs, idempotency still PASSes because the
door must remain latched):

```text
$ scripts/smoke.sh --core --bootstrap-token "$PARSAR_BOOTSTRAP_TOKEN"
...
[smoke] PASS bootstrap_status ... — needed=false has_owners=true owners=1 http_enabled=true dev_auth_enabled=false
[smoke] PASS bootstrap_dev_auth_disabled — dev_auth_enabled=false
[smoke] PASS bootstrap_state_recognized — install already provisioned (needed=false, owner_count=1)
[smoke] SKIP bootstrap_provision — install already provisioned (needed=false); fresh-setup PASS not testable, idempotency probe will still run
[smoke] PASS bootstrap_idempotency — second POST returned 409 bootstrap_closed (door correctly latched)
[smoke] SKIP runtime_chain — ...
[smoke] summary mode=core pass=7 fail=0 skip=2
```

Example session — core mode catching `PARSAR_DEV_AUTH=true` left
enabled on a production-ish deployment:

```text
$ scripts/smoke.sh --core --api-url http://127.0.0.1:8080
...
[smoke] PASS bootstrap_status ... — dev_auth_enabled=true
[smoke] FAIL bootstrap_dev_auth_disabled — dev_auth_enabled=true; production deployments MUST set PARSAR_DEV_AUTH=false (X-Parsar-Dev-User-ID shim must stay off)
...
[smoke] summary mode=core pass=5 fail=1 skip=3
[smoke] 1 probe(s) failed; see /home/ops/.parsar/smoke/<ts>
$ echo $?
1
```

Example session — DB unreachable (lite mode still surfaces the
problem; core mode would additionally SKIP every `bootstrap_*` check
because `/api/v1/bootstrap/status` cannot count owners without the
DB):

```text
$ scripts/smoke.sh --api-url http://127.0.0.1:8080
[smoke] target http://127.0.0.1:8080
[smoke] mode lite
[smoke] evidence dir /home/ops/.parsar/smoke/20260528T071502Z
[smoke] PASS healthz http://127.0.0.1:8080/healthz — http=200
[smoke] FAIL readyz http://127.0.0.1:8080/readyz — http=503 body={"status":"degraded","name":"parsar","checks":[{"name":"database","status":"not_configured",...}]}
[smoke] PASS v1_health http://127.0.0.1:8080/api/v1/health — http=200
[smoke] summary mode=lite pass=2 fail=1 skip=0
[smoke] 1 probe(s) failed; see /home/ops/.parsar/smoke/20260528T071502Z
$ echo $?
1
```

## Adding a new readiness check

When a future hard dependency lands (audit DB, model gateway, sandbox
provider…), follow the same pattern as the database check:

1. Add a field to `HealthDeps` in `server/internal/api/health.go`.
2. Write a `checkX(ctx, dep) CheckResult` helper that returns one of
   `CheckStatusOK`, `CheckStatusFail`, `CheckStatusNotConfigured`.
3. Add the helper call inside `readinessHandler` so the
   `checks` array grows by one row.
4. Wire the dependency from `cmd/server/main.go` — make sure to use
   the `var dep api.X` pattern so a typed-nil pointer becomes a
   nil interface (see comment in `cmd/server/main.go` and the
   regression test `TestReadinessTypedNilPinger`).
5. Add unit-test coverage matching the existing `TestReadinessDB*`
   set (ok / fail / missing / timeout / typed-nil-pinger).
6. Document the new check in this file.

Soft dependencies (e.g. an optional audit Kafka sink, a sandbox
runner used by only some agents) should **not** gate readiness;
expose them via a future `/metrics` or `/debug` endpoint instead.
