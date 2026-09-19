# MiniMax Code Runtime

MiniMax Code uses the same Core/Runtime execution contract as the other engines.
The text profile supports `environment:none`; the dedicated Docker profile adds
workspace execution and the shared Files/Artifacts path. See
[workspace qualification](../../../../contracts/agents-api/mcode-workspace-v1.md)
for exact acceptance evidence and limits.
Public MCP/functions, image input and native Subagent execution remain outside
this batch.

## Pinned prerequisite

Use the official [`@minimax-ai/code`](https://github.com/MiniMax-AI/minimax-code)
package version **0.4.12**, with Node.js 22.x and its native SQLite dependency. The Docker image pins Node.js
22.23.1; the text fixture used 22.22.0.
The inspected upstream source is `33b259bbbeb1c16433390869938191d09bdb0680`.
Install outside the checkout, under a private operator directory in `~/.parsar/`.
Check the native install succeeds and `mcode --version` reports exactly 0.4.12.
This profile runs on a trusted execution host.

Set `PARSAR_MCODE_BIN` to that absolute executable and `PARSAR_MCODE_AGENTS_API=1`
for the daemon. The opt-in only advertises the profile for the qualified version.
Use the existing authenticated daemon connection and operator device enrollment;
this is not a new public enrollment API or official `self_hosted` implementation.
Set `AGENTS_API_ENGINE=mcode` in the independent Core deployment. Existing Sessions
retain their engine. Do not expose a new public harness selector.

## Docker workspace

Build the shared workspace helpers, install the published CLI into a private
operator directory, and build the companion from the pinned native source:

```sh
MCODE_NATIVE_SOURCE=/absolute/minimax-code bash scripts/build-mcode-harness.sh
MCODE_CLI_DIR=/absolute/published-package \
MCODE_HARNESS_BUILD_DIR=/absolute/built-companion \
bash scripts/build-mcode-runtime.sh
docker build --platform linux/amd64 -t agents-runtime:mcode \
  "${PARSAR_HOME:-$HOME/.parsar}/build/mcode-runtime"
```

Configure Core's existing managed Docker provider with the immutable image ID,
`deploy/codex/seccomp.json` and `nested_sandbox: true`. Core, database ownership,
enrollment and the public protocol remain shared. The image supplies the private
`PARSAR_MCODE_WORKSPACE=managed` and companion paths; caller Agent options cannot
change them. Public Files and Artifacts use the common bound workspace helpers.

The native process and ACP Session use a private control directory, while six
original native tools execute against `/workspace` through one trusted MCP bridge
and the upstream Linux sandbox. MCP is internal transport here; it does not enable
caller-supplied public MCP servers. Project instructions must be read through the
workspace tools. Native automatic project configuration and diff/undo capture do
not apply to this isolated tool path. Exact native-ID continuation remains
required; recovery without a recorded ID fails closed. Native Bash observations
become public `command_execution` items after command arguments arrive. Their
text output and status are retained; absent native exit code/duration stay unknown.
The private MCP server and file/skill/task utilities are not invented public MCP
or function calls.

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
web search, builtin file/shell tools, browser tools, mcode-tools and native goals.
The workspace profile adds only its trusted isolated tool bridge.

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
results or native history. Product ACP regression uses the same 0.4.12 package
and covers new/resume, model/instruction refresh, Skill discovery and MCP using
its existing synthetic provider fixture.

That text acceptance used an in-process Core HTTP server and a separate real
daemon. The subsequent [Docker workspace qualification](../../../../contracts/agents-api/mcode-workspace-v1.md)
uses independently built Core binaries, its own database and managed Runtime.
It covers public Files/Artifacts, cancellation, reconnect, exact-history recovery
and isolation with real Kimi and MiniMax APIs. Read its recorded Kimi continuation
limitation: new model-issued commands are not automatic recovery replay. Neither
acceptance establishes complete official protocol compatibility.
