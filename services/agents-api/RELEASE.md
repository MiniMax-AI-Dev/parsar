# Agents API distribution

This Linux amd64 package contains the independent API, embedded migrator and two
operator commands. It needs PostgreSQL. The Docker variant also includes the
qualified colocated Runtime image, its seccomp policy and `HOSTED.md`; use that
guide after the common API setup below. The basic archive needs separately
installed execution software.
It does not need a source checkout, Go, Node, the Parsar product or its database.
The [coverage ledger](https://github.com/MiniMax-AI-Dev/parsar/blob/@SOURCE_REVISION@/contracts/agents-api/README.md)
describes supported workflows and remaining protocol gaps. Packaging does not
establish complete OpenAI Agents API compatibility.

## Verify and extract

Verify the archive checksum supplied alongside the package, then extract into a
new directory under `~/.parsar/`. Keep deployment configuration outside the extracted
package so replacing binaries does not replace credentials or state.

```sh
sha256sum -c @ARCHIVE_NAME@.tar.gz.sha256
mkdir -p "$HOME/.parsar/releases"
tar -xzf @ARCHIVE_NAME@.tar.gz -C "$HOME/.parsar/releases"
cd "$HOME/.parsar/releases/@ARCHIVE_NAME@"
sha256sum -c SHA256SUMS
export AGENTS_API_BIN_DIR="$PWD/bin"
```

`manifest.json` records the source revision/tree, target platform, fixed upstream
protocol and binary hashes. The package also includes its license. Checksums
detect changed bytes; obtain the archive and checksum from a trusted distributor.

## Start a new API installation

Provision a dedicated PostgreSQL database and account. Use neither the product
database nor its migrations. The following local example assumes an unused port
8091. For remote clients, place the API behind TLS and set both advertised URLs to
the corresponding reachable service addresses.

```sh
umask 077
export PARSAR_HOME="$HOME/.parsar/agents-api-deployment"
mkdir -p "$PARSAR_HOME"
export AGENTS_API_DATABASE_URL='postgres://<account>:<password>@<host>/<execution-db>'
export AGENTS_API_KEYS_FILE="$PARSAR_HOME/keys.json"
export AGENTS_API_ADDR=127.0.0.1:8091
export AGENTS_API_ENGINE=codex
```

Create `keys.json` with explicit trusted identities. Generate a random caller key,
save it separately in a private `caller.key` file, and put only its SHA-256 digest
in the API configuration. A tenant is a canonical nonzero UUID; organization,
project and subject IDs must remain stable across key rotation.

```json
[{
  "tenant_id": "<tenant UUID>",
  "organization_id": "<organization ID>",
  "project_id": "<project ID>",
  "subject_kind": "service_account",
  "subject_id": "<subject ID>",
  "token_sha256": "<caller key SHA-256 hex digest>"
}]
```

For the Docker variant, continue in `HOSTED.md` now to configure the Runtime's
outward connection and provider before starting Core. For the basic archive,
set the separately installed software's reachable endpoints:

```sh
export AGENTS_API_DAEMON_WS_URL=ws://127.0.0.1:8091/api/v1/agent-daemon/ws
export AGENTS_API_EXECUTOR_URL=http://127.0.0.1:8091
```

Keep the configuration and key files mode 0600. Run migrations explicitly, then
start the API in the foreground or through your existing service supervisor:

```sh
"$AGENTS_API_BIN_DIR/agents-api-migrate"
"$AGENTS_API_BIN_DIR/agents-api"
```

`GET /healthz` provides liveness. Successful startup establishes the immutable
project mappings required by the operator commands. One API execution worker owns
each database; starting replicas does not provide execution HA. Native history
belongs to the harness host and must survive API replacement.

## Connect execution software

Keep the API running. In a separate operator shell with the same private database
configuration, provision a new daemon profile and an executor principal key using
the IDs from `keys.json`. `KEY_ID` is a new canonical nonzero UUID retained for
future rotation/revocation. The executor key can be issued before any Session.

```sh
umask 077
mkdir -p "$PARSAR_HOME/parsar-daemon/agents-api"
"$AGENTS_API_BIN_DIR/agents-api-device" \
  --tenant "$TENANT_ID" --name 'Agents API harness' \
  --url http://127.0.0.1:8091 \
  > "$PARSAR_HOME/parsar-daemon/agents-api/auth.json"
"$AGENTS_API_BIN_DIR/agents-api-environment-key" \
  --tenant "$TENANT_ID" --organization "$ORGANIZATION_ID" \
  --project "$PROJECT_ID" --subject-kind service_account \
  --subject-id "$SUBJECT_ID" --key-id "$KEY_ID" \
  > "$PARSAR_HOME/executor-key.json"
```

Use new private files; do not overwrite an existing device profile or key output.
Install the daemon, matching native Codex 0.153.4 resources and
[native executor launcher](https://github.com/MiniMax-AI-Dev/parsar/blob/@SOURCE_REVISION@/packages/codex-executor/README.md)
separately. In the daemon's own service environment, configure native model access
and run `parsar-daemon connect --profile agents-api` with the provisioned profile.
Transfer the profile securely if the harness runs on another host. The harness
must not inherit the operator database or caller credentials. Provider credentials
belong in its private native configuration, outside caller executor compute.

## Use the public client

Install the official Python client at the commit in `manifest.json` (SDK 3.13.0).
In a client process, set `OPENAI_BASE_URL=http://127.0.0.1:8091/v1` and supply the
private caller key as `OPENAI_API_KEY`. Create an empty self-hosted Session:

```python
from openai import OpenAI

client = OpenAI()
session = client.beta.agents.sessions.create(
    agent={"model": "<model configured on the harness>"},
    environment={"type": "self_hosted", "workspace_directory": "/workspace"},
)
print(session.id, session.environment.id, session.environment.remote_url)
```

On caller-controlled executor compute, prepare `/workspace` and connect the
separately installed launcher using the returned target. Transfer only its scoped
`executor-key.json`; do not transfer caller, database, daemon or model credentials.

```sh
agents-api-codex-executor --remote "$REMOTE_URL" \
  --environment-id "$ENVIRONMENT_ID" --credentials "$HOME/.parsar/executor-key.json" \
  --codex-bin /opt/codex/bin/codex
```

A directory or key binding does not isolate files or same-user processes. Use an
appropriate separate runtime when isolation is required. HTTP is accepted only
for loopback development; a remote executor needs the reachable HTTPS target.

In the same Python client, stream a Turn after connecting the executor:

```python
with client.beta.agents.sessions.stream(
    session.id, input="Run a command in the workspace and explain its result."
) as stream:
    for event in stream:
        print(event.type)
```

Keep the Session ID for later Turns. After an API restart, reconnect the client
and recover through Session, Turn and Items queries; SSE does not replay history.
Reuse the database, caller identities, daemon profile and native history. Do not
resubmit uncertain execution as new work. Graceful shutdown or connection closure
does not by itself prove all native descendants have exited.

For key rotation, device revocation, existing-database upgrades and other supported
profiles, use the [versioned service guide](https://github.com/MiniMax-AI-Dev/parsar/blob/@SOURCE_REVISION@/services/agents-api/README.md).
This package does not install PostgreSQL, daemons, harnesses, TLS or a supervisor,
and it does not switch Parsar's product execution path.
