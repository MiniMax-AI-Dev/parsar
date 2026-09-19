# MiniMax Code text Runtime

This profile targets `environment:none` through the same Core/daemon execution
contract as the other engines. It has no public workspace, Files, Artifacts, MCP,
function calls, image input or native Subagent execution. Those are separate
qualification tasks, not requirements to match Codex or Claude.

## Pinned prerequisite

Use the official [`@minimax-ai/code`](https://github.com/MiniMax-AI/minimax-code)
package version **0.4.12**, with Node.js 22.22.x and its native SQLite dependency.
The inspected upstream source is `33b259bbbeb1c16433390869938191d09bdb0680`.
Install outside the checkout, under a private operator directory in `~/.parsar/`.
Check the native install succeeds and `mcode --version` reports exactly 0.4.12.
The ordinary Parsar product image retains its existing package pin; this profile
uses an explicitly selected native executable on a trusted execution host.

Set `PARSAR_MCODE_BIN` to that absolute executable and `PARSAR_MCODE_AGENTS_API=1`
for the daemon. The opt-in only advertises the profile for the qualified version.
Use the existing authenticated daemon connection and operator device enrollment;
this is not a new public enrollment API or official `self_hosted` implementation.
Set `AGENTS_API_ENGINE=mcode` in the independent Core deployment. Existing Sessions
retain their engine. Do not expose a new public harness selector.

## Provider configuration

Keep `AGENTS_API_EXECUTION_OPTIONS_FILE` private (mode 0600). Supply the existing
`mcode_provider` native custom-provider shape, for example:

```json
{
  "mcode_provider": {
    "name": "Parsar",
    "kind": "custom",
    "enabled": true,
    "npm": "@ai-sdk/anthropic",
    "options": {
      "baseURL": "https://api.moonshot.cn/anthropic",
      "apiKey": "<private provider key>"
    },
    "models": {
      "kimi-k3": {
        "name": "Kimi K3",
        "tool_call": true,
        "limit": {"context": 64000, "output": 4096}
      }
    }
  }
}
```

The public Agent model must appear in this managed provider's native model list.
The adapter selects it explicitly; it does not use a native account fallback.
Use the real provider endpoint for the selected credential. Test proxies may
forward requests unchanged and capture tool names/status, but must not synthesize
model responses or log keys.

## Execution boundaries

Each API Session uses a separate native state directory and cwd. The execution
child inherits only process and model-network essentials; it does not inherit
Core/daemon tokens or arbitrary Node startup configuration. The adapter owns its
native config, instructions and home and disables external skills, delegated work,
web search, file/shell tools, browser tools, mcode-tools and native goals.

The native model may still see `skill`, `task_query`, `task_output` and `task_stop`.
The first loads an exact registered skill name and cannot execute a script; the
configured builtin/external skill catalog is empty. Task utilities cannot create
work and enforce native Session ownership. Auxiliary native title requests are
internal bookkeeping. Do not equate these with public function or workspace tools.

ACP `mcode/session/steer` acknowledges acceptance for the active native Turn,
separately from the transport write. It is not a promise that the model consumed
that input before cancellation. Unknown outcomes are never automatically replayed.
Cancellation waits for process-group exit and output settlement. Public slash
text remains a model message instead of invoking native ACP operator commands.

Cold continuation requires the exact persisted native Session ID and matching
private cwd. Missing/foreign history fails; recovery by guessing an ID from native
session listings is not qualified. Public usage breakdown is unavailable because
native ACP context occupancy and cumulative cost are not per-Turn usage.

## Acceptance

The opt-in `TestNativeMCodePublicExecution` uses the fixed official Python SDK,
raw HTTP, actual daemon/gateway/Worker and a dedicated PostgreSQL test database.
Provide private `PARSAR_MCODE_REAL_OPTIONS` (the provider object above plus the
`model` string), `PARSAR_MCODE_BIN`, `PARSAR_NATIVE_DAEMON_BIN`,
`PARSAR_NATIVE_PROOF_DIR`, `PARSAR_OFFICIAL_SDK_PYTHON` and
`PARSAR_AGENTS_API_TEST_DATABASE_URL`, then run:

```sh
go test ./services/agents-api/internal/store \
  -run '^TestNativeMCodePublicExecution$' -count=1 -v -timeout=15m
```

It verifies real text execution, same-Turn steering and durable input receipt,
daemon cold restart with the same native ID, ordinary slash text, foreign-tenant
rejection, cancellation and subsequent continuation. Run independently with real
Kimi and MiniMax options. This fixture creates only operator device credentials
privately; all tested Sessions and inputs enter through public HTTP.

`TestNativeMCodeHistoryIsolation` additionally takes
`PARSAR_MCODE_FOREIGN_NATIVE_ID` from a successful public run and verifies rejection
in another private native home, plus rejection of a nonexistent history ID. Run it
in the daemon's `internal/agent/mcode` package with the same private native/provider
options. It must fail before model input, without a replacement native Session.

On 2026-09-19, the public fixture passed independently with real Kimi K3 and
MiniMax M2.7 APIs. Both exercised execution, native steering receipts, daemon
restart, history continuation, tenant rejection and cancel/continue. Native
missing/foreign history rejection passed separately. Credential scans found the
provider key only in each private mode-0600 native config, not in logs, public
results or native history. The ordinary product's pinned 0.3.11 ACP regression
also passed new/resume, model/instruction refresh, Skill discovery and MCP using
its existing synthetic provider fixture.

This acceptance uses an in-process Core HTTP server and a separate real daemon;
it is not a standalone Core deployment certification. Hosted Docker isolation,
Files/Artifacts and complete official protocol compatibility remain unqualified.
Before release, record `make check`, race checks and independent review alongside
these native results in the board task.
