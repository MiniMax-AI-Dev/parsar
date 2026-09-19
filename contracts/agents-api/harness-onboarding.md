# Add a native harness to Agent Core

This reference is for adapter developers using the shared contract on main after
PR #701 (2026-09-19). Start with a working native SDK or machine-readable protocol.
The goal is to register an engine without changing public handlers, storage or the
scheduler. Codex, Claude and additional harnesses have equal architectural status;
each engine qualifies its own supported operations.

The [harness contract](harnesses.md) is the semantic baseline. The
[contributor guide](../../CONTRIBUTING.md#harness-qualification-and-onboarding)
owns architecture and delivery rules. The pinned external Agents API in
[upstream.json](upstream.json) remains separate from this private adapter contract.

## Ownership and implementation locations

| Component | Responsibility | Existing location |
| --- | --- | --- |
| Core | Public protocol, authentication, resource ownership, durable state and scheduling | `services/agents-api` |
| Runtime | Authenticated connection, dispatch, input receipts and preparation ownership | `apps/parsar-daemon/internal/dispatch` |
| Adapter | Native configuration, process/SDK calls, event translation and native restrictions | `apps/parsar-daemon/internal/agent/<kind>` |
| Harness | Model/tool loop and native history | Pinned upstream SDK or executable |
| Service profile | Pure validation of qualified placements and option values | `services/agents-api/internal/engine` |
| Runtime registration | Factory and verified capabilities available in this installation | `apps/parsar-daemon/internal/cli` |

A service profile authorizes supported combinations. A Runtime advertisement says
what that installation can execute. Neither replaces public schema validation,
tenant authorization or the other boundary. Native limits belong in adapters and
profiles, not engine-name branches in Core.

## Implement the existing interfaces

Use `internal/agentdaemon/proto` for requests, events, receipts and errors. Do not
introduce a parallel wire protocol or another model/tool loop.

| Interface | When required | Observable obligation |
| --- | --- | --- |
| `agent.Factory` and `agent.Session` | Every harness | Start a run; cancel idempotently; own output closure and native process lifetime |
| `agent.DurableSteerer` | Current public text execution contract | Distinguish a complete native write from confirmed application; return rejection/inactive/not-ready accurately |
| `agent.PreparationFactory` and `agent.Prepared` | Placements requiring preparation | Prepare without consuming model input; transfer ownership once in Start; retain failed cleanup ownership |
| `agent.PreparedCancellation` | Executable preparations | Cancel the same resource across Start; settle only after native effects and output writes stop |
| `agent.PermissionResponder`, `agent.UserChoiceResponder` | Only when emitting those interactions | Route exact interaction identities and preserve application receipts |
| Other optional interfaces | Only for declared operations | Implement the existing operation semantics and validate native support |

Read the source comments in `registry.go`, `steering.go`, `preparation.go` and
`interactions.go` before implementing. Base `Session.Cancel` is a cancellation
signal; its return alone is not proof that all effects stopped. Use the existing
router's settlement path and adapter outcome interfaces. Do not give both the
adapter and router ownership of closing the same output channel.

Preserve event order and native call identities. Emit one run terminal outcome;
never manufacture applied input, usage counters or successful cleanup. Resume only
the native history bound to the execution Session. Missing or ambiguous history
fails before new model input. A disconnected observer does not authorize replay.

## Register a supported operation set

1. Pin the upstream source/package version and document the native entry point.
2. Implement the adapter using its SDK or native protocol. Reuse shared process,
   credential/configuration and local workspace helpers where applicable.
3. Register its `SupportedAgentKind` and factory with `RegisterKind`; register
   preparation afterward with `RegisterPreparation` when supported. Runtime
   advertisements must describe behavior verified for that installation.
4. Add a profile to the existing static catalog, or supply an immutable catalog
   through service composition. Custom composition supplies the same
   `execution.Policy` to `api.WithExecutionPolicy` and `Dispatcher.Policy`.
5. Package the native prerequisites and select the engine through operator
   configuration (`AGENTS_API_ENGINE`). Do not invent a public harness field.

The static registration surface requires a build. Dynamic plugins are outside
this contract. A small adapter does not remove the need for native qualification.
The runnable test-only example is
[`testdata/onboarding/main.go`](../../apps/parsar-daemon/testdata/onboarding/main.go).
It registers a text-only synthetic harness and is never shipped as a real engine.

## Required behavior versus optional operations

The current public text path requires durable turns, applied input receipts,
ordered observations, cancellation and enforcement of disabled execution controls.
Check `execution.Policy.engineCapabilities` for the exact current requirements.
An engine without native tools can guarantee their absence; an engine with tools
must actually disable them when requested. Configuration acceptance is not proof
of enforcement.

MCP, public function calls, image inputs, verbosity controls and other optional
operations do not need to match another engine. Reject unqualified combinations
explicitly and record the gap. Never advertise a capability to bypass selection.

Hosted workspace execution additionally requires verified preparation, workspace
reads/output export, network behavior and credential/history isolation. Reuse the
same dedicated Runtime binding and shared Files helpers. A native Bash sandbox
alone does not establish isolation for other native file tools. Enable a placement
only after its required security and lifecycle behavior is demonstrated.

## Acceptance and delivery

Before implementation, record the operation set, expected results, exclusions and
stopping conditions in the board. A batch ends when its declared operations pass;
it does not expand to match another harness's feature list.

- Adapter tests: native failures, ordered events, input write/application receipts,
  cancellation settlement, strict continuation and unknown-outcome handling.
- Shared integration: public admission, actual Worker/device selection, gateway,
  daemon registration/dispatch and durable terminal projection. See
  `TestThirdHarnessPublicOnboarding` for a synthetic example, not native evidence.
- Real acceptance: pinned official Python SDK and raw HTTP against our Agents API,
  real provider API, native harness and independent execution database. Verify
  initial execution, follow-up input, cancellation and restart/continuation.
  For hosted qualification also verify Files/Artifacts, workspace identity,
  credential protection and foreign-history rejection.
- Regression: existing qualified engines keep working. Run targeted tests during
  development, then `make check` and applicable real regressions. API changes
  require `make openapi`; query changes require `make sqlc-generate`.
- Review: use a fresh independent Astra high reviewer for shared, lifecycle or
  security changes. Supply requirements, criteria, boundaries, rules, repository
  and baseline only. Resolve material findings; defer documented low-value work.

Record exact revisions, image/package versions, commands, results and limits.
Keep keys in private operator files; never commit them or include them in logs or
Feishu. Failed or synthetic runs cannot be counted as real acceptance. Merge the
bounded PR after required checks/review, update its board child and reassess the
full board. Acceptance of one engine is not complete public protocol compatibility.

## MiniMax Code application

The upstream project is [MiniMax-AI/minimax-code](https://github.com/MiniMax-AI/minimax-code).
The repository already contains an ACP stdio adapter under `agent/mcode`, including
native session creation/loading, events and human interactions. Its existing
product integration is not Agents API qualification. The current service catalog
registers Codex, Claude Code and MiniMax Code. Text qualification in #702 and
workspace qualification in #703 are separate recorded milestones.

The implementation reuses that adapter with an explicit native 0.4.12 opt-in for
`environment:none` text execution. It adds native active-input receipts and
cancellation settlement, a pure service profile and verified registration. No
public handler, store schema or scheduler engine branch is needed. Existing
product behavior and accepted Codex/Claude features remain protected.

See [the MiniMax Code deployment guide](../../services/agents-api/deploy/mcode/README.md)
for setup, real-provider acceptance and limits. Hosted workspace, Files/Artifacts,
public functions and MCP are not qualified by the text profile. Native differences
remain separately tracked work; they do not require feature equality for onboarding.

The requested MiniMax Code workspace Runtime is qualified separately in
[MCODE-WORKSPACE-V1-001](mcode-workspace-v1.md). Its explicit scope includes
workspace execution and shared Files/Artifacts, cancellation/recovery and independent
Docker deployment. Text-only qualification is an intermediate milestone for that
scope, not completion of the requested Runtime integration.
