# Codex colocated Runtime

These operator inputs select the V1 native isolation profile. The explicit service
configuration below enables basic Docker-hosted admission; it does not replace
the accepted caller-managed `self_hosted` path. Use one dedicated sandbox and retained
home/workspace per Session. Core stays outside it. Do not mount another Session's
history, host home, Docker socket, or product/Core credentials.

The shared Runtime and its two management modes are defined in
[the contributor guide](../../../../CONTRIBUTING.md#product-and-execution-service-separation).
User-managed installation and enrollment are outside this qualification batch.

Use Codex 0.153.4 and its matching `codex-resources` directory. Install the immutable
requirements file at `/etc/codex/requirements.toml`, mount only the authorized
Environment parent at `/environment`, containing only `workspace` and `staging`
directories on one mount for trusted writes. Retain daemon/native state beneath `/home`. Set
`PARSAR_CODEX_PERMISSION_PROFILE=managed-workspace` on the daemon. This operator
setting enables adapter selection of the immutable Runtime network profile at
startup and on both new/resumed threads;
it also filters native shell inheritance to process essentials, retaining default
secret exclusions. Native shell snapshots are disabled for this managed path:
their private storage is unavailable inside the tool sandbox, so loading one would
abort the pinned harness's login-shell wrapper. Ordinary shell startup remains
native. Model credentials remain available to the trusted harness;
it is not a request option or a public capability. Missing/invalid native profiles
must fail, without falling back to an unrestricted run. Ordinary deployments leave
this setting unset. It is incompatible with remote/none/read-only preparations.

The requirements file is outside the workspace and read-only. It fixes the allowed
profile and denies reads of daemon authentication, generated provider configuration
and native history under the declared daemon state layout. Minimal native reads
exclude other home contents; native helper aliases under the Session tmp directory
remain readable so the pinned harness can start its sandbox and apply_patch.
Only `/environment/workspace` is writable by native tools. Staging and its ancestors
are unavailable for native tool writes; staging is also explicitly denied for reads.
The image includes enabled and disabled native network profiles with the same
filesystem restrictions. Bootstrap freezes `PARSAR_RUNTIME_NETWORK_ACCESS`;
preparation must match it, and the adapter selects the native profile. Public
omission defaults to enabled. Restricted domains are unimplemented and rejected.
The trusted harness and daemon retain model/Core connectivity with either policy.
Do not expose private stock filesystem RPC as public Files.
Public file access needs the existing bounded, authorized filesystem primitives.

The Docker candidate uses a non-root user, no capabilities, no-new-privileges,
read-only root, private PID namespace, dedicated bridge networking, bounded tmpfs
and process/memory/CPU limits. Stock Codex's inner bubblewrap sandbox needs user,
mount and PID namespaces. `seccomp.json` retains the Moby default restrictions and
adds clone, unshare, setns, mount, umount2 and pivot_root for that inner sandbox.
No host capability or privileged container is required. On the tested AppArmor
host, Docker's default profile denies namespaced mounts; the candidate uses
container-specific `apparmor=unconfined`. This removes that outer LSM layer, so the
native sandbox, outer namespace/capability restrictions and actual isolation
checks remain required. Do not alter the host-wide AppArmor or seccomp policy.

Seccomp source: [Moby profiles, revision 65adc7e](https://github.com/moby/profiles/blob/65adc7e022c97f55e45c054ff012988027733b87/seccomp/default.json), Apache-2.0.
Unmodified source SHA-256:
`785b2429264afba4d594320337cb17f144f3c7d51585f9805eef72e28f4f9334`.
The final extra syscall rule is the only change to the parsed upstream profile.

Before treating this as a qualified deployment, verify native workspace execution,
credential/symlink/process-metadata denial, permission downgrade rejection, real
model execution, cancellation, daemon/container restart with retained history and
files, and missing-history rejection. An alive container or a selected profile is
not an isolation or public API acceptance result. Qualification applies only to
the selected deployment profile, not complete protocol compatibility or another
engine/provider.

The optional local file writer uses the existing `agents-api-codex-write` binary
outside `/environment`, with `PARSAR_RUNTIME_WRITE_HELPER` selecting that immutable
executable and `PARSAR_RUNTIME_STAGING=/environment/staging`. Set
`PARSAR_RUNTIME_WORKSPACE=/environment/workspace`; the public file path remains
`/workspace/...` and Core sends only the relative path to the bound Runtime.
Docker mounts the existing volume's `workspace` subdirectory at `/workspace` as
a second view of the same files. The image contains the initial directory before
volume population, so bootstrap works with an empty volume. Native tools receive
only that additional workspace root; staging remains private. Trusted atomic
rename stays within `/environment`. A symlink is insufficient because stock Codex
mounts canonical roots. The qualified Docker version is 29.1.3; the engine must
support volume subpath mounts. Other versions need their own deployment checks.
Workspace and staging must share the same mount for atomic rename. Do not mount
them separately or put daemon/model credentials, native history or other tenants
inside `/environment`. The read-only Runtime can omit both writer settings.

The installer runs as a trusted bounded daemon child with a minimal environment.
Native tools retain their narrower filesystem policy. Qualify direct reads,
symlink and process-root aliases, attempted staging modification, real uploaded
bytes consumed by Codex, cancellation and retained-history restart before using
this writer profile for public admission. Configuration alone is not that proof.

## Managed Runtime image and Docker adapter

Build the existing Rust filesystem helpers with `make build-agents-executor`, and
extract the official npm package `@openai/codex@0.153.4-linux-x64` beneath
`~/.parsar/`. Set `AGENTS_RUNTIME_CODEX_PACKAGE` to its extracted `package` directory
and run `scripts/build-agents-runtime.sh`. It builds the existing daemon and
prepares a binary-only Docker context at `~/.parsar/build/agents-runtime`; build
that context with the printed Docker command. This initial image is Linux amd64.
The package includes the unmodified native executable and matching resources.
It does not contain the product server, product CLI, credentials or workspace data.

The service's `internal/sandbox` interface has five operations. Its Docker adapter
uses the official Moby Go client and an operator-selected immutable image digest,
network, installation UUID and the contents of this directory's `seccomp.json`.
The Runtime authenticates outward through the ordinary daemon bootstrap path;
`CoreURL` includes the existing `/api/v1` gateway prefix. The image's default
entrypoint is the same daemon connect command used by a user-managed Runtime.
No socket, host home or product configuration is mounted inside the Runtime.

The caller persists a fresh allocation UUID with the authorized tenant and
Environment before Create, and serializes lifecycle operations for that allocation.
Create returns the reference even on failure. Duplicate allocation creation does
not rewrite credentials or restart the container. After a lost response, inspect
the allocation and reconcile its actual state; do not blindly replay Create.
Core owns durable allocation reconciliation through its internal managed Runtime
coordinator. Public admission requires the explicit operator configuration below.

Two labelled named volumes retain native state and the workspace/staging pair.
The trusted daemon auth profile is copied with restrictive permissions before
startup; it does not enter image layers, environment variables, labels or arguments.
GetInfo describes observed compute state, not daemon or native readiness. Docker
has no renewable provider lease: Renew verifies the allocation, while Core owns
keepalives and expiry. Kill verifies allocation ownership, removes
the container, explicitly removes its named volumes and confirms absence. Keep the
reference and retry cleanup when an operation fails; an HTTP timeout is not proof
that a resource disappeared. Never use broad container or volume pruning.

RunCommand is for trusted initialization, using an explicit context deadline,
argument vector and nonroot user. It preserves nonzero status and limits each
output stream to1MiB. Disconnecting an exec stream does not stop the command:
an unconfirmed result requires allocation cleanup before reuse. Routine agent
execution, cancellation and Files continue through Core/daemon/Runtime.

## Standalone operator configuration

Set `AGENTS_API_DAEMON_WS_URL` to the outward URL reachable from the Runtime and
`AGENTS_API_MANAGED_RUNTIMES_FILE` to a private JSON file beneath `~/.parsar/`:

```json
{
  "core_url": "https://core.example/api/v1",
  "default_provider": "11111111-1111-4111-8111-111111111111",
  "docker": {
    "11111111-1111-4111-8111-111111111111": {
      "host": "unix:///var/run/docker.sock",
      "image": "sha256:<qualified immutable image digest>",
      "network": "bridge",
      "seccomp_file": "/absolute/path/to/seccomp.json"
    }
  }
}
```

The provider key identifies this Docker installation permanently. Keep its entry
while any allocation needs cleanup; changing the endpoint requires a new key.
An empty default disables new hosted admission/bootstrap while preserving existing
Session controls, input retry outcomes and cleanup of retained allocations.
V1 supports explicit local Unix Docker sockets, ignoring ambient
`DOCKER_HOST`. Optional `extra_hosts` is trusted operator configuration. No Docker
socket is mounted in a Runtime. User-managed enrollment remains separate work.

For a trusted model endpoint, `AGENTS_API_EXECUTION_OPTIONS_FILE` can supply the
existing adapter options as a JSON object, including `codex_provider` with `name`,
`base_url`, `bearer_token` and `wire_api`. Protect this file with mode 0600; it is
read at startup, copied for each execution and never persisted as public Session
configuration. Omit it for the adapter's existing model configuration. Changes
require a Core restart. Do not place credentials in public requests or images.

With the qualified Codex image and default provider configured, create an idle or
initial-text Session using `environment: {"type": "openai_hosted"}`. Core commits
its identity before automatically provisioning it. Queries expose durable
connection status; execution separately prepares the native harness. Session
deletion revokes authority before owned container/volume cleanup. Supported network
policies are enabled and disabled. Templates, populated startup installations,
restricted domains and hosted MCP combinations remain explicit gaps.
