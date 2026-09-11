# Agents API contract

The external reference is [openai-python beta/agents](https://github.com/openai/openai-python/tree/d7c41efee1b0802b79f3f88a678ef2052b06e9ce/src/openai/resources/beta/agents),
pinned in `upstream.json`. Its resource methods, corresponding types, pagination
and streaming helpers define the compatibility target. This directory records
the boundary; it does not imply that every upstream feature is implemented.

Parsar owns product Agents and Teams. This service owns execution configuration
snapshots and single-Agent sessions/turns. The OpenAI Agents Python **SDK** is a
separate future dependency for Team orchestration in Parsar, not the HTTP contract.

## Public semantics

- Use `/agents/sessions` beneath the configured API base URL, bearer authentication
  and `OpenAI-Beta: agents=v1`. Do not introduce a competing `/sessions` surface.
- Session creation takes an environment and inline agent configuration or a saved
  agent reference. A model name is not a daemon engine name.
- `AgentSession` includes the effective agent/environment, Unix-second timestamps,
  `object: agent.session`, metadata, required actions, status, usage and vault IDs.
  A Session remains reusable after its current Turn completes.
- Input, cancellation and function results are submitted through session events.
  Turns are queried through `/agents/sessions/{id}/turns`; do not invent turn-create
  endpoints. Event submissions support the `Idempotency-Key` header.
- List operations use the upstream `after`, `limit`, `order` and resource-specific
  filters. Stream events preserve the upstream discriminators and payload shapes.
- The upstream self-hosted environment includes an exec-server `remote_url`.
  A Parsar daemon socket is not automatically compatible with that transport.
  Provider adaptation must be explicit and verified before advertising support.

## Delivery and verification

| Capability | Current state |
| --- | --- |
| Shared daemon connection layer | Implemented; existing product protocol retained |
| Tenant-scoped Session persistence | Implemented; internal Store, not a public API |
| Effective Session configuration persistence | Implemented; immutable JSON snapshot and retry identity |
| Authenticated Session HTTP API | Pending |
| Turn execution, events, results and cancellation | Pending |
| Pending actions and environment lifecycle | Pending |
| Official-client compatibility and product cutover | Pending |
| Team orchestration | Deferred; Parsar-owned |

The Store's internal DTO is not the upstream response model. The API layer must
validate and resolve the upstream schema before persistence, and report only
supported options. For example, upstream metadata is limited to 16 pairs with
64-character keys and 512-character values; a storage byte limit is not a
replacement for that public validation.

Use the pinned official Python client against the actual service, with response
validation enabled, for supported Session operations, pagination, streaming,
errors, idempotency and tenant isolation. A client import or permissive parsing
alone is not evidence of compatibility. Unsupported capabilities must be explicit
errors, not successful placeholder resources. Add any provider or engine-specific
extension separately from upstream fields and document it here when implemented.
