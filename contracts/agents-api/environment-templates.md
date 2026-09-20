# Environment Templates and initial files

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
  supported as described below; other populated installations reject explicitly.
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
    name="Restricted outbound access", network={"access": "disabled"}
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

## Explicit gaps and evidence boundaries

Nonempty `env`, `setup_commands`, `packages`, `capability_directories`,
`skills` and `plugins`, plus restricted-domain network policy, remain unsupported
for both templates and inline initialization. The separate live Files API remains
available after initialization. Unsupported requests reject without echoing payloads.

The [hosted guide](https://developers.openai.com/api/docs/guides/agents-api/environments/openai-hosted)
clarifies that configured env values are readable by Agent code, files/packages
precede setup commands, nonzero setup prevents start, and runtime-reserved env names
must reject. Implementing those populated fields requires a separate batch with
common initialization, safe snapshots and failure/readiness acceptance.

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
inherits that policy from another template. Runtime and provider packaging are
unchanged. Database integration tests cover persistence and concurrent field updates;
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
