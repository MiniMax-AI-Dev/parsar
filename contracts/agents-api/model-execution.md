# Session model execution extension

Core accepts optional top-level `x_agents_core.model_provider` on Session creation.
This is a Core extension, not part of the pinned upstream protocol. It supplies
execution input only: there is no Provider CRUD, catalog, model alias resolution or
product permission model in Core.

```json
{
  "agent": {"model": "exact-provider-model", "x_agents_core": {"harness": "mcode"}},
  "environment": {"type": "openai_hosted"},
  "x_agents_core": {
    "model_provider": {
      "protocol": "anthropic",
      "base_url": "https://provider.example/anthropic",
      "api_key": "<private key>",
      "context_window": 200000,
      "max_output_tokens": 8000
    }
  }
}
```

`protocol` is `anthropic` for Claude Code/MiniMax Code or `responses` for Codex.
The endpoint must use HTTPS without embedded credentials, a query or a fragment.
Keys must be nonempty, at most 16 KiB, and contain no NUL/CR/LF. Unknown fields and
unsupported protocol/Harness/environment combinations are rejected before creating
a Session. Context/output limits are optional nonnegative integers, with output no
larger than context; both must be positive for MiniMax Code. Use the actual model's
limits. Native provider availability is checked during execution, not by a new probe.
`agent.model` retains its exact meaning; this extension never changes model identity.

The entire supplied configuration is frozen and encrypted in the Session creation
transaction, with a distinct credential-crypto purpose and tenant/Session binding.
Creation retries include this intent in their request hash; changing the key or
endpoint under the same idempotency key conflicts. Recovery reads the committed
Session before mutable Agent/template resolution. No public Session, Agent,
Environment, event or ordinary configuration contains the key. The top-level
extension is write-only and has no update endpoint.

At dispatch, Core resolves its encrypted snapshot into the existing native adapter
options. It does not fall back to operator credentials when a snapshot is missing
or cannot decrypt. Omission preserves the existing operator-options behavior.
Core needs its configured credential encryption key to accept and resume these
Sessions; retaining the same key is required across restarts. Native harness homes
may contain private provider configuration under the existing qualified hosted
isolation rules; tools and public Files must not access those homes. This extension
does not qualify a new runtime placement or self-hosted credential path.

Parsar manages its own workspace catalog and encrypted keys, sends this extension
only on the first Core Session request, and retains a private encrypted snapshot
for uncertain creation retries. Catalog updates and deletion affect new Sessions;
existing Sessions retain their original model, endpoint and key.
