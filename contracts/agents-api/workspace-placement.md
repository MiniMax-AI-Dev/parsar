# Two-engine workspace placement

This decision serves the Codex/Claude single-Agent milestone. It does not enable
a public profile or change the pinned [Environment contract](environments.md).
Ownership rules remain in [CONTRIBUTING.md](../../CONTRIBUTING.md#environment-ownership-and-placement).

## Current implementation and missing prerequisites

| Boundary | Codex | Claude Agent SDK |
| --- | --- | --- |
| Native pin | 0.153.4, commit `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a` | SDK 0.3.269, native 2.1.269 |
| Public execution | `none` and the accepted `self_hosted` remote-executor profile | `none`, with built-in command/file tools disabled |
| Workspace placement | Separate native executor; harness cwd is not the remote workspace | Private typed factory binding inside a separately qualified outer placement; changing cwd alone is insufficient |
| Preparation | Ready before input promotion; Start retains the same native preparation | Private SDK/Go prepare-start ownership is qualified; public workspace admission remains closed |
| History | Retained native history on the bound device, separately from executor workspace | Managed native state and exact resume; private workspace continuation has explicit real-provider acceptance |
| Files | A shared native manager was proven privately; production transport/lifetime composition remains missing | Native tools can access a local workspace; public Files and an authorized idle owner remain missing |
| Cancellation | Owned-command cancellation verified; auxiliary process cleanup still has a recorded failure | Private workspace factory acceptance checks cancellation and effect cessation; arbitrary escaped descendants are not qualified |

Keep Codex's accepted remote path. Qualify Claude's maintained `query()` entry
inside a dedicated execution environment, retaining the native model/tool loop.
Do not force every adapter through Codex's transport or introduce a replacement
loop. A public `self_hosted` implementation must still support the documented
caller-started executor flow; a private daemon URL or an extra installation step
cannot silently replace it.

## Claude isolation prerequisite

There are two separate boundaries. The deployment excludes broader application,
daemon and other-tenant credentials from the harness environment and mounted
files. Within that deployment, native controls prevent generated operations from
reading selected model/MCP credentials or rewriting native history. Directory
bindings, a custom process spawner and permission callbacks alone prove neither.

The pinned SDK exposes `SandboxSettings`, replacement `Options.env`, `Options.settings`
and `query()`. Linux Bash uses bubblewrap, separate user/PID namespaces and the
native executable's bundled seccomp helper. Native Read/Edit run in the trusted
harness and use permission rules. A sandbox `denyRead` entry is not automatically
a Read permission deny; the documented merge works in the opposite direction.

The first bounded profile must qualify these native controls together:

- Explicit runtime environment; only the selected credentials enter native code.
  Deny their variable names to sandboxed commands. Keep secret and history roots
  outside the workspace, with both sandbox read/write denies and native Read/Edit
  denies. Check workspace symlinks as well as direct paths.
- Empty user/project/local setting sources, fixed native tool inventory, explicit
  outside-workspace read restrictions and no bypass permission mode. Operator
  policy remains relevant; absence of project settings is not absence of policy.
- Sandbox enabled with `failIfUnavailable`, no unsandboxed fallback, no excluded
  commands and no weaker nested/network isolation. Verify the actual seccomp
  helper and socket restrictions; a warning-only dependency check is insufficient.
- Separate persistent workspace and protected native history. Check history before
  resume, preserve the native Session identity and fail before new work when
  required history is absent. An Environment ID is not a backup.

These are supported native configuration surfaces, not completed production
isolation. The source review used the pinned `sdk.d.ts` and shipped native binary
(SHA-256 `25e44883f54419569a3d739f38cbbdaebe83b09895da0f343e1b003710a4775b`).
Upstream [sandbox documentation](https://code.claude.com/docs/en/sandboxing) and
[deployment guidance](https://code.claude.com/docs/en/agent-sdk/secure-deployment)
provide context; current documentation does not replace the pinned source.

## Execution and file ownership

`execution.RunEnvironmentInput` currently owns a connection through preparation
and one Run, then releases it. That is not an idle file owner. Existing durable
connection generations fence observations; they do not revoke an old process or
an already-dispatched file write.

Determine prerequisites for each public operation under the
[Core/Runtime rules](../../CONTRIBUTING.md#environment-ownership-and-placement).
Environment metadata retrieval already reads durable resources without preparing
execution. Files.list needs live path/size metadata from the authorized workspace;
the private bounded byte reader alone does not implement that route. Reuse native
directory/metadata access and existing lifecycle interfaces through a thin adapter.
Bound a live read to its exact authorized Environment/workspace context, including
when idle, and retain permission, path-isolation and unavailable-runtime checks.
Do not require a complete write, replacement or retirement mechanism for this read.

For file mutations and owner replacement, reject superseded ownership, including
at the executor. Lease loss stops new mutations and closes the transport; uncertain
effects remain unknown rather than being replayed. A replacement socket alone never
authorizes overlap. Define capacity and release for the lifetime actually used by
the operation; a permanent idle owner is not a universal prerequisite. Releasing
transient credentials must not delete caller-owned files or required native history.

The tracked exact-pin raw-runner hook now publishes its stock-built manager;
private Files/execution/cancel/history composition is qualified. The injectable
in-process route can drop notifications on saturation, while the maintained raw
socket client's consumer queue is unbounded. The optional private harness artifact
therefore uses stock raw stdio with the existing Go RPC and a separate local
metadata socket into the same manager. It does not create a second executor pair
or call host-local `fs/*` for a remote path. Patch ownership, exact builds and
acceptance are defined in the [artifact guide](../../packages/codex-harness/README.md).
This does not enable public Files, a reusable idle owner or full transport bounds.

## Acceptance and next slice

Start with synthetic credential/file/socket canaries using the pinned native
tools. Stop on disclosure, bypass or fallback; never widen access to obtain a
passing result. Then use a real provider for native command/file effects, fresh
process continuation of the same workspace/history, cancellation with separately
observed process/effect cessation, and missing-history safe failure. This private
prerequisite does not establish public Session preparation, Files or deployment.

The temporary `mx` placement qualified the pinned native controls and real
workspace/history continuation under a locked runtime identity and explicit
mount/PID boundary. It is not a production placement: CPU/memory/pids limits,
disk quotas, provider-only trusted-harness egress and concurrent tenants remain
unqualified. Default Docker and the tested gVisor version failed the strict native
sandbox prerequisite; do not reuse either unchanged as a supported placement.

The private typed Claude factory profile composes those controls in the existing
bridge, retaining `none` behavior. Its separate acceptance must use that actual
factory and bridge; the earlier direct native proof alone is insufficient.
Native command/file observations, public preparation and an idle Files owner
are later independently accepted slices.
Use the same fixed SDK/raw HTTP and real execution acceptance for both public
engines before claiming the complete milestone.

`NATIVE-COMMAND-OUTPUT-001` remains a material Codex retained-output gap. The user
has deferred it from the current principal-workflow milestone acceptance on the
task board; this does not establish complete output fidelity. The recorded failed real run
is not fixed by a later gated success. No production-ready maintained remedy was
verified in the checked upstream sources; do not fabricate output, repair model
prose or silently adopt a native fork. Reassess that dependency from the full
board alongside other material security, state and data-loss issues.
