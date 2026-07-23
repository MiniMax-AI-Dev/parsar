# MCP Connector Directory Catalog

`catalog.json` is the repository-maintained source for Parsar's built-in MCP
Connector Directory. It contains metadata plus hosted Streamable HTTP endpoints.
Importing an item saves a workspace capability and never executes it.

## Updating the catalog

- Add only MCP servers that can be verified in an official repository or the
  official MCP Registry.
- Keep `id` stable and unique. Renaming an item does not require changing its
  ID.
- Entries use an HTTPS `url` only.
- OAuth entries must support MCP OAuth discovery, PKCE, and dynamic client
  registration. Tokens are stored in the existing encrypted workspace secret
  flow and are never included in the catalog.
- Use only HTTPS URLs without embedded credentials.
- Update `updated_at` whenever catalog content changes.

Validate changes with the Go tests in `server/internal/mcpcatalog` and the full
repository gate:

```bash
go test ./server/internal/mcpcatalog
make check
```
