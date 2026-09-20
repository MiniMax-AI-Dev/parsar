# Core harness selection extension

The pinned official Agent contract has no harness selector. Core adds one optional
`x_agents_core` field to saved Agent create/update/read and Session inline/effective
Agent configuration. This is a Core extension, not an upstream field.

```json
{"x_agents_core":{"harness":"claude_sdk"}}
```

Supported identifiers are `codex`, `claude_sdk` (Claude Code), and `mcode`
(MiniMax Code). Unknown identifiers, empty objects and unknown nested fields are
rejected. Omission inherits a saved Agent value, or uses `AGENTS_API_ENGINE` for
an inline Agent. An explicit null clears the saved selection or replaces it for
one Session, restoring deployment-default selection. Agent updates preserve omitted
fields and replace the entire supplied extension. Saved resources do not start
execution and can retain protocol configuration beyond the selected engine's
current execution profile.

Session creation resolves the extension after saved-Agent overrides, validates the
selected execution profile, and persists the resulting existing `Session.Engine`.
An explicitly selected harness must be enabled by the deployment; it never falls
back to a different engine. Effective Session reads report the persisted engine,
including for historical Sessions without the extension. They never consult current
Agent defaults or the current deployment default. Explicit-selector creation retries
retain caller intent before mutable configuration resolution. Changing a selector
under an existing creation key conflicts.

The environment remains the separate official Session `environment` parameter.
Environment templates select startup configuration, not engines, containers or
providers. Multiple Sessions using the same Agent/template have separate Environment
resources and native histories. Agent edits do not change accepted Sessions.

## Operator configuration

Existing `AGENTS_API_ENGINE` and `default_provider` deployments keep their default
behavior. A deployment enabling multiple hosted harnesses adds `engine_providers`
to `AGENTS_API_MANAGED_RUNTIMES_FILE`:

```json
{
  "core_url": "http://core:8091/api/v1",
  "default_provider": "11111111-1111-4111-8111-111111111111",
  "engine_providers": {
    "codex": "11111111-1111-4111-8111-111111111111",
    "claude_sdk": "22222222-2222-4222-8222-222222222222"
  },
  "docker": {
    "11111111-1111-4111-8111-111111111111": {
      "host": "unix:///var/run/docker.sock",
      "image": "sha256:<qualified-codex-image>",
      "network": "bridge",
      "seccomp_file": "/private/seccomp.json"
    },
    "22222222-2222-4222-8222-222222222222": {
      "host": "unix:///var/run/docker.sock",
      "image": "sha256:<qualified-claude-image>",
      "network": "bridge",
      "seccomp_file": "/private/seccomp.json",
      "nested_sandbox": true
    }
  }
}
```

Each reference must name an existing qualified provider entry. The default engine
inherits `default_provider` when no explicit mapping exists. Other engines have no
fallback. Admission and initial allocation use the same engine mapping. Once an
allocation exists, its persisted provider identity remains authoritative across
restarts and configuration changes. Keep retained provider entries for cleanup;
changing a provider target requires a new key. No image is qualified merely by
being named in this map. Existing provider security and native capability checks
still apply. This change adds no provider or native harness implementation.

For multiple engines, use an exclusive `by_harness` object in the private
`AGENTS_API_EXECUTION_OPTIONS_FILE`, with one existing adapter-options object per
engine. No engine inherits another engine's credentials. A legacy flat options
object is usable only by the deployment default engine. Missing options for a
selected engine fail execution. Credentials stay in private operator files and
transient adapter requests, never Agent defaults, metadata or effective responses.

Model names remain explicit `agent.model` values. The selected native adapter uses
its configured provider and rejects unsupported model settings without changing
model identity. Core applies its existing engine/environment/tool/verbosity rules
before creating a Session; provider model availability is checked during native
startup/execution. There is no invented cross-provider model-name catalog.

Current profile limits remain in the [engine coverage table](README.md#public-engine-profiles).
