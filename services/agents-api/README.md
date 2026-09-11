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
pagination cursors. Metadata is limited to 16 KiB of JSON string pairs and should
contain references or labels, not credentials or Agent configuration.

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
