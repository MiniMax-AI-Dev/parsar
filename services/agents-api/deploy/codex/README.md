# Codex co-location qualification profile

These private deployment inputs select the V1 native isolation candidate. They do
not enable public hosted admission, provision an Environment, or replace the
accepted caller-managed `self_hosted` path. Use one dedicated sandbox and retained
home/workspace per Session. Core stays outside it. Do not mount another Session's
history, host home, Docker socket, or product/Core credentials.

The shared Runtime and its two management modes are defined in
[the contributor guide](../../../../CONTRIBUTING.md#product-and-execution-service-separation).
User-managed installation and enrollment are outside this qualification batch.

Use Codex 0.153.4 and its matching `codex-resources` directory. Install the immutable
requirements file at `/etc/codex/requirements.toml`, mount only the authorized
workspace at `/workspace`, and retain daemon/native state beneath `/home`. Set
`PARSAR_CODEX_PERMISSION_PROFILE=managed-workspace` on the daemon. This operator
setting selects the native profile at startup and on both new/resumed threads;
it also filters native shell inheritance to process essentials, retaining default
secret exclusions. Model credentials remain available to the trusted harness;
it is not a request option or a public capability. Missing/invalid native profiles
must fail, without falling back to an unrestricted run. Ordinary deployments leave
this setting unset. It is incompatible with remote/none/read-only preparations.

The requirements file is outside the workspace and read-only. It fixes the allowed
profile and denies reads of daemon authentication, generated provider configuration
and native history under the declared daemon state layout. Minimal native reads
exclude other home contents; native helper aliases under the Session tmp directory
remain readable so the pinned harness can start its sandbox and apply_patch.
Only `/workspace` is writable. Native commands have no network access in this initial profile; the trusted harness and
daemon still need their model/Core connections. Do not claim upstream network
configuration support or expose private stock filesystem RPC as public Files.
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
not an isolation or public API acceptance result. Provider admission and lifecycle,
public Files, source uploads and default deployment cutover remain separate work.
