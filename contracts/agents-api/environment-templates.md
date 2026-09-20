# Environment Templates and initialization

Core owns reusable configuration through the five pinned
[Template operations](https://github.com/openai/openai-python/blob/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents/environments/templates.py).
Templates do not contain a running workspace and do not select a provider image.
An E2B `templateID:build_UUID` remains private operator packaging configuration.
Each referencing Session obtains its own Environment through the same initialization
and five-operation SandboxProvider path as inline configuration.

## Supported batch

- Create, retrieve, update, delete and list under `/v1/agents/environments/templates`.
  Every operation requires project authentication and `OpenAI-Beta: agents=v1`.
  CRUD/list works without an execution deployment.
- Optional nullable name, preserved verbatim, with a local 1–256 Unicode character
  bound. Network supports `enabled` and `disabled`; omitted/null create network
  defaults to the pinned enabled policy. Update omission preserves; supplied name
  or network replaces, with null clearing name or resetting network.
- Empty/null installation fields retain empty defaults. Responses contain safe
  metadata and never `env`, `setup_commands` or inline file data. Initial files are
  supported as described below, together with env, ordered setup and npm/Python packages; remaining populated installations reject explicitly.
- Listing uses `after`, `limit` (1–100, default 20), and `order` (default `desc`).
  Creation timestamp plus ID supplies stable local ordering. Missing/foreign IDs
  and cursors return the same not-found result. No compute is allocated by CRUD.
- Session `environment_template_id` resolves under the caller's tenant. Omitted
  network inherits; enabled can narrow to disabled, never the reverse. Effective
  configuration is frozen without passing the template ID to execution.
- Updating/deleting a template does not change existing Sessions. Creation retries
  recover recorded caller intent before template lookup, including after deletion;
  changed intent conflicts. This is the existing local retry policy, not a claim of
  complete upstream idempotency semantics.

```python
from openai import OpenAI

client = OpenAI(base_url="https://your-core.example/v1", api_key="your-project-key")
template = client.beta.agents.environments.templates.create(
    name="Python workspace", network={"access": "disabled"},
    env={"APP_MODE": "analysis"}, packages={"python": ["packaging==26.0"]},
    setup_commands=[{"command": "mkdir -p /workspace/outputs"}]
)
session = client.beta.agents.sessions.create(
    agent={"model": "your-configured-model"},
    environment={"type": "openai_hosted", "environment_template_id": template.id},
    input="Create /workspace/outputs/report.txt containing the result of 6 * 7.",
)
# Inspect Session/Turn/Items and retrieve published Artifacts after completion.
# Delete the Session to reclaim its Environment; template deletion is independent.
```

## Initial files

Both inline hosted configuration and reusable templates accept `files` entries with
an absolute destination inside `/workspace`: `inline` with standard-base64 `data`, or
`file_id` referencing a project-owned Files API upload. The guide's limits are 50
initial files, 5 MiB per inline file, 10 MiB total inline content, and 50 MiB per
referenced file. Session/Template JSON requests allow 16 MiB for the base64 envelope.
Paths must be canonical, distinct and stay within the workspace; symlinks are not
followed. A failed install never starts native execution.

Configure `AGENTS_API_CREDENTIAL_KEY_FILE` with the existing execution-service
base64 32-byte encryption key. Template writes and Session resolution need it;
ordinary metadata reads do not. Template inline metadata contains type/path/size,
while references contain type/path/file_id. Sessions receive fresh file IDs and
sizes for both variants. Initialization keeps file data out of ordinary configuration,
resource responses, lifecycle events and command arguments. Templates keep references; each Session authorizes and
freezes its own encrypted source bytes. Later source deletion cannot change them.

Template `files` omission preserves on update; null/[] clears. Referenced Sessions
inherit files. Supplying `files` together with `environment_template_id`, including
null/[], explicitly rejects while replacement/merge/null semantics remain unconfirmed.
Use a complete standalone inline configuration when a different file set is needed.

Core initializes both paths with the same trusted file installer through Provider
RunCommand. Daemon authentication remains available, but native preparation and live
Files wait for all writes. Each file gets a two-minute transfer budget; the batch has
a thirty-minute local budget and shares maintenance scans with other allocations.
These are local operational limits, not verified upstream timing. Initial input
retains its existing five-minute admission deadline; large installations can use an
idle Session and wait for connected status before submitting input.

Uncertain writes and Core restart during initialization fail the new Environment and
reclaim it; they do not replay partial installation. After completion, reconnect and
native-history recovery preserve user modifications instead of reinstalling files.
Docker/E2B and all three harnesses use this same lifecycle. The Provider API remains
five operations; public Templates are never E2B image templates.

## Packaged Runtime initialization contract

Template handlers and stores resolve public configuration without choosing a
harness, native path or compute backend. The common initialization lifecycle uses
the following existing Linux Runtime packaging requirements through Provider
`RunCommand`; these are private deployment requirements, not public Template fields.

- `/workspace` is the public workspace. `/environment/workspace` names the same
  storage for trusted initialization; `/environment/staging` is private staging.
- `/usr/bin/python3 -I -S` runs the trusted, fd-anchored initial-file installer.
  It invokes the existing `/usr/local/bin/agents-api-codex-write` atomic writer.
  That executable is a shared filesystem helper packaged for every harness; its
  historical name does not select Codex or invoke native Codex tools.
- Confidential content travels on bounded stdin. Successful initialization needs
  the writer's versioned completion receipt and confirmed process exit. Unknown
  effects use the existing allocation cleanup path rather than replay.
- Provider implementations preserve argv, stdin, exit status and allocation
  ownership. They do not interpret public templates. Runtime adapters own native
  configuration; initialization must not consume a harness's private history,
  model credentials or native tool protocol.

New hosted harnesses reuse these helpers and paths; new Providers deploy the same
Runtime contract. Neither addition should change template validation, storage or
resolution. Extend this contract only for an accepted initialization requirement.
The trusted `/usr/local/bin/agents-api-runtime-initialize` receives a bounded
version-1 JSON operation on stdin. It configures read-only tool env under
`/environment/initialization`, installs packages under `/environment/packages`,
and runs ordered commands through distro bubblewrap. The fixed mount/process map
excludes daemon credentials, native history and staging. User values are applied
inside isolation, never to the launcher. Receipt and process exit must both confirm
completion; child output is discarded because it can contain secrets.

Runtime receives a `tool_environment` execution flag, without template identity or
provider information. Adapters validate the common files and apply them in their
native tool sandbox: Claude uses its native Bash hook, Codex its managed Bash hook,
and MiniMax its isolated native-tool worker. Native transports remain unchanged.
Files reads do not require initialized tool configuration. Docker setup requires
the existing nested-sandbox deployment profile for every harness; E2B supplies
the same Runtime layout and kernel isolation.

Codex 0.153.4 can execute an original command when a native hook process fails.
The adapter verifies the required trusted managed hook before preparation and
stops the Turn on an observed failed hook. Earlier command effects may already
exist; this is not an atomic hook-failure prevention guarantee.

## Explicit gaps and evidence boundaries

System packages, nonempty `capability_directories`, `skills` and `plugins`,
plus restricted-domain network policy, remain unsupported
for both templates and inline initialization. The separate live Files API remains
available after initialization. Unsupported requests reject without echoing payloads.

The [hosted guide](https://developers.openai.com/api/docs/guides/agents-api/environments/openai-hosted)
clarifies that configured env values are readable by Agent code, files/packages
precede setup commands, nonzero setup prevents start, and runtime-reserved env names
must reject. The shared initialization batch implements those fields with encrypted snapshots
and the existing readiness gate. Public reads show packages but omit env/commands.
Template updates replace each supplied field; omission preserves it and null clears
it. Referenced Sessions inherit the snapshot; explicit env/packages/setup overrides
with a template ID reject while override semantics remain unconfirmed.

Files are installed first, followed by npm/Python packages and ordered commands;
the default cwd is `/workspace`. One command or package operation has the existing
two-minute local budget, within the thirty-minute initialization budget. No command
is retried after unknown effects. Completed setup never runs on reconnect.
Package dependencies are available to native tools across working directories.
System-level packages remain a separate privilege-boundary gap.

The [update Reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents/subresources/environments/subresources/templates/methods/update)
defines runtime network as post-setup and packages as preceding that policy.
Initialization therefore uses its isolated provisioning network; native tools
apply the requested enabled/disabled policy afterward. Allowing setup internet is
an implementation inference from that phase boundary, not an explicit upstream
guarantee. Env values are intentionally readable by Agent code; they must not
appear automatically in public metadata or initialization diagnostics.

The [current Template reference](https://developers.openai.com/api/reference/python/resources/beta/subresources/agents/subresources/environments/subresources/templates)
mentions different GA/beta defaults; this service retains `agents=v1` and the
[fixed baseline](upstream.json), whose omitted network is enabled. Exact upstream
errors, no-op timestamps, concurrent pagination and referenced Session null-network
override semantics remain unverified. The last case explicitly rejects in this
batch rather than guessing inheritance. This batch is not full protocol compatibility.

## Verification

`official_environment_templates.py` checks all five fixed-SDK operations plus raw
HTTP, exact safe response shapes, field replacement/defaults, pagination, tenant
isolation and rejected confidential canaries. `official_e2b_v1.py` opts in with
private `verify_environment_templates: true`; it creates its actual native/model
Sessions from public templates, verifies frozen snapshots and creation retries
after update/delete, then reuses the existing execution, Files/Artifacts, isolation,
cancellation and crash/history-recovery assertions. Its disabled-network Session
inherits that policy from another template. Runtime and provider packaging were
unchanged in the original metadata-only batch. Database integration tests cover persistence and concurrent field updates;
API tests cover parsing and caller-intent distinctions.

`official_environment_initial_files.py` and the `verify_initial_files: true` option
together with `verify_environment_templates: true` in the real E2B runner add
both-source/template/inline metadata, source-deletion,
foreign-tenant and actual first-native-read checks. Existing Files/Artifacts,
cancel/crash/history checks then verify that initialization did not change the
execution loop or overwrite later user modifications. Controlled PostgreSQL lifecycle
tests separately exercise interrupted installation, readiness and maintenance fairness.
A test's presence is not a passing acceptance result; retain actual run evidence.

### Accepted initial-file profiles (2026-09-20)

The batch passed fixed SDK 3.13.0/raw HTTP acceptance with real models on Docker
and E2B for Codex, Claude Code and MiniMax Code. Both template and inline paths
verified initial native reads, Files/Artifacts, tenant and credential isolation,
source/template deletion followed by creation retry, cancellation, and preserved
workspace changes/native history after Core and Runtime restarts. E2B also verified
Core interruption during initialization: no native execution, no replay and owned
resource reclamation. All six completed runs reported clean resource cleanup.

Separate real Provider checks covered Docker binary stdin/backpressure and E2B
50 MiB stdin. The real shared installer verified empty, binary, nested and 50 MiB
files, rejected symlink destinations, and preserved outside bytes. PostgreSQL/race
suites and `make check` passed. The optional native build probe skipped by the
default gate is not counted as real acceptance. Runtime images were the retained
qualified builds; Core was built from this batch. E2B runs preceded the final
readiness guard and store-interface cleanup, which received targeted regression;
The six-profile matrix preceded final creation-intent size and canonical-identity
corrections. Real HTTP/PostgreSQL regression accepted a 1 MiB file and two 5 MiB
inline files with retries, and verified canonical template/file encryption bindings.
A further rebuilt standalone Docker/Codex run passed a 5 MiB initial file with
real model reads, Artifacts, cancellation and retained history in 101.23 seconds.
The original Docker matrix used Core SHA-256
`31973b17dd96106743e581c400555e3a4b036ad8cb3e68b51530a2b56023abe3`.

Docker MiniMax Code passed with the real MiniMax API at its standard HTTPS origin
through the test network relay. Earlier Kimi/MiniMax connection timeouts remain
recorded with unknown cause, as does a Docker reconnect failure under a different
Core/Runtime restart order. They are not claimed as fixed. Sanitized run results,
checks, build hashes and failed attempts are retained under the private
`environment-template-files` acceptance directory and the linked task record.

### Current setup batch

`official_environment_setup.py` adds fixed-client/raw-response assertions for
confidential snapshots, safe package metadata, real registry dependencies, ordered
setup, native visibility across cwd and the post-setup network boundary. Runtime
mechanism tests cover private files/processes, immutable configuration, child
cleanup and failure receipts. The batch passed real-model template and inline acceptance on newly built Docker
Runtimes for Codex, Claude Code and MiniMax Code, plus Codex on a newly built E2B
template. Each verified actual npm/PyPI installs, ordered setup, native env and
dependency visibility across working directories, Files/Artifacts, cancellation,
post-setup disabled networking and recovery without repeating initialization. E2B
also verified daemon/history/process/envd isolation and separate Core/Runtime
crashes with exact native history and no automatic input replay.

The shared initializer additionally passed actual Docker isolation probes for all
three profiles and E2B registry installation. Docker nonzero setup and missing cwd
failed before native Turns and reclaimed the Environment. PostgreSQL/race checks
cover encrypted owner/field-bound snapshots, readiness and uncertain-install cleanup.
A rebuilt standalone Core passed real fixed-SDK/raw-HTTP retries with changed, added
and removed inline env/setup under a saved Agent; unchanged retries still recover
after Agent deletion. Only this creation-identity regression required the final
Core rebuild; the completed model matrix preceded that isolated hash correction.

One MiniMax inline post-restart model request reported an upstream timeout after
100 seconds. The affected inline rerun passed in 214.75 seconds, with no production
transport changes; this does not establish or fix the timeout cause. All completed
runs confirmed owned resource cleanup. The three-harness-by-two-Provider matrix
was not repeated: shared E2B initialization and the changed native adapter paths
were covered separately. System packages and unconfirmed reference overrides
remain gaps, and native Codex hook failure retains the limitation stated above.
Private sanitized run/check/build evidence is retained under
`~/.parsar/remediation/20260920/environment-template-setup/` and the linked board.
These results do not establish complete Template or Agents API compatibility.
