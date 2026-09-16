# Vault credential storage

The standalone service supports static-bearer Credential creation, token replacement,
deletion and safe metadata retrieval through the pinned official SDK. It stores tokens as authenticated
ciphertext in its own PostgreSQL database. There is no product-service dependency,
public secret-read endpoint. Resource creation, replacement and retrieval do not contact the
configured MCP destination; an attached Session can use it during execution.

## Configure the storage key

`AGENTS_API_CREDENTIAL_KEY_FILE` points to a file containing one base64-encoded,
random 32-byte key. Generate it once in private service configuration; the following
command refuses to replace an existing file:

```sh
(
  umask 077
  set -C
  mkdir -p "$HOME/.parsar/agents-api"
  openssl rand -base64 32 > "$HOME/.parsar/agents-api/credential.key"
)
export AGENTS_API_CREDENTIAL_KEY_FILE="$HOME/.parsar/agents-api/credential.key"
```

Keep the same key across service restarts and retain a protected backup separately
from database backups. The service reads it at startup; it never generates a
replacement or falls back to `PARSAR_MASTER_KEY`. Invalid configured files fail
startup with a safe error. If the setting is absent, other resources and Credential
metadata reads continue working, but Credential creation and replacement return local
`503 credential_storage_unavailable` before writing.
Session attachment/selection also uses safe metadata. If a selected credential
cannot be decrypted at dispatch, execution fails without contacting its MCP server
or falling back to anonymous authentication.

Losing or replacing the key prevents decryption of existing credentials. Metadata
reads do not decrypt tokens and therefore do not prove that a key can recover them.
This release supports one retained key; storage-key rotation and re-encryption are not
implemented. Go's random-nonce GCM requires no more than 2^32 encryptions per key;
stop new credential writes before that bound until a supported storage-key rotation process
is available. Never treat editing the key file as rotation.

## Public resource contract

```python
credential = client.beta.agents.vaults.credentials.create(
    vault.id,
    name="Internal MCP",
    auth={
        "type": "static_bearer",
        "mcp_server_url": "https://mcp.example.com/endpoint",
        "token": token_from_private_configuration,
    },
)
metadata = client.beta.agents.vaults.credentials.retrieve(
    credential.id, vault_id=vault.id,
)
for metadata in client.beta.agents.vaults.credentials.list(vault.id, order="asc"):
    print(metadata.id, metadata.name, metadata.auth.mcp_server_url)
```

These operations use ordinary project authentication and `OpenAI-Beta: agents=v1`.
Users and service accounts in the same project share access; a foreign project or
wrong owning Vault cannot retrieve the Credential. Parsar approval and personal
credential policies belong in the product client.

Listing supports `after`, creation order (default `desc`), a limit defaulting to
20 and clamped to 1–100, and scalar or array `status` filters (`active`/`archived`,
both by default). Credential classification is private and independent of Vault
classification. Listing requires no encryption key and reads only safe metadata;
the parent and cursor must belong to the requested project and Vault. Synthetic
archived fixtures verify filtering, not a public archive operation. Archive behavior
and exact hosted query/concurrent-page semantics remain separate gaps.

Required name is trimmed to 1–256 UTF-8 bytes. Required `auth` accepts
`static_bearer`, an HTTPS `mcp_server_url` and a string `token`. The token is
preserved as opaque data, including whitespace or an empty string. This does not
verify that it will authenticate to a destination. The local URL profile excludes
userinfo and fragments, preserves queries and performs no DNS or HTTP request.
Exact hosted empty-token and URL normalization rules remain unverified.

The response contains `id`, `vault_id`, `name`, `object: vault.credential`,
`created_at`, `updated_at` and `auth`. Static auth contains only `type` and
`mcp_server_url`. There is no token, ciphertext or key information in the response.
The existing 1 MiB body bound is a local implementation limit. Creation makes a
fresh resource; hosted retry/idempotency semantics remain unverified.

## Encryption boundary and remaining work

The implementation uses standard-library AES-256-GCM with random nonces, without
custom nonce generation or password-derived keys. Ciphertext is a format version
byte followed by the standard AEAD nonce/ciphertext/tag payload. Authenticated data
contains a fixed domain/version and the tenant, Vault, Credential, auth type and
exact destination. A wrong key, modified payload or substituted binding fails
authentication. Names are public mutable metadata and are not part of this binding.
Resource SQL reads select no secret ciphertext. The key and request token exist in
trusted service memory; this protects stored secrets, not a compromised service host.

Credential archive behavior, OAuth refresh, restricted-key scopes and revocation
semantics remain separate gaps. The foreign key preserves Vault ownership and
atomically removes all dependent Credentials when their Vault is deleted. The full
protocol target is unchanged.

## Use a credential in a Session

Attach the owning Vault and declare the same exact HTTPS destination:

```python
session = client.beta.agents.sessions.create(
    agent={
        "model": model,
        "tools": [{
            "type": "mcp",
            "server_label": "internal",
            "transport": {"type": "http", "server_url": "https://mcp.example.com/endpoint"},
            "connection_origin": "service",
            "credential_id": credential.id,
        }],
    },
    environment={"type": "none"},
    vault_ids=[vault.id],
)
```

Trusted service-side Codex and Claude SDK support `environment:none`; Codex also
supports the documented `self_hosted` combination. The daemon must advertise both
`mcp_http_tools` and `mcp_http_bearer_auth`. The usual
[MCP profile limits](README.md#http-mcp-execution) still apply. Without an explicit
`credential_id`, one exact-URL static credential among attached Vaults is selected;
zero matches remains anonymous and multiple matches fail. A foreign, missing,
unattached or wrong-destination reference returns the same local 404 before Session
creation. Saving a reference on an Agent does not authorize it for a Session.

The Session freezes its attachment list and private selection, including anonymous
decisions. Public tools retain the caller's `credential_id` value, including null.
Identical creation retries recover the accepted Session before selecting again;
adding another credential does not change an existing binding. Each dispatch
rechecks the complete scope before decryption. The token goes only through the
private daemon request and a fresh native child environment variable, never public
configuration, history, arguments or logs. Native execution requires nonempty RFC
6750 b64token bytes and rejects other opaque stored strings without trimming them.
Exact hosted matching, response population, selection timing and error/redirect
semantics remain unverified.

## Replace a stored token

Use the pinned auth-only update operation when the MCP server's bearer token changes:

```python
updated = client.beta.agents.vaults.credentials.update(
    credential.id,
    vault_id=vault.id,
    auth={"type": "static_bearer", "token": replacement_from_private_configuration},
)
```

This calls `POST /v1/vaults/{vault_id}/credentials/{credential_id}`. Both `auth` and
its `type`/string `token` are required; null, missing fields and extra mutation
fields are rejected. Empty and whitespace tokens remain opaque stored values,
subject to the existing native execution syntax limit when used. The response is
the same safe Credential metadata. ID, Vault, name, auth type, exact destination
and creation time stay unchanged; only ciphertext and update time are replaced
atomically. Missing encryption configuration or a failed mutation leaves the old
row intact. Unknown, foreign, wrong-Vault and malformed references use local 404.

Existing explicit and implicit Session bindings keep the same selected identity
and creation retry behavior. A later dispatch reads the replacement after commit;
an already-resolved or running request may still hold the previous token. Updating
this resource performs no MCP call, changes no server-side token independently,
and provides no in-flight revocation, hot reload or cancellation. Coordinate the
destination's token change operationally. Replacing this token does not rotate the
storage encryption key or reset its encryption budget. OAuth replacement and exact
hosted overlapping-update, replay and timestamp semantics remain unverified.

## Delete a stored credential

```python
deleted = client.beta.agents.vaults.credentials.delete(
    credential.id,
    vault_id=vault.id,
)
```

The response confirms the ID, `deleted: true` and `object: vault.credential.deleted`.
This operation removes the owned database row and encrypted token without loading
the storage key. Local retrieval, update and repeated deletion return 404 afterward;
listings omit the row. The parent Vault and other credentials remain available.

Existing Session snapshots and history keep their frozen credential identity. A
subsequent secret lookup fails without selecting another credential or switching
to anonymous MCP. A token already read before deletion may remain available to
dispatched work. Deletion does not stop running Sessions or revoke tokens at their
providers; use Session cancellation and provider management for those operations.

The relationship between deletion and archived status, exact hosted metadata
visibility and duplicate-deletion errors remain unverified. This implementation
does not infer an archive transition. Row removal is not evidence of physical
erasure from PostgreSQL pages, WAL, backups or native history.

## Delete a Vault and its credentials

```python
deleted = client.beta.agents.vaults.delete(vault.id)
```

The response contains `id`, `deleted: true` and `object: vault.deleted`. Deletion
removes the project-owned Vault and every stored Credential in one database
transaction, including active and archived classifications. It needs no storage
key and sends no provider requests. Other Vaults and their Credentials are unchanged.

Local retrieval, repeated deletion, child reads/updates/listing and new Session
attachments return 404 after removal. New child creation also returns 404 when
credential writes are configured; the existing missing-key 503 still takes
precedence when writes are disabled. Vault lists omit the deleted parent.
Existing Session snapshots and recorded creation retries keep the original Vault
and Credential IDs, and historical Items remain available. Later secret lookup
fails without selecting another Credential from an attached Vault or downgrading
to anonymous MCP. Already-read tokens may remain in dispatched work.

This operation follows the same provider revocation, running-Session and physical
erasure limits as single-Credential deletion. Exact hosted archive relationships,
post-delete visibility and overlapping-mutation/error semantics remain unverified.
