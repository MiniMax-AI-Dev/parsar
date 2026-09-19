# Claude Code on the dedicated Runtime

Core is independently deployed. Each managed Session receives one Docker Runtime
containing the daemon, pinned Claude Agent SDK/native harness and local workspace.
The SDK owns the model/tool loop. Public clients use the same Agents API contract.

## Build and configure

On Linux amd64, build the existing shared workspace helpers, then run:

```sh
bash scripts/build-claude-sdk-runtime.sh
bash scripts/build-claude-runtime.sh
docker build --platform linux/amd64 -t agents-runtime:claude \
  "${PARSAR_HOME:-$HOME/.parsar}/build/claude-runtime"
```

The bundle pins SDK `0.3.269` and native Claude Code `2.1.269`. The Dockerfile pins
Node's Linux amd64 manifest. Keep the exported SDK bundle immutable. Configure
Core's existing managed Runtime file with the resulting immutable image ID, a
private Docker network, the existing `deploy/codex/seccomp.json`, and
`"nested_sandbox": true`. This option enables child reaping and the outer procfs
layout required by native bubblewrap; all native sandbox restrictions remain on.
It is disabled for existing deployments unless explicitly selected.

Set `AGENTS_API_ENGINE=claude_sdk`. In a private file selected by
`AGENTS_API_EXECUTION_OPTIONS_FILE`, configure the provider:

```json
{
  "claude_provider": {
    "base_url": "https://api.anthropic.com",
    "bearer_token": "REPLACE_WITH_OPERATOR_SECRET"
  }
}
```

Use the endpoint and model supported by your actual provider. Do not bake keys
into the image. Core forwards this adapter-owned option transiently; it is not a
public Agent field. Workspace tool processes cannot inherit the provider secret.
Follow the existing Core setup for independent PostgreSQL credentials, migrations,
API authentication and managed Runtime enrollment. Parsar is not a dependency.

The supported hosted profile accepts text execution with medium verbosity and
native Bash/Read/Edit. Files and Artifacts use the shared public interfaces.
Workspace functions/MCP, multi-agent, structured output, user-managed enrollment
and other environment installations remain explicit gaps. The existing
`environment:none` function/MCP profile is separate. This is not complete upstream
protocol compatibility.

## Adding another native engine

Start with the existing `agent.Factory`, `agent.Session` and
`agent.PreparationFactory` interfaces; do not create another scheduler or loop.
Registration itself is small:

```go
registry.RegisterKind(descriptor, adapter.NewFactory(config))
registry.RegisterPreparation(descriptor.Kind, true, adapter.NewPreparationFactory(config))
```

These calls refer to the existing daemon registry. `descriptor` must advertise
only capabilities established by your adapter and deployment tests. A dedicated
local Runtime automatically reuses workspace authorization, idle directory reads,
file writes and output export; it does not need an engine-specific Files API.

The adapter translates native configuration and events, owns its child processes,
confirms input receipts, and implements cancellation and preparation ownership.
`Prepared.Start` transfers ownership once; `Close` releases only unused preparation;
`PreparedCancellation.Cancel` follows the same resource through transfer and waits
for output settlement. Reuse the shared process runner and existing error types.
Native history belongs to the bound API Session, not the device descriptor.

Add the qualified placement and value constraints to the small execution
`acceptedEngine` declaration. That declaration gates public support independently
of Runtime capability flags. Do not add engine branches to handlers, stores,
resource managers or scheduling. A genuinely new public capability may require a
bounded extension of the shared contract; registration alone cannot make an
incompatible harness conformant.

Acceptance must use the pinned official OpenAI client, raw HTTP and a real model:
execute and continue; list/create files and retrieve artifacts; cancel and prove
process settlement; reconnect/restart without replay; verify tenant, credential and
history isolation. Missing history fails closed. Run focused race tests, `make
check` and independent review before merging. Keep unsupported combinations explicit.
