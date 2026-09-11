# Agents API

Execution service under construction. This first slice provides durable Session
storage and an independent migration command. It does not expose HTTP endpoints,
run Agents, or change Parsar's production dispatch.

A Session is an execution context with a stable engine choice. Product
conversations can map to multiple Sessions. Native engine IDs, device bindings,
runs and pending interactions will be added with their execution flows.

## Database ownership

Use a dedicated PostgreSQL database and account, separate from the Parsar product.
This service does not import `server/internal` or apply product migrations.
Migrations are embedded and tracked in `agents_api_schema_version`.

```bash
AGENTS_API_DATABASE_URL='postgres://.../agents_api' \
  go run ./services/agents-api/cmd/migrate
make sqlc-generate
```

The Store requires a tenant on every operation. The future API authentication
layer must derive that tenant from the caller's credential, never trust a tenant
claimed in a request body. Store methods alone do not authenticate callers.

Session creation requires an idempotency key scoped to the tenant. Repeating the
same engine and metadata returns the existing Session; different input with the
same key returns a conflict. Read/list operations are tenant-scoped, including
pagination cursors. Metadata is limited to 64 KiB of JSON string pairs and should
contain references or labels, not credentials or Agent configuration.

The separate `configuration` field preserves the resolved non-secret Agent and
environment snapshot as a JSON object, up to 512 KiB. Creation retries include
that snapshot in their identity; changed configuration conflicts rather than
mutating an existing Session. Legacy Sessions without configuration keep their
original retry identity. JSON key order and whitespace do not affect matching.
The API layer validates the supported upstream schema before calling the Store;
the Store does not invent defaults or claim a daemon environment is connected.
See [the compatibility boundary](../../contracts/agents-api/README.md).

## Standalone HTTP service

Run migrations first, then `go run ./services/agents-api/cmd/server`. The service
requires `AGENTS_API_DATABASE_URL` and `AGENTS_API_KEYS_FILE`; it does not read the
product database or accept product login cookies. The key file is a JSON array of
`{"tenant_id":"<nonzero UUID>","token_sha256":"<SHA-256 hex digest>"}` bindings.
Provision a random bearer key per execution tenant and give clients the plaintext
key securely; keep only its digest in the server file. Rotate by replacing the
bindings and restarting. These service identities do not grant product-user rights.

`AGENTS_API_ADDR` defaults to `127.0.0.1:8091`; use a TLS reverse proxy for remote
access. `AGENTS_API_ENGINE` defaults to `codex` and is stored independently from
the client's requested model. This slice stores configuration; it does not run
that engine or connect an environment yet.

The SDK base URL is `http://127.0.0.1:8091/v1`. Supported operations are Session
create, retrieve and list, with `OpenAI-Beta: agents=v1` (set by the official SDK).
Creation supports inline `agent.model`, optional `agent.instructions`,
`environment: {"type":"none"}`, and metadata. Metadata allows at most 16 pairs,
64-character keys and 512-character values, including Unicode. Requests have a
1 MiB body limit and resolved configuration retains the 512 KiB Store limit.
`Idempotency-Key` makes creation retries safe; omission creates a new Session.
Inline Agent IDs identify the Session's immutable execution configuration, not a
reusable Parsar Agent. List supports `after`, `limit` (1–100) and `order` (asc/desc).

Unsupported fields, saved Agent references, vaults, initial input and streaming
return explicit errors. Session update/delete, event submission and other
resources remain unsupported. `/healthz` reports process liveness only.

After preparing a dedicated test database, build the server and verify it with
the official client installed from the commit in `contracts/agents-api/upstream.json`:

```bash
go build -o /tmp/agents-api ./services/agents-api/cmd/server
AGENTS_API_SERVER_BIN=/tmp/agents-api python services/agents-api/tests/official_client.py
```

The test uses `PARSAR_AGENTS_API_TEST_DATABASE_URL`, temporary service keys and
fresh tenant IDs. It checks strict response schemas, retries, ordering, tenant
isolation, unsupported options and reads after a process restart.

## Checks

```bash
PARSAR_AGENTS_API_TEST_DATABASE_URL='postgres://.../parsar_agents_api_local_tests' \
  make check-agents-api
```

The test database must be named `parsar_agents_api_*_tests` and contain no product
workspace tables. Tests apply only this service's migrations and use new tenant
IDs without truncating tables. Missing test configuration skips DB tests locally;
the `agents-api` CI workflow always supplies its own PostgreSQL service. Run the
full `make check` before review as well. Product OpenAPI generation excludes this
service; its future HTTP contract will have a separate generated artifact.
