# Two-engine workspace placement

This decision serves the Codex/Claude single-Agent milestone. It does not enable
a public profile or change the pinned [Environment contract](environments.md).
Ownership rules remain in [CONTRIBUTING.md](../../CONTRIBUTING.md#environment-ownership-and-placement).

## Current implementation and missing prerequisites

| Boundary | Codex | Claude Agent SDK |
| --- | --- | --- |
| Native pin | 0.153.4, commit `3d2ee51ca2d5db578f328aa75e20aa22c0197c9a` | SDK 0.3.269, native 2.1.269 |
| Public execution | `none` and the accepted `self_hosted` remote-executor profile | `none`, with built-in command/file tools disabled |
| Workspace placement | Separate native executor; harness cwd is not the remote workspace | Isolated co-location is the candidate; changing cwd alone is insufficient |
| Preparation | Ready before input promotion; Start retains the same native preparation | No workspace preparation capability yet |
| History | Retained native history on the bound device, separately from executor workspace | Managed native state and exact resume for the restrictive profile; workspace continuation needs validation |
| Files | A shared native manager was proven privately; production transport/lifetime composition remains missing | Native tools can access a local workspace; public Files and an authorized idle owner remain missing |
| Cancellation | Owned-command cancellation verified; auxiliary process cleanup still has a recorded failure | Restrictive-profile process release verified; sandboxed command effects/descendants need validation |

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

Before Files admission, define a bounded adapter-side owner for the exact
Environment generation. Execution and file access use that same authorized
workspace context. Core checks resource ownership and scheduling; the adapter
owns native transport and translation. Each mutation must reject superseded
ownership, including at the executor. Lease loss stops new mutations and closes
the transport; uncertain effects remain unknown rather than being replayed.
Specify idle capacity, expiry/revocation and release independently of Run
completion. Releasing transient credentials must not delete caller-owned files
or required native history. A replacement socket alone never authorizes overlap.

The pinned Codex raw app-server runner cannot inject the privately proven shared
manager. Its injectable in-process route can drop notifications on saturation.
A second connection, a larger downstream queue or host-local `fs/*` against a
remote workspace does not solve that production seam. Native core integration
or a maintained upstream entrypoint requires a separately accepted change.

## Acceptance and next slice

Start with synthetic credential/file/socket canaries using the pinned native
tools. Stop on disclosure, bypass or fallback; never widen access to obtain a
passing result. Then use a real provider for native command/file effects, fresh
process continuation of the same workspace/history, cancellation with separately
observed process/effect cessation, and missing-history safe failure. This private
prerequisite does not establish public Session preparation, Files or deployment.

Only after that proof, add the smallest private typed Claude workspace profile
and native observations while preserving existing `none` behavior. Public
preparation and an idle Files owner are later independently accepted slices.
Use the same fixed SDK/raw HTTP and real execution acceptance for both public
engines before claiming the complete milestone.

`NATIVE-COMMAND-OUTPUT-001` remains a material Codex output-loss blocker for
complete retained output and full-loop acceptance. The recorded failed real run
is not fixed by a later gated success. No production-ready maintained remedy was
verified in the checked upstream sources; do not fabricate output, repair model
prose or silently adopt a native fork. Reassess that dependency from the full
board alongside other material security, state and data-loss issues.
