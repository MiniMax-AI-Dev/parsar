# Environment Templates: basic hosted configuration

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
- Empty/null installation fields retain empty defaults. Responses contain the
  pinned metadata fields, empty arrays/objects as applicable and never `env` or
  `setup_commands`. Populated confidential/installation inputs reject before storage.
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

## Explicit gaps and evidence boundaries

Nonempty `env`, `setup_commands`, `files`, `packages`, `capability_directories`,
`skills` and `plugins`, plus restricted-domain network policy, remain unsupported
for both templates and inline initialization. Files can still be written through
the separately accepted live Files API after connection. Do not substitute that
later write for before-start template materialization. Unsupported requests reject
without echoing payloads; no secret-storage or initializer framework is introduced.

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
