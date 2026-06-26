#!/usr/bin/env bash
#
# Self-hosted smoke test for Parsar.
#
# Goal: after an operator deploys a Parsar server (k8s, plain docker,
# bare metal — doesn't matter), give them a single command that proves
# the deployment is actually ready to take traffic. Distinct from the
# dev/fake stack, which depends on dev seed data + mock IM endpoints +
# a CWD next to the repo. smoke.sh assumes none of that:
#
#   - no dev seed data, no fake IM mocks;
#   - no dev auth headers (X-Parsar-Dev-User-ID is gated behind
#     PARSAR_DEV_AUTH and must NOT be set in production);
#   - no repo CWD requirement — runs from anywhere;
#   - no writes to the current working directory (all evidence lives
#     under ~/.parsar/smoke/, matching the project-wide
#     "runtime state under ~/.parsar/" hard rule).
#
# Two modes:
#
#   lite (default)  — three black-box probes:
#     1. GET /healthz       → 200, status=ok            (liveness)
#     2. GET /readyz        → 200, status=ok, every check[].status=ok
#                                                      (readiness incl. DB)
#     3. GET /api/v1/health → 200, status=ok            (legacy alias)
#
#   core            — lite plus the minimum product-loop assertions that
#                     a fresh deployment actually entered a usable
#                     state. Currently:
#     4. GET /api/v1/bootstrap/status reachable + shape-valid
#     5. /api/v1/bootstrap/status.dev_auth_enabled == false
#        (a production-profile deployment MUST NOT enable the
#        X-Parsar-Dev-User-ID shim — Validate() refuses to start
#        in production-profile with dev_auth=true, but the smoke
#        re-asserts it from the outside because the operator may
#        have switched profile after first start).
#     6. setup-state recognition (PASS regardless of needed=true/false;
#        FAIL only when the payload is missing the fields we depend on).
#     7. POST /api/v1/bootstrap (provision first owner) when
#        --bootstrap-token / $PARSAR_BOOTSTRAP_TOKEN is supplied AND
#        the install still needs setup; SKIP with a clear reason
#        otherwise.
#     8. POST /api/v1/bootstrap (idempotency) — once an owner exists,
#        a second POST must return 409 bootstrap_closed; SKIP without
#        a token.
#     9. runtime / audit / usage workflow chain — currently SKIP with
#        a TODO pointing at the missing /api/v1/workspaces/* surface
#        (the workspace-scoped business endpoints still live under
#        /dev/* behind the session middleware, which is unreachable
#        from an unauthenticated smoke).
#
# Exit code 0 on success, 1 on failure. SKIPs do NOT fail the run.
# Every FAIL line prints the failing endpoint, the HTTP status, and
# the response body so the operator can immediately see what the
# server complained about. Every SKIP line names the env/config that
# would have unlocked the check.
#
# Usage:
#   scripts/smoke.sh                                  # lite, http://127.0.0.1:8080
#   scripts/smoke.sh --api-url http://parsar:8080   # lite, custom URL
#   scripts/smoke.sh --core                           # core mode
#   scripts/smoke.sh --core --bootstrap-token "$T"    # core + try provisioning
#   PARSAR_API_URL=http://parsar:8080 scripts/smoke.sh --core
set -euo pipefail

API_URL_DEFAULT="${PARSAR_API_URL:-http://127.0.0.1:8080}"
API_URL="${API_URL_DEFAULT}"
PER_REQUEST_TIMEOUT_SECONDS=5
MODE="lite"
BOOTSTRAP_TOKEN_CLI=""
BOOTSTRAP_EMAIL="${PARSAR_BOOTSTRAP_EMAIL:-smoke@example.com}"
BOOTSTRAP_WORKSPACE="${PARSAR_BOOTSTRAP_WORKSPACE:-Smoke Workspace}"
BOOTSTRAP_NAME="${PARSAR_BOOTSTRAP_NAME:-Smoke Owner}"

usage() {
  cat <<HELP
Parsar self-hosted smoke test.

Usage:
  $(basename "$0") [--mode lite|core] [options]

Modes:
  lite (default)      Three black-box health probes only.
  core                Lite probes + bootstrap chain assertions.

Options:
  --mode MODE         lite (default) or core.
  --lite              Shortcut for --mode lite.
  --core              Shortcut for --mode core.
  --api-url URL       Base URL of the deployed Parsar server.
                      Default: \$PARSAR_API_URL or ${API_URL_DEFAULT}.
  --timeout SECONDS   Per-request timeout in seconds (default ${PER_REQUEST_TIMEOUT_SECONDS}).
  --bootstrap-token T Token to POST /api/v1/bootstrap with. May also be
                      supplied via \$PARSAR_BOOTSTRAP_TOKEN. When
                      omitted, the provisioning + idempotency checks
                      are SKIPped with a clear reason. Never written
                      to evidence files.
  --bootstrap-email E Email for the first owner (default \${PARSAR_BOOTSTRAP_EMAIL:-${BOOTSTRAP_EMAIL}}).
  --bootstrap-workspace N
                      Workspace display name (default \${PARSAR_BOOTSTRAP_WORKSPACE:-${BOOTSTRAP_WORKSPACE}}).
  --bootstrap-name N  Owner display name (default \${PARSAR_BOOTSTRAP_NAME:-${BOOTSTRAP_NAME}}).
  -h, --help          Show this help and exit.

Exit codes:
  0   no FAIL rows. PASS + SKIP rows allowed.
  1   at least one FAIL row; see stderr for the failing URL,
      HTTP status, and response body.

Evidence:
  All probe bodies / status codes land under
  ~/.parsar/smoke/<UTC-timestamp>/ — never the CWD.
HELP
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --mode)
      [[ $# -ge 2 ]] || { echo "[smoke] --mode requires a value" >&2; exit 2; }
      MODE="$2"; shift 2 ;;
    --lite) MODE="lite"; shift ;;
    --core) MODE="core"; shift ;;
    --api-url)
      [[ $# -ge 2 ]] || { echo "[smoke] --api-url requires a value" >&2; exit 2; }
      API_URL="$2"; shift 2 ;;
    --timeout)
      [[ $# -ge 2 ]] || { echo "[smoke] --timeout requires a value" >&2; exit 2; }
      PER_REQUEST_TIMEOUT_SECONDS="$2"; shift 2 ;;
    --bootstrap-token)
      [[ $# -ge 2 ]] || { echo "[smoke] --bootstrap-token requires a value" >&2; exit 2; }
      BOOTSTRAP_TOKEN_CLI="$2"; shift 2 ;;
    --bootstrap-email)
      [[ $# -ge 2 ]] || { echo "[smoke] --bootstrap-email requires a value" >&2; exit 2; }
      BOOTSTRAP_EMAIL="$2"; shift 2 ;;
    --bootstrap-workspace)
      [[ $# -ge 2 ]] || { echo "[smoke] --bootstrap-workspace requires a value" >&2; exit 2; }
      BOOTSTRAP_WORKSPACE="$2"; shift 2 ;;
    --bootstrap-name)
      [[ $# -ge 2 ]] || { echo "[smoke] --bootstrap-name requires a value" >&2; exit 2; }
      BOOTSTRAP_NAME="$2"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "[smoke] unknown arg: $1" >&2; usage >&2; exit 2 ;;
  esac
done

case "${MODE}" in
  lite|core) ;;
  *) echo "[smoke] --mode must be 'lite' or 'core', got: ${MODE}" >&2; exit 2 ;;
esac

# Strip any trailing slash so the URL we print and the URL we curl
# always match exactly.
API_URL="${API_URL%/}"

# Resolve the bootstrap token from CLI flag first, env var second. CLI
# wins so a `PARSAR_BOOTSTRAP_TOKEN=stale-value scripts/smoke.sh --core --bootstrap-token fresh`
# does what the operator intended.
BOOTSTRAP_TOKEN="${BOOTSTRAP_TOKEN_CLI:-${PARSAR_BOOTSTRAP_TOKEN:-}}"

# Evidence directory under ~/.parsar/smoke/<ts>/ — never the
# CWD. The hard rule (AGENTS.md "All runtime config, logs, state, and
# cache must live under ~/.parsar/") applies to ops scripts too.
PARSAR_HOME="${PARSAR_HOME:-$HOME/.parsar}"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
EVIDENCE_DIR="${PARSAR_HOME}/smoke/${RUN_TS}"
mkdir -p "${EVIDENCE_DIR}"

# Preflight: curl and python3 (json parsing). Bail with a helpful
# error rather than trace-reading later.
for cli in curl python3; do
  if ! command -v "${cli}" >/dev/null 2>&1; then
    echo "[smoke] required CLI missing: ${cli}" >&2
    exit 2
  fi
done

PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

record_pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  echo "[smoke] PASS $*"
}

record_fail() {
  FAIL_COUNT=$((FAIL_COUNT + 1))
  echo "[smoke] FAIL $*" >&2
}

record_skip() {
  SKIP_COUNT=$((SKIP_COUNT + 1))
  echo "[smoke] SKIP $*"
}

# http_get <label> <relative-path>
#
# Issues a GET against ${API_URL}${path}, captures HTTP status + body
# into the evidence dir. Returns 0 on a usable response (any status
# code that is not "000" / network error) and prints the status into
# stdout; returns non-zero on transport failure. Body lives at
# ${EVIDENCE_DIR}/${label}.body, status at ${EVIDENCE_DIR}/${label}.status,
# curl stderr at ${EVIDENCE_DIR}/${label}.curl.err. Callers inspect
# those files; we deliberately do not echo bodies to the terminal
# because bodies can be long.
http_get() {
  local label="$1" path="$2"
  local url="${API_URL}${path}"
  local body_path="${EVIDENCE_DIR}/${label}.body"
  local status_path="${EVIDENCE_DIR}/${label}.status"

  local http_code
  http_code="$(curl -sS \
    -o "${body_path}" \
    -w '%{http_code}' \
    -m "${PER_REQUEST_TIMEOUT_SECONDS}" \
    "${url}" 2> "${EVIDENCE_DIR}/${label}.curl.err" \
    || true)"
  printf '%s\n' "${http_code}" > "${status_path}"
  printf '%s' "${http_code}"

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    return 1
  fi
  return 0
}

# http_post_json <label> <relative-path> <bearer-token> <body-json>
#
# Same shape as http_get but POSTs application/json. Bearer token may
# be empty — in that case no Authorization header is sent. The request
# body is delivered via --data-binary to avoid any shell-escaping
# surprises with embedded newlines.
http_post_json() {
  local label="$1" path="$2" token="$3" body="$4"
  local url="${API_URL}${path}"
  local body_path="${EVIDENCE_DIR}/${label}.body"
  local status_path="${EVIDENCE_DIR}/${label}.status"
  local -a headers=(
    -H "Content-Type: application/json"
  )
  if [[ -n "${token}" ]]; then
    headers+=(-H "Authorization: Bearer ${token}")
  fi

  local http_code
  http_code="$(curl -sS \
    -X POST \
    "${headers[@]}" \
    --data-binary "${body}" \
    -o "${body_path}" \
    -w '%{http_code}' \
    -m "${PER_REQUEST_TIMEOUT_SECONDS}" \
    "${url}" 2> "${EVIDENCE_DIR}/${label}.curl.err" \
    || true)"
  printf '%s\n' "${http_code}" > "${status_path}"
  printf '%s' "${http_code}"

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    return 1
  fi
  return 0
}

# describe_failure <label> <http_code>
#
# Renders the FAIL message body that goes to stderr — short, single
# line, includes the URL the user can paste into curl. Body content is
# trimmed because bootstrap_closed responses are tiny but a 500 body
# can be a multi-line Go panic; the operator already has the full
# file at ${EVIDENCE_DIR}/${label}.body.
describe_failure() {
  local label="$1" http_code="$2"
  local body_text
  body_text="$(head -c 500 "${EVIDENCE_DIR}/${label}.body" 2>/dev/null || true)"
  printf '%s — http=%s body=%s' "${label}" "${http_code}" "${body_text}"
}

############################################################
# lite mode: 3 health probes (unchanged contract)
############################################################

# Validator: HTTP 200 + body has status=="ok" (used by both /healthz
# and /api/v1/health, which share the {status, name} envelope).
LIVENESS_VALIDATOR='
import json, sys
code = sys.argv[1]
if code != "200":
    sys.stderr.write(f"expected http=200, got {code}\n")
    sys.exit(1)
try:
    body = json.load(sys.stdin)
except Exception as exc:
    sys.stderr.write(f"body is not JSON: {exc}\n")
    sys.exit(1)
if body.get("status") != "ok":
    sys.stderr.write(f"expected status=ok, got {body!r}\n")
    sys.exit(1)
if body.get("name") != "parsar":
    sys.stderr.write(f"expected name=parsar, got {body!r}\n")
    sys.exit(1)
'

# Validator: HTTP 200 + every checks[].status == "ok". A check row
# with status=fail / not_configured fails the smoke run and the
# operator sees which dependency was the problem.
READYZ_VALIDATOR='
import json, sys
code = sys.argv[1]
if code != "200":
    sys.stderr.write(f"expected http=200, got {code}\n")
    sys.exit(1)
try:
    body = json.load(sys.stdin)
except Exception as exc:
    sys.stderr.write(f"body is not JSON: {exc}\n")
    sys.exit(1)
checks = body.get("checks") or []
if not checks:
    sys.stderr.write(f"readyz returned no checks: {body!r}\n")
    sys.exit(1)
bad = [c for c in checks if c.get("status") != "ok"]
if bad:
    sys.stderr.write(f"readyz checks not all ok: {bad!r}\n")
    sys.exit(1)
if body.get("status") != "ok":
    sys.stderr.write(f"readyz envelope status not ok: {body!r}\n")
    sys.exit(1)
'

# probe_lite_endpoint <label> <relative-path> <validator-py>
#
# lite-mode wrapper: GETs the path and runs the validator. Records a
# PASS row on success and a FAIL row otherwise. Returns 0 either way
# — callers decide whether a failure here should short-circuit
# subsequent checks (lite always runs all three; core uses
# probe_lite_endpoint for the lite triplet and then short-circuits
# bootstrap if bootstrap/status is itself unreachable).
probe_lite_endpoint() {
  local label="$1" path="$2" validator="$3"
  local url="${API_URL}${path}"
  local http_code
  http_code="$(http_get "${label}" "${path}")" || true

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    local err_text
    err_text="$(cat "${EVIDENCE_DIR}/${label}.curl.err" 2>/dev/null || true)"
    record_fail "${label} ${url} — no HTTP response (curl: ${err_text:-unknown error})"
    return
  fi

  if ! python3 -c "${validator}" "${http_code}" < "${EVIDENCE_DIR}/${label}.body"; then
    record_fail "$(describe_failure "${label}" "${http_code}") (url=${url})"
    return
  fi

  record_pass "${label} ${url} — http=${http_code}"
}

############################################################
# core mode: bootstrap chain assertions
############################################################

# bootstrap_status_shape: must be HTTP 200 + JSON containing the five
# typed fields the openapi contract documents. We deliberately do not
# enforce a specific boolean value here — that is what the dedicated
# dev-auth / state-recognition checks are for. We also stash the raw
# state into a file the later steps source so we do not curl twice.
BOOTSTRAP_STATUS_VALIDATOR='
import json, sys
code = sys.argv[1]
if code != "200":
    sys.stderr.write(f"expected http=200, got {code}\n")
    sys.exit(1)
try:
    body = json.load(sys.stdin)
except Exception as exc:
    sys.stderr.write(f"body is not JSON: {exc}\n")
    sys.exit(1)
required = {
    "needed": bool,
    "has_owners": bool,
    "owner_count": int,
    "http_enabled": bool,
    "dev_auth_enabled": bool,
}
for key, want in required.items():
    if key not in body:
        sys.stderr.write(f"bootstrap status missing field {key!r}: {body!r}\n")
        sys.exit(1)
    val = body[key]
    if want is bool and not isinstance(val, bool):
        sys.stderr.write(f"bootstrap status field {key} should be bool, got {type(val).__name__}: {val!r}\n")
        sys.exit(1)
    if want is int and not isinstance(val, int):
        sys.stderr.write(f"bootstrap status field {key} should be int, got {type(val).__name__}: {val!r}\n")
        sys.exit(1)
'

# get_status_field <field> — emits the boolean / int value of one
# field from the cached bootstrap status body. Python is the parser
# of record so we never grep a JSON shape.
get_status_field() {
  local field="$1"
  python3 -c "
import json, sys
with open('${EVIDENCE_DIR}/bootstrap_status.body') as f:
    body = json.load(f)
val = body.get('${field}')
print('true' if val is True else 'false' if val is False else val)
"
}

# probe_bootstrap_status: PASS iff status endpoint reachable + shape
# matches openapi contract. On FAIL, all downstream bootstrap_* checks
# SKIP with a clear "depends on bootstrap_status" reason rather than
# spewing redundant 503s.
probe_bootstrap_status() {
  local label="bootstrap_status"
  local path="/api/v1/bootstrap/status"
  local url="${API_URL}${path}"
  local http_code
  http_code="$(http_get "${label}" "${path}")" || true

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    local err_text
    err_text="$(cat "${EVIDENCE_DIR}/${label}.curl.err" 2>/dev/null || true)"
    record_fail "${label} ${url} — no HTTP response (curl: ${err_text:-unknown error})"
    return 1
  fi

  if ! python3 -c "${BOOTSTRAP_STATUS_VALIDATOR}" "${http_code}" < "${EVIDENCE_DIR}/${label}.body"; then
    record_fail "$(describe_failure "${label}" "${http_code}") (url=${url})"
    return 1
  fi

  local needed has_owners owners http_enabled dev_auth_enabled
  needed="$(get_status_field needed)"
  has_owners="$(get_status_field has_owners)"
  owners="$(get_status_field owner_count)"
  http_enabled="$(get_status_field http_enabled)"
  dev_auth_enabled="$(get_status_field dev_auth_enabled)"
  record_pass "${label} ${url} — http=${http_code} needed=${needed} has_owners=${has_owners} owners=${owners} http_enabled=${http_enabled} dev_auth_enabled=${dev_auth_enabled}"
  return 0
}

# probe_bootstrap_dev_auth_disabled: dev_auth=true on a real
# deployment means the operator left the X-Parsar-Dev-User-ID shim
# wired up — a hard security regression. We re-assert it from the
# outside because the operator could have flipped the YAML between
# the last process restart and now.
probe_bootstrap_dev_auth_disabled() {
  local label="bootstrap_dev_auth_disabled"
  local dev_auth_enabled
  dev_auth_enabled="$(get_status_field dev_auth_enabled)"
  if [[ "${dev_auth_enabled}" == "false" ]]; then
    record_pass "${label} — dev_auth_enabled=false"
    return
  fi
  record_fail "${label} — dev_auth_enabled=${dev_auth_enabled}; production deployments MUST set PARSAR_DEV_AUTH=false (X-Parsar-Dev-User-ID shim must stay off)"
}

# probe_bootstrap_state_recognized: PASS iff the payload told us
# something usable about the install state. Either needed=true (still
# fresh) or needed=false + owner_count>=1 (provisioned). FAIL on the
# pathological needed=false + owner_count=0 shape (means the field
# wiring is broken upstream).
probe_bootstrap_state_recognized() {
  local label="bootstrap_state_recognized"
  local needed owners
  needed="$(get_status_field needed)"
  owners="$(get_status_field owner_count)"
  if [[ "${needed}" == "true" && "${owners}" == "0" ]]; then
    record_pass "${label} — install awaiting first owner (needed=true, owner_count=0)"
    return
  fi
  if [[ "${needed}" == "false" && "${owners}" != "0" ]]; then
    record_pass "${label} — install already provisioned (needed=false, owner_count=${owners})"
    return
  fi
  record_fail "${label} — inconsistent payload: needed=${needed} owner_count=${owners}"
}

# probe_bootstrap_provision: try POST /api/v1/bootstrap when we have a
# token AND setup is still needed. Three terminal states:
#   - SKIP "no token"             — neither --bootstrap-token nor env
#   - SKIP "already provisioned"  — needed=false already; nothing to do
#   - SKIP "http surface disabled"— operator forgot to wire the token
#                                    server-side; surface as SKIP not
#                                    FAIL because the CLI carrier
#                                    (cmd/parsar-bootstrap) is the
#                                    fallback path documented in
#                                    deploy-runbook.md.
#   - PASS / FAIL                 — actual POST result.
#
# On PASS we mark a sentinel file the idempotency probe reads.
probe_bootstrap_provision() {
  local label="bootstrap_provision"
  local needed http_enabled
  needed="$(get_status_field needed)"
  http_enabled="$(get_status_field http_enabled)"

  if [[ -z "${BOOTSTRAP_TOKEN}" ]]; then
    record_skip "${label} — no bootstrap token supplied; set PARSAR_BOOTSTRAP_TOKEN or pass --bootstrap-token to exercise POST /api/v1/bootstrap"
    return
  fi
  if [[ "${needed}" == "false" ]]; then
    record_skip "${label} — install already provisioned (needed=false); fresh-setup PASS not testable, idempotency probe will still run"
    return
  fi
  if [[ "${http_enabled}" == "false" ]]; then
    record_skip "${label} — server reports http_enabled=false (PARSAR_BOOTSTRAP_TOKEN unset on the server); use the CLI fallback (cmd/parsar-bootstrap) instead"
    return
  fi

  # Body is rendered by python3 with values handed through env, NOT
  # interpolated into the python source string. Operator-supplied
  # values can carry apostrophes / quotes / newlines / backslashes
  # ("O'Hara", `My "Workspace"`, …) — shell-into-python interpolation
  # would crash the python parser before the script could emit a
  # FAIL row, swallowing the smoke summary under `set -e`.
  local body
  body="$(
    BOOTSTRAP_EMAIL="${BOOTSTRAP_EMAIL}" \
    BOOTSTRAP_NAME="${BOOTSTRAP_NAME}" \
    BOOTSTRAP_WORKSPACE="${BOOTSTRAP_WORKSPACE}" \
    python3 -c '
import json, os
print(json.dumps({
    "email": os.environ["BOOTSTRAP_EMAIL"],
    "name": os.environ["BOOTSTRAP_NAME"],
    "workspace_name": os.environ["BOOTSTRAP_WORKSPACE"],
}))
'
  )"
  local http_code
  http_code="$(http_post_json "${label}" "/api/v1/bootstrap" "${BOOTSTRAP_TOKEN}" "${body}")" || true

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    local err_text
    err_text="$(cat "${EVIDENCE_DIR}/${label}.curl.err" 2>/dev/null || true)"
    record_fail "${label} POST /api/v1/bootstrap — no HTTP response (curl: ${err_text:-unknown error})"
    return
  fi

  if [[ "${http_code}" != "201" ]]; then
    record_fail "$(describe_failure "${label}" "${http_code}")"
    return
  fi

  if ! python3 -c '
import json, sys
body = json.load(sys.stdin)
if body.get("setup_complete") is not True:
    sys.stderr.write(f"expected setup_complete=true, got {body!r}\n")
    sys.exit(1)
for f in ("user_id", "workspace_id", "workspace_slug", "member_id"):
    if not body.get(f):
        sys.stderr.write(f"missing field {f}: {body!r}\n")
        sys.exit(1)
' < "${EVIDENCE_DIR}/${label}.body"; then
    record_fail "$(describe_failure "${label}" "${http_code}")"
    return
  fi

  # Mark that a fresh provision happened so the idempotency probe
  # knows to verify the door is now closed.
  : > "${EVIDENCE_DIR}/bootstrap_provision.completed"
  record_pass "${label} — POST /api/v1/bootstrap returned 201 setup_complete=true"
}

# probe_bootstrap_idempotency: regardless of which side of the
# provision we are on (fresh provision just succeeded, or the install
# was already provisioned at smoke start), the second POST to
# /api/v1/bootstrap must return 409 bootstrap_closed. Requires a token
# to call the endpoint; SKIP otherwise.
probe_bootstrap_idempotency() {
  local label="bootstrap_idempotency"
  if [[ -z "${BOOTSTRAP_TOKEN}" ]]; then
    record_skip "${label} — no bootstrap token supplied; cannot POST to verify 409 bootstrap_closed"
    return
  fi
  local needed
  needed="$(get_status_field needed)"
  # If the install was already provisioned and we never touched it,
  # needed is "false" since the very first status call. If we did
  # provision in this run, the cached status body still says
  # needed=true (we did not re-fetch). Either way the door should be
  # closed for the second POST.
  if [[ "${needed}" == "true" && ! -f "${EVIDENCE_DIR}/bootstrap_provision.completed" ]]; then
    record_skip "${label} — install still needs setup and no provision PASS in this run; nothing to verify closed against"
    return
  fi

  # Hard-coded literals today, but rendered through the same
  # env-handoff + json.dumps shape as bootstrap_provision so the
  # pattern stays consistent and nobody regresses it later by
  # parameterising these strings without remembering to re-quote.
  local body
  body="$(python3 -c '
import json
print(json.dumps({
    "email": "idempotency-probe@example.com",
    "name": "Idempotency Probe",
    "workspace_name": "Idempotency Probe Workspace",
}))
')"
  local http_code
  http_code="$(http_post_json "${label}" "/api/v1/bootstrap" "${BOOTSTRAP_TOKEN}" "${body}")" || true

  if [[ -z "${http_code}" || "${http_code}" == "000" ]]; then
    local err_text
    err_text="$(cat "${EVIDENCE_DIR}/${label}.curl.err" 2>/dev/null || true)"
    record_fail "${label} POST /api/v1/bootstrap (second time) — no HTTP response (curl: ${err_text:-unknown error})"
    return
  fi

  if [[ "${http_code}" != "409" ]]; then
    record_fail "$(describe_failure "${label}" "${http_code}") (expected 409 bootstrap_closed)"
    return
  fi

  if ! python3 -c '
import json, sys
body = json.load(sys.stdin)
if body.get("code") != "bootstrap_closed":
    sys.stderr.write(f"expected code=bootstrap_closed, got {body!r}\n")
    sys.exit(1)
' < "${EVIDENCE_DIR}/${label}.body"; then
    record_fail "$(describe_failure "${label}" "${http_code}")"
    return
  fi

  record_pass "${label} — second POST returned 409 bootstrap_closed (door correctly latched)"
}

# probe_runtime_chain_todo: AgentRun / audit / usage are real product
# loops, but the corresponding HTTP surface still lives under
# /dev/{conversations,projects,workspaces}/* behind the session
# middleware. Hitting those from an unauthenticated smoke would
# either bounce off 401 or — worse — silently pass when an operator
# left PARSAR_DEV_AUTH=true on a production deployment. Until
# workspace-scoped business endpoints are promoted to /api/v1/* with
# real cookie sessions, this probe records SKIP and names the missing
# surface so the gap is visible in every smoke run.
probe_runtime_chain_todo() {
  record_skip "runtime_chain — AgentRun / audit / usage workflow not exposed under /api/v1/*; today the only authenticated surface is /dev/* behind cookie session middleware (or the dev shim, which production must keep off). TODO: promote /api/v1/workspaces/{wid}/projects/{pid}/agent-runs, .../audit-records, .../usage (read-only summary) so a real session cookie can drive smoke-core."
}

############################################################
# Entry: run lite probes; in core mode add bootstrap chain
############################################################

echo "[smoke] target ${API_URL}"
echo "[smoke] mode ${MODE}"
echo "[smoke] evidence dir ${EVIDENCE_DIR}"

probe_lite_endpoint healthz       "/healthz"       "${LIVENESS_VALIDATOR}"
probe_lite_endpoint readyz        "/readyz"        "${READYZ_VALIDATOR}"
probe_lite_endpoint v1_health     "/api/v1/health" "${LIVENESS_VALIDATOR}"

if [[ "${MODE}" == "core" ]]; then
  if probe_bootstrap_status; then
    probe_bootstrap_dev_auth_disabled
    probe_bootstrap_state_recognized
    probe_bootstrap_provision
    probe_bootstrap_idempotency
  else
    record_skip "bootstrap_dev_auth_disabled — depends on bootstrap_status reachability"
    record_skip "bootstrap_state_recognized — depends on bootstrap_status reachability"
    record_skip "bootstrap_provision — depends on bootstrap_status reachability"
    record_skip "bootstrap_idempotency — depends on bootstrap_status reachability"
  fi
  probe_runtime_chain_todo
fi

echo "[smoke] summary mode=${MODE} pass=${PASS_COUNT} fail=${FAIL_COUNT} skip=${SKIP_COUNT}"

if [[ "${FAIL_COUNT}" -gt 0 ]]; then
  echo "[smoke] ${FAIL_COUNT} probe(s) failed; see ${EVIDENCE_DIR}" >&2
  exit 1
fi

echo "[smoke] all required probes passed"
