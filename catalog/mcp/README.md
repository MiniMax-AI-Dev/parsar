# MCP Connector Directory Catalog

`catalog.json` is the repository-maintained source for Parsar's built-in MCP
Connector Directory. It contains metadata plus either stdio launch configuration
or a credential-free Streamable HTTP endpoint. Importing an item saves a
workspace capability and never executes it.

## Updating the catalog

- Add only MCP servers that can be verified in an official repository or the
  official MCP Registry.
- Keep `id` stable and unique. Renaming an item does not require changing its
  ID.
- Pin npm and Python packages to an explicit version. Do not use `latest`.
- Stdio entries use `command`, `args`, `env`, and `startup_timeout_sec`.
- Streamable HTTP entries use an HTTPS `url` only. Built-in remote entries must
  complete an MCP initialize request without headers, API keys, OAuth, or other
  user credentials before they are added.
- Catalog entries may rely on tools such as `npx` or `uvx` being available in
  the eventual Runtime. Importing a connector does not install those tools or
  download its package.
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

## Remote catalog override

Operators may set `PARSAR_MCP_CATALOG_URL` to a trusted JSON endpoint with the
same schema. Parsar applies a bounded download size, HTTP timeout, redirect
limit, and full structural validation. A failed remote load falls back to the
embedded catalog. Catalog URLs cannot be supplied through an API request.
