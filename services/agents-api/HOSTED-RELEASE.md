# Docker-hosted Agents API

This package runs Core on a Linux amd64 host and creates one colocated
Runtime per Session through Docker. Each Runtime contains the daemon, stock
Codex 0.153.4, local tools and workspace. Core, its PostgreSQL database and
operator secrets stay outside the Runtime. No source checkout, compiler,
Parsar service, product database, separate executor or manual daemon enrollment
is needed. Complete the common database, caller identity and private directory
setup in `README.md` first; do not start Core until the configuration below is ready.

## Load the qualified Runtime

The supported deployment profile was qualified on Docker 29.1.3 with local Linux
volumes and unprivileged user namespaces. The Docker engine must support volume
subpath mounts. Other hosts and security policies need their own isolation checks.
The Provider uses a non-root container, read-only root, dropped capabilities,
no-new-privileges, private PID namespace and bounded resources. Stock Codex uses
its inner bubblewrap sandbox with the bundled seccomp policy and container-specific
`apparmor=unconfined`; host-wide policy must remain unchanged. Docker availability
alone does not establish that this native sandbox can run safely.

From the extracted package directory, after checking `SHA256SUMS`:

```sh
export AGENTS_API_PACKAGE="$PWD"
docker image load --input "$AGENTS_API_PACKAGE/runtime/image.tar"
docker image inspect @RUNTIME_IMAGE@ --format '{{.Id}} {{.Os}}/{{.Architecture}}'
```

The result must be `@RUNTIME_IMAGE@ linux/amd64`. The image is selected by this
immutable ID; no registry pull or mutable tag is required. Package checksums
establish transferred bytes, not trust in their distributor or safety of another
image. Keep the package's `runtime/seccomp.json` available to Core.

## Configure Core

Run Core as an operator account with access to the explicit local Docker Unix
socket. Docker access is privileged host authority; keep Core and this socket
outside agent workspaces. The Provider never mounts it in a Runtime. Retain the
same Docker backend, provider UUID, database, caller identities and native volumes
across Core upgrades. Changing a backend needs a new provider UUID; keep the old
entry until its allocations are reclaimed.

For a local deployment, the following uses the Docker bridge gateway so sandbox
connections can reach Core. Reserve port 8091 and restrict it to intended clients
and Runtime containers with the host firewall. For external access, terminate TLS
with your existing proxy and use its reachable HTTPS/WSS addresses instead.

```sh
export AGENTS_API_ADDR=0.0.0.0:8091
RUNTIME_GATEWAY="$(docker network inspect bridge --format '{{(index .IPAM.Config 0).Gateway}}')"
export AGENTS_API_DAEMON_WS_URL="ws://$RUNTIME_GATEWAY:8091/api/v1/agent-daemon/ws"
export AGENTS_API_MANAGED_RUNTIMES_FILE="$PARSAR_HOME/managed-runtimes.json"
export AGENTS_API_EXECUTION_OPTIONS_FILE="$PARSAR_HOME/execution-options.json"
```

Create the private managed configuration with a fresh stable provider UUID. Replace
both occurrences of `<provider UUID>`, the gateway and the absolute package path:

```json
{
  "core_url": "http://<Docker bridge gateway>:8091/api/v1",
  "default_provider": "<provider UUID>",
  "docker": {
    "<provider UUID>": {
      "host": "unix:///var/run/docker.sock",
      "image": "@RUNTIME_IMAGE@",
      "network": "bridge",
      "seccomp_file": "<absolute package path>/runtime/seccomp.json"
    }
  }
}
```

Create `execution-options.json` with your trusted native model configuration.
For a Responses-compatible model endpoint, the existing Codex adapter accepts:

```json
{
  "codex_provider": {
    "name": "Configured model provider",
    "base_url": "https://<model endpoint>/v1",
    "bearer_token": "<private model credential>",
    "wire_api": "responses"
  }
}
```

Keep both files mode 0600 outside the extracted package. The model endpoint must
be reachable from the Runtime; a host loopback address is not the container's
host. Core copies these options into trusted execution preparation without storing
them in public Session configuration. Never put model credentials in Agent
instructions, public requests, image layers or a workspace. Configuration changes
require a Core restart. Leave remote executor URLs unset for this hosted profile.

```sh
chmod 0600 "$AGENTS_API_KEYS_FILE" "$AGENTS_API_MANAGED_RUNTIMES_FILE" "$AGENTS_API_EXECUTION_OPTIONS_FILE"
"$AGENTS_API_BIN_DIR/agents-api-migrate"
"$AGENTS_API_BIN_DIR/agents-api"
```

Use your existing service supervisor for long-running operation. Migrations are
explicit. One Core execution worker owns each database; replicas do not provide
execution HA. `GET /healthz` checks liveness, not successful sandbox preparation.

## Execute through the public client

Use the pinned client described in `README.md`, with `OPENAI_BASE_URL` set to
`http://127.0.0.1:8091/v1` and `OPENAI_API_KEY` read from the private caller key.
The public model name must match the trusted endpoint's supported model:

```python
import base64
from openai import OpenAI

client = OpenAI()
session = client.beta.agents.sessions.create(
    agent={"model": "<configured model>"},
    environment={"type": "openai_hosted"},
    input="Create /workspace/result.txt containing a short greeting.",
)
print(session.id, session.environment.id)
```

Core provisions the Runtime automatically. Retrieve the Session and list its Turns
and Items to observe execution; wait for the Turn to complete before a file upload.
A connected Environment confirms the daemon transport, not native readiness.
Keep the Session ID for all subsequent operations:

```python
print(client.beta.agents.sessions.retrieve(session.id))
print(client.beta.agents.sessions.turns.list(session.id))
print(client.beta.agents.sessions.items.list(session.id))
print(client.beta.agents.environments.files.list(session.environment.id, path="/workspace"))
client.beta.agents.environments.files.create(
    session.environment.id,
    type="inline",
    path="/workspace/input.txt",
    data=base64.b64encode(b"Read these exact bytes.").decode(),
)
with client.beta.agents.sessions.stream(
    session.id, input="Read /workspace/input.txt with the native shell and quote it."
) as stream:
    for event in stream:
        print(event.type)
```

Cancellation of an accepted/running Turn uses the same public Session:

```python
client.beta.agents.sessions.events.create(
    session.id, events=[{"type": "agent.session.input.cancel"}]
)
```

Query until the Turn is terminal; a cancellation request alone is not completion.
Pre-Turn reservation cancellation remains a recorded gap. Reconnect clients after
Core restart and recover through Session/Turn/Items; SSE is live and does not replay
history. Preserve Runtime containers and both owned volumes for native history and
workspace continuation. Do not turn an uncertain interrupted execution into a new
request or delete native history to make a retry succeed.

When finished, `client.beta.agents.sessions.delete(session.id)` requests owned
Runtime cleanup. Core must remain running with the original Provider configured
until its labelled container and volumes are gone. Public deletion acknowledgment
is not physical cleanup confirmation. Do not use broad Docker pruning. An empty
`default_provider` disables new hosted admission while keeping existing Session
controls and cleanup available. Back up the independent PostgreSQL database
(including large objects) and retained Runtime state together under an operator
recovery plan; the archive itself contains no deployment data.

## Acceptance limits

The qualified basic profile covers public creation, native execution, inline and
source-file copies/listing, cancellation, retained-history restart and owned
cleanup. Network access defaults to enabled; explicit disabled confines native
tools while the trusted harness retains model/Core connectivity. Restricted domains,
populated startup installations/templates, hosted MCP combinations, Artifacts and
complete protocol parity remain open. Files size and directory bounds are local
implementation limits, not verified upstream limits.

User-managed deployment will reuse this Runtime, but installation/enrollment and
its official protocol mapping are separate work. It is not automatically official
`self_hosted`. This package does not install Docker/PostgreSQL/TLS/a supervisor,
publish an image, migrate product execution, add engines or introduce another
execution topology. See the [versioned coverage ledger](https://github.com/MiniMax-AI-Dev/parsar/blob/@SOURCE_REVISION@/contracts/agents-api/README.md).
