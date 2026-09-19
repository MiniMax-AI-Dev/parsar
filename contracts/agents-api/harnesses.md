# Native harness contract and qualification

Codex, Claude Code and future harnesses are equal execution engines. Core owns
public protocol, authority and durable state. Each adapter owns native
configuration, transport and process translation;
the native harness owns the model/tool loop. Supporting this contract
means implementing its observable semantics. Harnesses do not need identical
feature sets. Optional native limitations are separate capability work and do not
block completion of otherwise qualified onboarding.

## Integration surface

1. Implement the existing daemon `agent.Factory`/`Session` and, for prepared
   environments, `PreparationFactory`/`Prepared` interfaces. `Session` requires
   cancellation; adapters that emit permission or user-choice requests additionally
   implement `PermissionResponder` or `UserChoiceResponder`. Other optional
   interfaces, such as function results, follow their declared operations. Reuse
   `internal/agentdaemon/proto` requests, neutral events, input receipts and errors.
2. Register the factory, preparation factory and verified Runtime capabilities in
   the daemon registry. Keep native translation inside the adapter. Dedicated local
   environments reuse shared Files/write/export helpers and binding checks.
3. Add a pure qualified profile to `services/agents-api/internal/engine` and its
   static catalog (or supply an immutable catalog at service composition). Declare
   supported placements, public configuration/result limits
   and required Runtime controls. A profile uses existing public/protocol types;
   it has no database, credential-decryption or native-process responsibilities.
4. Supply the native deployment prerequisites. Verify common lifecycle behavior
   and run public acceptance for each declared operation. Do not require MCP,
   functions, images, verbosity control or another engine's optional features simply
   to register a harness.
   No new handler, store table, scheduler, event projector or model loop is needed
   for capabilities already represented by the contract.

The catalog is the explicit service qualification boundary; a Runtime heartbeat
cannot authorize new public functionality. Unknown profiles fail closed. Schema
validity, qualified service support and the available Runtime remain independent
checks. Additional capability combinations require evidence, not an engine-name
exception. The static registry requires a build to add an implementation; dynamic
plugin loading and untrusted code execution are outside this design.

New Session selection currently uses the operator's `AGENTS_API_ENGINE` setting;
existing Sessions retain their engine. The default is a deployment convenience,
not a different contract or authority level. There is no invented public `harness`
field. Future selection changes must respect the pinned public protocol.

## Shared behavioral obligations

- Preparation holds resources without consuming input; start transfers ownership
  once. Unused preparation releases through `Close`; cancellation remains valid
  across the transfer and reports settlement only after native effects stop.
- Confirm accepted/applied inputs separately. Preserve ordered public Items and
  events, stable call identity and one terminal outcome. Never replay uncertain
  work merely because a connection closed.
- Resume only the bound Session's native history. Missing, ambiguous or foreign
  history fails closed. Device identity is not native Session ownership.
- Native tools and public Files operate on the same authorized workspace.
  Generated code cannot access daemon/model credentials or foreign history.
- Emit verified measurements; absence of native usage detail is not a zero value.
  Explicit unsupported operations remain implementation gaps in protocol coverage.

## Current qualified operations

The baseline is actual supported behavior on main, not everything Codex accepts
syntactically or everything either upstream harness can theoretically perform.

| Operation | Codex | Claude Code |
| --- | --- | --- |
| Docker hosted text execution, native local tools | Qualified | Qualified |
| Files, immutable Artifacts, cancellation, restart/history recovery | Qualified | Qualified |
| Public functions in `none` | Qualified | Qualified; object-root schemas and text results |
| Public functions alongside hosted workspace tools | Qualified | Qualified; object-root schemas and text results |
| HTTP MCP and static-bearer Vault credentials in `none` | Qualified | Qualified subset |
| Required MCP initialization | Qualified | Qualified on `none`; native readiness before initial input |
| Hosted HTTP MCP | Gap | Gap |
| Function image results | Supported subset | Gap; currently rejected |
| Non-default verbosity | Native/model-dependent support | No equivalent qualified; medium only |
| Public detailed Usage | Supported native counters | Native raw usage retained; public breakdown gap |
| Official `self_hosted` remote executor path | Existing native Codex path | Not qualified; requires separate design |
| Explicit reasoning, structured output, enabled `multi_agent`, message images | Shared service gaps | Shared service gaps |

This inventory records supported combinations, not a feature-equality checklist.
Do not silently drop options, fabricate measurements, weaken isolation or remove
working features. Unsupported operations stay explicit; implementing them is a
separate board decision, not an onboarding prerequisite. User-managed colocated Runtime enrollment is a
separate queued feature; it is not a substitute for official `self_hosted`.

## Common contract acceptance

The synthetic [third-harness fixture](../../apps/parsar-daemon/testdata/onboarding/main.go)
implements only the current text execution contract: cancellation, durable active
input receipts and strict bound-history continuation. It has no workspace, MCP,
public functions, permissions or user-choice handlers. Its registration is local
to the fixture; production builds never register it.

`TestThirdHarnessPublicOnboarding` uses a custom immutable `engine.Catalog` in the
same `execution.Policy` supplied to both the API handler and Dispatcher. The zero
policy selects built-ins; an explicitly empty catalog authorizes no engines.
The test runs public Session/input admission, Worker device selection, the real
WebSocket gateway, daemon Registry/Router, neutral events and durable terminal
projection. It checks applied input receipts, saved native identity, continuation,
cancellation, unsupported optional requests and missing mandatory Runtime support.
This proves the integration path, not real native execution or sandbox security.
The existing internal registration functions suffice for this fixture; no dynamic
registry or global mutable test registration is required.

The current text execution contract still requires durable input/Turn semantics,
ordered observations and enforcement of disabled execution controls. An adapter
without tools or subagents can guarantee their absence; it must not pretend to
apply unsupported requested behavior. Hosted qualification has additional workspace
and isolation obligations. These guarantees are independent of feature equality.

## Native operation acceptance

Use the pinned official Python SDK, raw HTTP and real model APIs. The common
`services/agents-api/tests/official_hosted_functions_native.py` assertions exercise
function success/error, native file output and public artifact bytes, same-history
continuation after restart, foreign result rejection and pending-call cancellation.
The operator fixture supplies only deployment/restart and model configuration;
public assertions are shared by adapters that support this operation. Existing workflow, file, artifact
and interruption fixtures remain applicable. Native isolation canaries supplement
these tests; synthetic responses alone do not establish live qualification.

The 2026-09-19 candidate passed the same hosted-function assertions with Codex
0.153.4 and Claude SDK 0.3.269/native 2.1.269 using real Kimi K3. Each run used
an independent Core, dedicated Agents API database and Docker Runtime. Cold
restart acceptance restores Core before restarting Runtime; daemon startup while
Core is unavailable is not qualified by this test. Evidence is retained under
`~/.parsar/remediation/20260919/harness-parity/` on the validation server.

The broader protocol inventory remains in [README.md](README.md). Passing one
profile or these shared assertions does not establish complete compatibility.
