# Vault credential storage

The standalone service supports static-bearer Credential creation and safe metadata
retrieval through the pinned official SDK. It stores tokens as authenticated
ciphertext in its own PostgreSQL database. There is no product-service dependency,
public secret-read endpoint or contact with the configured MCP destination.

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
metadata reads continue working, but Credential creation returns local
`503 credential_storage_unavailable` before writing.

Losing or replacing the key prevents decryption of existing credentials. Metadata
reads do not decrypt tokens and therefore do not prove that a key can recover them.
This release supports one retained key; rotation and re-encryption are not
implemented. Go's random-nonce GCM requires no more than 2^32 encryptions per key;
stop new credential writes before that bound until a supported rotation process
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
```

Both operations use ordinary project authentication and `OpenAI-Beta: agents=v1`.
Users and service accounts in the same project share access; a foreign project or
wrong owning Vault cannot retrieve the Credential. Parsar approval and personal
credential policies belong in the product client.

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

Credential update/list/delete, OAuth refresh, restricted-key scopes, Session
`vault_ids`, MCP `credential_id` use, secret forwarding, execution and revocation
semantics remain separate gaps. The foreign key preserves Vault ownership and
defines dependent-row removal for a future Vault deletion operation; no public
Vault deletion is added here. The full protocol target is unchanged.
