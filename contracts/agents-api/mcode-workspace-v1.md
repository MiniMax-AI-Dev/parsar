# MiniMax Code workspace Runtime qualification

Status: implementation, public qualification and independent review passed,
2026-09-20. Baseline main
`55502c87c3f722bbefc5449348309e3fcc03a7ee`. Board batch:
`MCODE-WORKSPACE-V1-001`, under `ENGINE-EXTENSIBILITY-001`.

## Goal and boundaries

Complete the requested MiniMax Code Docker workspace loop through the existing
Core/Runtime contract: create a Session, prepare its Environment, execute native
tools, upload/list workspace files, export immutable Artifacts, cancel, reconnect
and continue the exact native history. Prove independent deployment without Parsar.
PR #702 qualified text execution only; it does not complete this goal.

Core owns the public protocol, authorization and durable resources. The Docker
Provider creates and reclaims one dedicated Runtime; daemon, native harness and
local tools share that Runtime. Native configuration and isolation belong in the
adapter. Reuse preparation ownership, localworkspace binding and common file and
export helpers. Do not add engine-name branches to public handlers, persistence or
scheduling, or implement another model/tool loop.

This batch excludes E2B, public MCP/functions/Subagents, a general plugin or sandbox
framework, product migration and unrelated refactoring. Existing qualified Codex
and Claude behavior remains protected. Optional feature equality is not required;
the explicitly requested workspace and Files/Artifacts loop is required.

## Design gate before hosted wiring

The inspected native source is `MiniMax-AI/minimax-code` revision
`33b259bbbeb1c16433390869938191d09bdb0680`, package 0.4.12. A deterministic
Linux ACP probe returned a private synthetic canary through Read with sandboxing
enabled and `denyRead` configured. The disabled control also returned it. This
probe used no real secrets or real model and is not acceptance evidence.

The native registry supplies sandbox operations to Bash only; native file tools
execute in the harness process that owns model configuration and history. Docker
alone does not separate those tools from that process's private data.

A first prototype moved six native tools into an isolated worker. Ordinary tool
operations, disabled networking and detached-child cancellation passed in Docker,
as did real Kimi and MiniMax Write/Read/Bash calls. However, the native process
still auto-started a workspace `.mcp.json` command and followed a `CLAUDE.md`
symlink into private data. Both bypasses were reproduced with synthetic canaries.
Native pre-tool file capture also reads paths before tool execution. The registry
patch is therefore not an adequate authority boundary and will not be shipped.

The revised candidate retains the published CLI and uses its existing MCP client.
The native process and ACP Session use one private control directory. A single
trusted stdio MCP bridge exposes the original Read, Write, Edit, Bash, Grep and
Glob implementations under workspace-prefixed names, using upstream JSON schemas.
The bridge runs tools in the upstream-vendored Linux sandbox. Only this isolated
worker receives the real workspace as its execution root. Automatic native
project loading remains confined to the private control directory; caller files
cannot configure native processes or redirect privileged instruction reads.

This uses MCP as an adapter-internal transport, not public MCP feature admission.
Keep native model execution, ACP and history. Reuse native tools rather than
reimplement them. Workspace project instructions can be read through the isolated
tools; do not silently import them into the privileged control process. Preserve
accurate workspace guidance in the trusted tool descriptions.

Before hosted wiring, prove the published CLI actually selects these tools,
rejects private-file reads, ignores workspace MCP/instruction canaries, enforces
network policy and settles all owned workers on cancellation or transport loss.
Repeat real-provider acceptance against this candidate; earlier prototype results
do not qualify it. If the bridge needs a new model loop or a general compatibility
framework, stop and reassess rather than weakening isolation.

## Acceptance and stop conditions

| Area | Required observable result |
| --- | --- |
| Shared lifecycle | Preparation consumes no model input; Start transfers ownership once; cancellation/release settle once after effects stop; events stay ordered. |
| Workspace | Real model reads uploaded input, edits/creates files and runs a native command in the bound workspace. |
| Files and Artifacts | Official SDK and raw HTTP verify actual content, path filtering and pagination; exports remain immutable and scoped to their owner. |
| Recovery | Reconnect and cold continuation retain exact native history and workspace; no duplicate execution; missing or foreign history rejects before model input. |
| Isolation | Tenant/auth checks, daemon/model credentials, native history, staging and cross-Session paths remain protected; tools cannot escape through filesystem or process aliases. |
| Network and cancellation | Each exposed network mode is enforced; cancellation leaves no late file writes or surviving detached tool children. |
| Deployment | Separate Core and execution database with Docker-managed Runtime, no Parsar service or product database dependency. |
| Validation | Targeted/race checks, real Kimi and MiniMax API runs, `make check`, applicable generated artifacts and a fresh independent Astra high blind review. |

Synthetic fixtures may isolate failures but cannot replace real provider acceptance.
Record exact source/image versions, commands, results and unverified behavior. Keep
credentials in private operator files, outside commits and reports. New mechanisms
must address this batch's demonstrated functional or security risk.

The batch ends when this loop passes and its bounded PR is merged. Record
nonblocking issues in the board without expanding the batch. Do not close the
parent protocol objective or claim complete official protocol compatibility.

## Qualification evidence

The initial qualification candidate uses published CLI 0.4.12, the source revision above, Node.js
22.23.1 and Docker image
`sha256:9bfcf2c3a1bcf2ebf844878897d561dacb6a525c938c5d059645d0f88e1fdb99`.
The npm registry still reported 0.4.12 as latest on 2026-09-20. The Core was built
with `scripts/build-agents-api.sh` and run from a source-free package with its own
PostgreSQL database and credentials.

| Check | Result |
| --- | --- |
| Official SDK 3.13.0 and raw HTTP, real Kimi, standalone Docker workspace | Passed: Session admission/auth/tenant scope, Files upload/list/filter/order/pages, native input reads, both network policies, protected paths, Core restart, delayed Runtime reconnect, cancellation with no late writes. |
| Public Artifacts, real Kimi and MiniMax independently | Passed: binary/empty/nested/source-file exports, exact content, immutable versions, ownership, restart, cancelled-Turn exclusion, Environment expiry and orphan-free deletion. |
| Core SIGKILL during execution, real Kimi | Passed: failed Turn queries, no automatic/idempotent replay, stopped effects, exact history continuation, retained Items/Artifacts and foreign-Session isolation. |
| Runtime SIGKILL during execution, real MiniMax | Passed the same recovery checks, including continuation that did not execute the interrupted command again. |
| Lifecycle and repository checks | `make check`, targeted Go race tests and the existing published-CLI product ACP regression passed. `make openapi` produced no generated changes; no query/schema change requires sqlc regeneration. |

Evidence is retained on `zju_a100_2` under
`~/.parsar/remediation/20260919/mcode-workspace/`: `public-hosted-latest.json`,
`public-artifacts-latest.json`, `public-minimax-artifacts-latest.json`,
`public-recovery-latest.json`, `public-minimax-recovery-diagnostic-latest.json`,
`race.log`, `make-check.log` and `product-native-regression.log`. These private
fixtures reuse the fixed official client and existing public Files/Artifacts
acceptance helpers; they do not substitute model responses. A locally cached base
image matching the pinned Node manifest was used with Docker's legacy builder;
only its temporary build context omitted BuildKit chmod syntax.

Kimi's Runtime-kill continuation did not pass the no-repeat assertion: after a new
input, a new tool-call identity executed the interrupted command again. The
pre-input recovery and idempotency checks passed. Keep the failed evidence and
source inspection confirms ACP preserves native call IDs and the native loop
executes only calls from a fresh model response after the new input. This was a
new model-issued command, not automatic recovery replay. The passing MiniMax run
does not erase the Kimi failure. Model obedience is not an exactly-once execution
guarantee; no model-output patch or public command filter was added.

The initial-image real MiniMax isolation probe also passed: workspace MCP and
instruction-symlink canaries did not affect the privileged process; native file
tools and `/proc` aliases could not read synthetic credentials/history; visible
process environments contained no privileged values. Missing and foreign native
Session loads rejected before any model request. All task-owned probe containers
were removed. Evidence: `final-isolation/docker-final-isolation-result.json`.

The final candidate is
`sha256:da8ab6840fe33bf493771419d68fe271ad155ff34f2c258e43bd6cd0a79260ec`.
It directs native temporary files to the existing writable scratch directory and
includes the upstream tool license. The earlier image failed the same native
temporary-file regression; the final image passed under both network policies:
`mktemp`, Node temporary paths, 272 KB Bash output spill and native Read. Private
canaries remained unreadable, real private directories were unchanged, the control
directory stayed read-only, and network/seccomp restrictions remained enforced.
MCP cancellation and EOF stopped detached children with no delayed writes.

On this final image, real MiniMax public Artifacts and both Core/Runtime SIGKILL
recovery suites passed with no cleanup errors. Full `make check`, focused race
tests and OpenAPI generation also passed after the change. The default full check
skips the opt-in packaged native test; both actual Docker runs above executed it.
The broader initial-image acceptance remains evidence for unchanged public wiring
and isolation, rather than a claim that every suite reran on the final image.
Final evidence: `public-minimax-recovery-latest.json`,
`public-minimax-artifacts-latest.json`, and `proof/temp-regression/`.

A fresh independent GPT-6 Astra high review of the complete diff found no blocking
issues. Its independent mcode/CLI/engine/execution race tests and companion checks
passed; the reviewer did not rerun the full gate or real-provider deployment.
Those results above were run by the implementation owner. This qualification does
not establish complete protocol compatibility.
