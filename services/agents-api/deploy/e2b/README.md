# E2B colocated Runtime

E2B implements the existing SandboxProvider's Create, GetInfo, Renew, Kill and
initialization-only RunCommand. Each sandbox contains the same daemon, native
harness, local tools and workspace as the qualified Docker Runtime. Core remains
independent. Daily execution and public Files/Artifacts use daemon/Runtime; they
never use E2B commands or its filesystem service.

## Build and qualify

Use the qualified Linux amd64 Docker image for the selected harness. Build the
E2B template on a machine with Docker and Python 3.12+ using `requirements.txt`. The
builder extracts the existing runtime binaries and native profile; it does not
rebuild the harness or add a tool loop. Archive extraction retains read-only native
configuration; extraction errors must not silently omit the profile. Store private keys and build outputs under
`~/.parsar/` and keep them out of the checkout.

```sh
python -m venv "$HOME/.parsar/build/e2b-sdk"
"$HOME/.parsar/build/e2b-sdk/bin/pip" install -r services/agents-api/deploy/e2b/requirements.txt
"$HOME/.parsar/build/e2b-sdk/bin/python" services/agents-api/deploy/e2b/build-template.py \
  --image sha256:QUALIFIED_RUNTIME_IMAGE_DIGEST \
  --name your-runtime-build \
  --api-key-file "$HOME/.parsar/secrets/e2b.key" \
  --output "$HOME/.parsar/build/e2b-template.json"
```

The output's `template` is the official immutable `templateID:build_UUID`
reference. Qualify and deploy that exact reference, never a mutable alias or a
fallback template. Python and the E2B SDK are build/acceptance tools only; the
production Core uses Go and authenticated official REST/Connect transports.

## Operator configuration

Set `AGENTS_API_MANAGED_RUNTIMES_FILE` to a private JSON file:

```json
{
  "core_url": "https://core.example.com/api/v1",
  "default_provider": "7d7527e1-c198-4d6a-a807-c4b90e89acb4",
  "e2b": {
    "7d7527e1-c198-4d6a-a807-c4b90e89acb4": {
      "api_key_file": "/private/e2b.key",
      "template": "TEMPLATE_ID:BUILD_UUID",
      "lease_seconds": 7200
    }
  }
}
```

Set `AGENTS_API_DAEMON_WS_URL` to
`wss://core.example.com/api/v1/agent-daemon/ws`. Both endpoints must be reachable
from E2B. Select the existing `AGENTS_API_ENGINE` and corresponding private model
provider configuration for the qualified image. No public engine selector is
introduced. Docker and E2B entries share the provider-key namespace; retained
entries remain available for existing allocations and cleanup. Use a new provider
key when changing backend/account ownership.

The account must allow the configured lease (two hours minimum). This leaves
room for Core's existing one-hour disconnect grace. Core renews the original
running sandbox; no auto-pause, auto-resume, recreation, pool or migration is
implemented. Provider expiry destroys volatile workspace/history; Core reports
failure and must not fabricate a recovered Session or replay execution.

## Initialization and security boundary

Core persists its allocation and dedicated credential hash before Create. E2B
metadata carries only installation, tenant, Environment, allocation, Session and
device identifiers. The account key stays in Core. A private root-owned input
injects the existing daemon auth profile, binds the workspace at `/workspace`,
then launches the non-root daemon with the image's explicit native profile.
Model credentials arrive through the existing authenticated execution contract.
Neither credential belongs in template environment, metadata, command arguments,
images or logs.

E2B clears `/run` at boot and envd commands do not inherit template environment.
Initialization uses `/root/.parsar/e2b` and the root-owned image environment file.
E2B template finalization makes `/usr/local` writable and creates a passwordless
privileged `user` account. The protected `/opt/parsar-e2b/init.py` restores
root-owned executable paths (including injected envd/boot files) and locks that
unused account before starting daemon. These are required corrections to the
[provider's finalization](https://github.com/e2b-dev/runtime/blob/fad70f393e800cee0278669a63976c3aaa00871b/packages/orchestrator/pkg/template/build/phases/finalize/configure.sh),
not changes to the native harness.
Verify actual write and account-transition denial on every qualified template.
Its final atomic receipt distinguishes completed bootstrap from merely running
compute. On uncertain creation/initialization, Core observes the retained exact
allocation or reclaims it; it never retries startup or rotates its credential.
Inspection and cleanup recheck exact metadata ownership, including after restart.
A command timeout/transport failure is an unconfirmed effect, requiring cleanup
before reuse. Cancellation of a Provider request alone does not prove process exit.

Native sandboxing remains mandatory inside the VM. Qualify actual tool reads,
credential/history isolation, process namespaces, privilege denial, unauthenticated
envd denial, both network policies and exact Core binding with each real harness.
A readable **inner** PID 1 environment is not itself access to the outer daemon;
verify namespace identity and actual sensitive-value/file access. Template builds
and SDK deserialization alone do not qualify deployment.

## Acceptance scope

Use a separate execution database, the fixed official OpenAI SDK plus raw HTTP,
real E2B instances and real model APIs. Cover all five Provider operations, actual
native execution, Files upload/list, immutable Artifacts, tenant/auth isolation,
cancellation with stopped effects, daemon/Core reconnect and exact-history recovery
without automatic replay. Keep failure and cleanup evidence. Mock tests do not
substitute for these checks. This deployment does not claim full official protocol
compatibility or add user-managed enrollment, new protocol resources or HA.

`services/agents-api/tests/official_e2b_v1.py` runs this acceptance against the
packaged `bin/agents-api`, `bin/agents-api-migrate` and `upstream.json`. Use the
fixed OpenAI SDK from `contracts/agents-api/upstream.json`, plus `e2b` from this
directory's requirements. Pass a private JSON file with these operator inputs:

```json
{
  "engine": "codex",
  "model": "YOUR_REAL_MODEL",
  "proof_root": "/absolute/private/proofs",
  "package": "/absolute/agents-api-package",
  "e2b_key_file": "/absolute/private/e2b.key",
  "model_key_file": "/absolute/private/model.key",
  "database_file": "/absolute/private/dedicated-database.url",
  "options_file": "/absolute/private/execution-options.json",
  "port": 19341,
  "core_public_url": "https://acceptance-core.example.com",
  "template": "TEMPLATE_ID:BUILD_UUID",
  "native_history_root": "/home/runtime/.parsar/parsar-daemon/agent-sessions",
  "psql_command": ["psql", "--dbname=YOUR_PRIVATE_TEST_DATABASE"]
}
```

Route the public HTTPS/WebSocket endpoint to the test port. The fixture starts
and crashes its own Core and Runtime, creates billable sandboxes, and deletes
its Sessions and cloud allocations in cleanup. Use a dedicated database and
proof directory. `psql_command` must access that same database and accept `-At -c`;
do not put passwords in its arguments. Set the engine, history root and model
options for each qualified native profile. Failures retain redacted evidence;
direct provider cleanup is reported as failed Core cleanup, not acceptance.
