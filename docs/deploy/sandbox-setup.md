# Parsar Sandbox deployment (Kubernetes SandboxSet)

> Self-hosted cloud sandbox for the agent_daemon connector, backed by the
> [agents.kruise.io](https://openkruise.io/) SandboxSet CRD. Each pod
> ships envd (e2b-compatible API) + Claude Code CLI + parsar-daemon, allocated
> on demand by the Parsar server's E2B-compatible client.

This page is only needed when you want **platform-managed cloud sandboxes**
for the agent_daemon connector. Self-hosted deployments that just need
local-laptop-paired daemons can skip this entirely.

---

## 0. Architecture

```
User sends a message → Parsar Server
                            ↓
                  SandboxProvider.Acquire()
                            ↓
               E2B-compatible API (envd in pod)
                            ↓
                Allocate a pod from the warm pool
                            ↓
           Pod runs: parsar-daemon connect -b
                            ↓
              parsar-daemon WebSocket dials Server back
                            ↓
       Server delivers prompt_request → daemon runs
       Claude Code → streams output → client
```

The warm pool is maintained by the SandboxSet controller; allocation
happens at prompt time, not pod startup, so a cold queue runs ~3-5s
instead of the 30s+ that on-demand pod creation would cost.

---

## 1. Prerequisites

| Item | Notes |
|------|-------|
| A Kubernetes cluster with [OpenKruise Agents CRDs](https://openkruise.io/docs/installation) installed | Provides the `SandboxSet` resource type |
| A container registry you control (or use `ghcr.io` via the bundled workflow) | The sandbox image is built and pushed by `.github/workflows/sandbox-image-release.yml` to `ghcr.io/<your-org>/parsar-sandbox` |
| A published `parsar-daemon` release | `.github/workflows/parsar-daemon-release.yml` produces GitHub Releases; the sandbox Dockerfile pulls the binary at build time |
| Parsar Server reachable from the sandbox pods | `PARSAR_PUBLIC_URL` must be resolvable inside the cluster — parsar-daemon dials back to it over WebSocket |

---

## 2. Build the sandbox image

The default path uses GitHub Actions:

1. Fork or own a copy of this repo on GitHub.
2. Push to `main` (or tag `sandbox-v*`) — `.github/workflows/sandbox-image-release.yml` builds + pushes
   `ghcr.io/<your-org>/parsar-sandbox:latest` (and `:sha-<short>`, `:<tag>`).

To build locally instead:

```bash
cd infra/sandbox/parsar-daemon-claudecode
docker build --platform linux/amd64 \
  --build-arg PARSAR_DAEMON_REPO=<your-org>/parsar \
  -t ghcr.io/<your-org>/parsar-sandbox:dev \
  .
docker push ghcr.io/<your-org>/parsar-sandbox:dev
```

Sanity-check the image (envd MUST be present, otherwise pods will
CrashLoopBackOff on startup):

```bash
docker run --rm --platform linux/amd64 \
  ghcr.io/<your-org>/parsar-sandbox:dev \
  /bin/sh -c 'which envd && envd --version && claude --version && parsar-daemon version'
```

Image contents:

- `ubuntu:22.04` base
- `/usr/bin/envd` — e2b sandbox daemon (copied from `e2bdev/base:latest`)
- `/usr/local/bin/claude` — Claude Code CLI
- `/usr/local/bin/parsar-daemon` — parsar-daemon static binary (from GitHub Releases)
- `/usr/local/bin/codex` — OpenAI Codex CLI
- `node 20 + python3 + uv` — MCP server runtimes
- `IS_SANDBOX=1` env

---

## 3. Apply the SandboxSet

The bundled manifests default to `ghcr.io/<your-org>/parsar-sandbox:latest`
— substitute your registry path before applying. The repo ships two pool
sizes:

| File | Pool | Per-pod resources |
|------|------|-------------------|
| `infra/sandbox/parsar-daemon-claudecode/sandboxset.yaml` | Standard (`parsar-daemon`) | 4 vCPU, 8 GiB |
| `infra/sandbox/parsar-daemon-claudecode-xl/sandboxset.yaml` | XL (`parsar-daemon-xl`) | 4 vCPU, 50 GiB |

Apply:

```bash
# Replace <your-org> in the image: field first, then:
kubectl apply -f infra/sandbox/parsar-daemon-claudecode/sandboxset.yaml
kubectl apply -f infra/sandbox/parsar-daemon-claudecode-xl/sandboxset.yaml   # optional

# Pool status:
kubectl -n sandbox-work get sandboxset parsar-daemon
kubectl -n sandbox-work get pods | grep parsar-daemon
```

Key SandboxSet fields:

| Field | Default | Notes |
|-------|---------|-------|
| `metadata.name` | `parsar-daemon` (or `-xl`) | Also the template ID the Parsar server uses |
| `spec.replicas` | `10` / `2` | Warm pool size; cold queue starts ~3-5s, beyond pool fills new pods on demand |
| `image` | `ghcr.io/<your-org>/parsar-sandbox:latest` | Bump when re-tagging |
| `resources` | 4 vCPU / 8 GiB (XL: 50 GiB) | Per-pod reservation |

---

## 4. Wire the Parsar Server

Set these env vars on the Parsar Server process:

| Env | Value | Purpose |
|-----|-------|---------|
| `PARSAR_E2B_API_BASE_URL` | The cluster-internal URL of your envd gateway | Where the server's E2B client dials |
| `PARSAR_E2B_API_KEY` | Your gateway's API key | Auth for the above |
| `AGENT_DAEMON_SANDBOX_TEMPLATE` | `parsar-daemon` | Matches `metadata.name` in `sandboxset.yaml` |
| `AGENT_DAEMON_SANDBOX_TEMPLATE_XL` | `parsar-daemon-xl` | Optional; agents requesting `sandbox_size: "xl"` route here, otherwise fall back to the standard template |

Restart the server. The startup log should contain:

```
agent_daemon connector registered
agent_daemon gateway mounted
```

---

## 5. Smoke-test the chain

### Automated (production flow)

1. Create an Agent in the web UI with `daemon_mode: sandbox`.
2. Send a message to that Agent.
3. Server should: mint a pairing token → allocate a pod → daemon
   dials back via WebSocket → first response within 5-30s.

### Manual probe

```bash
# Confirm the pool is warm
kubectl -n sandbox-work get pods | grep parsar-daemon

# Exec into a pod
kubectl -n sandbox-work exec -it <pod-name> -c sandbox -- /bin/bash

# Inside the pod:
claude --version
parsar-daemon version

# Manually drive a connection (you need a pairing token from the
# admin shell first)
PARSAR_DAEMON_CONNECT_URL=https://parsar.example.com \
PARSAR_DAEMON_CONNECT_TOKEN=<pairing-token> \
parsar-daemon connect --device-name probe-sandbox -b

# Watch the log
tail -F ~/.parsar/parsar-daemon/default/connect.log
```

---

## 6. Rolling image updates

```bash
# 1. Tag a new image (locally or via the workflow on a `sandbox-v*` tag).
docker build --platform linux/amd64 \
  -t ghcr.io/<your-org>/parsar-sandbox:v0.0.X \
  infra/sandbox/parsar-daemon-claudecode/
docker push ghcr.io/<your-org>/parsar-sandbox:v0.0.X

# 2. Bump the `image:` field in sandboxset.yaml.

# 3. APPLY — never `delete`. The SandboxSet controller rolls warm-pool
#    pods automatically (maxUnavailable: 20% by default). Claimed pods
#    keep the old image until their natural TTL releases them.
kubectl apply -f infra/sandbox/parsar-daemon-claudecode/sandboxset.yaml

# 4. Watch the roll out
kubectl -n sandbox-work get sandboxset parsar-daemon -w
```

### ⚠️ Do NOT `delete sandboxset` or `delete pod <claimed>`

`delete sandboxset` removes every pod, including ones running live user
runs — they disconnect mid-prompt. The controller will respawn the pool,
but the in-flight user sees a hard sandbox failure.

To list which pods are currently claimed:

```bash
kubectl -n sandbox-work get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.metadata.labels.agents\.kruise\.io/sandbox-claimed}{"\n"}{end}' \
  | grep parsar-daemon
```

---

## 7. Troubleshooting

| Symptom | Likely cause | Investigation |
|---------|--------------|---------------|
| `availableReplicas: 0`, pods CrashLoopBackOff | envd binary missing from image | `kubectl describe pod <name>` — look for `exec: "envd": executable file not found in $PATH`. Re-build the image; the Dockerfile already copies envd from `e2bdev/base:latest`, regression usually means someone removed that COPY line. |
| Pods Pending forever | Cluster lacks resources | `kubectl describe pod <name>` → Events |
| Pod Running but envd port closed | envd failed to start | `kubectl exec` into pod, run `envd --version`, check `/proc/1/log` |
| daemon starts but cannot dial server | Network / `PARSAR_PUBLIC_URL` unreachable from pod | `kubectl exec` into pod, `curl -v $PARSAR_PUBLIC_URL/healthz` |
| 401/403 on dial | pairing token expired or signed by a different vault | Server log grep `authenticate`; rotate the pairing token |
| Claude does not respond | Claude CLI not installed or model not configured | Pod-side `claude --version`; check the Agent's model binding |
| `sandbox acquire: context deadline exceeded` (server) | Warm pool empty | `kubectl get sandboxset parsar-daemon` — `availableReplicas` should be > 0; if not, walk back through the rows above |

---

## 8. Tuning timeouts

| Parameter | Default | Notes |
|-----------|---------|-------|
| `SandboxAcquireTimeout` | 45s | Total cold-start budget |
| `SandboxConnectTimeout` | 30s | Wait for daemon WebSocket dial-in |
| `SandboxDefaultTTL` | 30 days | Long-running sandbox lifetime |
| `SandboxIdleReapThreshold` | 30 days | Idle reaper backstop; e2b TTL also applies |

---

## File reference

```
infra/sandbox/parsar-daemon-claudecode/
  Dockerfile          # Sandbox image definition
  sandboxset.yaml     # Standard SandboxSet (4 vCPU / 8 GiB pool)

infra/sandbox/parsar-daemon-claudecode-xl/
  sandboxset.yaml     # XL SandboxSet (4 vCPU / 50 GiB pool)

infra/e2b-templates/parsar-daemon-claudecode/
  e2b.Dockerfile      # e2b.app template (non-Kubernetes path)
  hooks/              # Claude / OpenCode hook scripts

.github/workflows/
  sandbox-image-release.yml   # ghcr.io build + push
  parsar-daemon-release.yml       # GitHub Releases for parsar-daemon binaries
```
