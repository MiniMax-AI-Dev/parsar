# Agents API

Execution service under construction. Current slices provide durable Session/Turn
storage, an authenticated Session HTTP service, an internal daemon dispatcher and
an independent migration command. Public execution is not enabled, and Parsar's
production dispatch is unchanged.

A Session is an execution context with a stable engine choice. Product
conversations can map to multiple Sessions. Device connections and bindings are
internal primitives; pending interactions and public execution remain unfinished.

## Database ownership

Use a dedicated PostgreSQL database and account, separate from the Parsar product.
This service does not import `server/internal` or apply product migrations.
Migrations are embedded and tracked in `agents_api_schema_version`.

```bash
AGENTS_API_DATABASE_URL='postgres://.../agents_api' \
  go run ./services/agents-api/cmd/migrate
make sqlc-generate
```

Session and device-management operations require a tenant. The API authentication
layer derives that tenant from the caller's credential, never a tenant
claimed in a request body. The daemon gateway separately authenticates device
credentials before accessing device liveness. Store methods alone do not
authenticate callers.

Session creation requires an idempotency key scoped to the tenant. Repeating the
same engine, metadata and configuration returns the existing Session; different input with the
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

## Internal Turn persistence

The Store accepts individual validated messages and cancellation requests.
Messages start a Turn when idle and append to the existing Turn while active,
including queued or waiting work. Session row locks and a PostgreSQL uniqueness
constraint serialize admission across processes. Input keys are scoped to the
Session across message/cancel kinds; identical retries return the original
sequence and Turn, while different input conflicts. JSON object key order and
whitespace are immaterial. Input payloads are limited to 512 KiB.

Cancellation records its original target even when there was no active Turn.
Queued work cancels immediately; a dispatcher must claim `in_progress` before
dispatch. Running/waiting work records the request and remains active until the
executor reports a terminal outcome. Completion may win a cancellation race;
once stored, terminal status, timestamps and outcome cannot be overwritten.
Outcome is a bounded adapter payload, not a second public response schema.

Tenant-scoped Turn and ordered input reads survive process restarts. The Session
configuration remains immutable and shared by its Turns. The internal dispatcher
claims a Turn before delivery, confirms additional messages through native steering,
and commits the outcome and native engine ID together. It requires an internally
resolved `daemon.work_dir` configuration and a bound device advertising streaming
and steering. Messages currently use normalized internal `{"text":"..."}` payloads;
public upstream input mapping is not implemented here.

The dispatcher takes immutable model/instructions from the Session snapshot and
resolves ephemeral engine credentials separately. Matching daemon versions release
the native writer before completing a Turn and acknowledge cancellation after the
engine returns. Subsequent Turns resume the native ID on the same device. Device
history must still exist. This is not the public self-hosted executor protocol.

Uncertain delivery, disconnected devices and unsupported interactions fail rather
than report success or automatically replay. A hard process crash can leave claimed
work in progress until future reconciliation; no background worker is wired yet.
Public event batches, durable output events, pending interactions and SSE are still
unsupported. Durable input acceptance is not an exactly-once execution guarantee.

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

## Internal execution device connection

The standalone service can accept existing daemon connections without a Parsar
workspace or product database. Enable its internal gateway by setting
`AGENTS_API_DAEMON_WS_URL=wss://your-service/api/v1/agent-daemon/ws` (use `ws`
for local development). This is separate from the official Agents API executor
contract; do not return this URL as a public `self_hosted` environment's remote URL.

After migrations, an operator can provision a device for an execution tenant:

```bash
umask 077
mkdir -p ~/.parsar/parsar-daemon/agents-api
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --name 'local executor' \
  --url 'http://127.0.0.1:8091' \
  > ~/.parsar/parsar-daemon/agents-api/auth.json
parsar-daemon connect --profile agents-api
```

The command requires `AGENTS_API_DATABASE_URL` and emits a secret profile once.
Use a new profile rather than overwriting an existing device's credentials. When
provisioning remote compute, securely transfer this file to the same profile path
on the executor. The database stores only the credential digest. API keys and
device credentials are not interchangeable. To revoke a device:

```bash
go run ./services/agents-api/cmd/device \
  --tenant '<execution-tenant-uuid>' --revoke '<device-uuid>'
```

An existing connection is retired on its next heartbeat; new connections are
rejected immediately. The internal Store binds each Session to one same-tenant
device, preserves that assignment across retries/restarts, and refuses a silent
move to another device. Revoked bindings cannot be used for dispatch. Device
connections alone do not start a Turn: public execution and provider lifecycle
remain under construction. See the [ownership rules](../../CONTRIBUTING.md#product-and-execution-service-separation).

## Official client verification

After preparing a dedicated test database, build the server and verify it with
the official client installed from the commit in `contracts/agents-api/upstream.json`:

```bash
python -m pip install -r services/agents-api/tests/requirements.txt
go build -o /tmp/agents-api ./services/agents-api/cmd/server
AGENTS_API_SERVER_BIN=/tmp/agents-api python services/agents-api/tests/official_client.py
```

The test uses `PARSAR_AGENTS_API_TEST_DATABASE_URL`, temporary service keys and
fresh tenant IDs. It checks upstream and generated response schemas, retries, ordering, tenant
isolation, unsupported options and reads after a process restart. It also runs the
[official Go client integration](../../packages/agents-client/README.md), using two
fresh tenants, and validates its created Sessions through the Python SDK.

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
service; its supported HTTP contract is generated separately.
