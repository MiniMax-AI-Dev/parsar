# MCP Connector Directory Catalog

`catalog.json` is the repository-maintained source for Parsar's built-in MCP
Connector Directory. It contains publisher-maintained Streamable HTTP endpoints
that have been exercised with Parsar's current MCP client. Entries may be
credential-free or use the standard MCP OAuth flow. Importing an item saves a
workspace capability and never executes it.

## Updating the catalog

- Add only MCP servers that can be verified in an official repository or the
  official MCP Registry.
- Keep `id` stable and unique. Renaming an item does not require changing its
  ID.
- Streamable HTTP entries use an HTTPS `url` only. Credential-free entries must
  complete MCP initialize, tools/list, and a harmless tool call without headers.
  OAuth entries must complete the full Parsar authorization and connection-test
  flow using official protected-resource and authorization-server discovery,
  dynamic client registration, and PKCE; do not add provider-specific client
  secrets.
- OAuth entries set `authentication.type` to `oauth2` and reference a built-in
  `credential_kind`. A member authorizes with their provider account, and
  Parsar stores the token as a workspace-scoped shared credential. Agents in
  that workspace use it automatically after the connector is enabled. Tokens
  are never stored in this catalog.
- Do not list unavailable or approved-client-only connectors. Add them only
  after Parsar can complete their authorization and connection-test flow.
- Do not add runtime-specific `npx` or `uvx` entries to the built-in directory.
  They may execute inside a container instead of the user's device and create a
  misleading product experience.
- Do not add MCPB/DXT desktop extensions as stdio entries. Control Chrome,
  PowerPoint (By Anthropic), Word (By Anthropic), and PDF Tools currently depend
  on Claude's desktop bundle installation lifecycle and cannot be represented by
  a truthful Parsar `command`/`args` pair. Add them only after Parsar supports
  audited MCPB installation.
- Do not list MCP Apps whose primary experience requires an embedded
  `io.modelcontextprotocol/ui` host. A runnable stdio example alone is not a
  complete Parsar connector until the web client can render that UI contract.
- `featured_rank` is the repository's explicit curation order. It is not a
  claim about usage or popularity.
- `env` declares variable names. Every value must be an empty string; secrets,
  API keys, tokens, and passwords must never be committed to the catalog.
- Use only `http` or `https` metadata URLs without embedded credentials. Remote
  MCP endpoints must use HTTPS.
- Update `updated_at` whenever catalog content changes.

Validate changes with the Go tests in `server/internal/mcpcatalog` and the full
repository gate:

```bash
go test ./server/internal/mcpcatalog
make check
```
