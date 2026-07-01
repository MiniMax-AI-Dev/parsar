# `deploy/compose/` — self-hosted deployment templates

This directory contains a **starting-point** Docker Compose layout for
running Parsar in a self-hosted environment (on-prem single host,
private staging, single-tenant production). It is **not** the dev
compose stack (`docker-compose.dev.yml` at the repo root) and **not** a
turnkey production deployment — it is the smallest concrete file pair
that proves the open-source binary can come up with config + secrets
injected from outside the repo.

## Files

| File | Purpose |
|---|---|
| `compose.selfhost.yml` | Two-service compose (`postgres` + `parsar-server`) with image / volumes / health checks. |
| `.env.example` | Every env var the compose file references, plus the env vars Parsar reads at runtime. All values are placeholders. |

## Three operator paths

Pick the one that matches your environment, then read the matching
section in `docs/deploy/deploy-runbook.md`:

1. **Single host, bundled Postgres** — use `compose.selfhost.yml` as-is.
   Bind-mount a config file in if you want; otherwise rely on env-only
   injection.
2. **Single host, external Postgres** — comment out the `postgres`
   service block in `compose.selfhost.yml`, override `DATABASE_URL` in
   `.env`, keep everything else.
3. **Kubernetes** — copy the env block from `compose.selfhost.yml`
   into your Deployment manifest, copy the health-check block into
   your `livenessProbe` / `readinessProbe` (per
   `docs/deploy/health-and-smoke.md`), and store secrets in a K8s
   `Secret` instead of `.env`.

## Where do real values come from?

**Never from this directory.** This whole folder is committed to the
open-source repo; any real secret here is a leak.

| Value class | Where it lives |
|---|---|
| Real image tag (your own registry path) | Set via `PARSAR_SERVER_IMAGE` in `.env` (or a CI variable) |
| Postgres password | Your secret manager → `.env` (uncommitted) |
| `PARSAR_MASTER_KEY` / `PARSAR_BOOTSTRAP_TOKEN` | Generated with `openssl rand -hex 32`, stored in secret manager, injected into `.env` (uncommitted) or container env |
| Feishu `app_id` / `app_secret` / `verification_token` | Feishu open platform → secret manager → `.env` |
| E2B API key | E2B dashboard → secret manager → `.env` |

## Image build contract

The `parsar-server` image referenced by `PARSAR_SERVER_IMAGE` ships its
binaries on `$PATH` at `/usr/local/bin`, so they are invoked by **bare
name** (NOT `./parsar-...` — the image's `WORKDIR` is `/var/lib/parsar`,
which does not contain them):

- `parsar-server` — the long-running HTTP server (the image `CMD`
  defaults to `/usr/local/bin/parsar-server`).
- `parsar-migrate` — the goose-driven migration runner used by the
  runbook's `docker compose ... exec parsar-server parsar-migrate` step.
- `parsar-bootstrap` — the first-owner provisioning CLI (used when the
  HTTP bootstrap endpoint is disabled).

The Dockerfile that builds this image lives at the repo root
(`Dockerfile`); `make docker-build` produces an image satisfying this
contract. Operators building their own image must install the same
binaries on `$PATH` for the runbook to work as written.

> The dev path (`make server` / `make migrate-dev`) does NOT need this
> image — it shells out via `go run ./cmd/server` / `go run ./cmd/migrate`.

## Verifying the compose file

After filling `.env`, run:

```bash
docker compose -f deploy/compose/compose.selfhost.yml --env-file deploy/compose/.env config
```

This expands every `${VAR}` and prints the merged config without
starting any container. CI can use the same command to keep the file
syntactically honest after future edits.

## Related docs

- `docs/deploy/deploy-runbook.md` — end-to-end runbook (Postgres → config → migration → bootstrap → smoke).
- `docs/deploy/config.example.yaml` — annotated YAML template covering every config knob.
- `docs/deploy/feishu-prod.md` — Feishu app + event-subscription wiring.
- `docs/deploy/health-and-smoke.md` — `/healthz`, `/readyz`, and the `scripts/smoke.sh` checker.
