-- +goose Up

-- OAuth payloads remain in the existing secrets table. This row only
-- registers the distinct kind used by Capability credential_ref validation.

INSERT INTO credential_kinds (
  code, display_name, description, source, built_in
)
VALUES (
  'notion_mcp_oauth',
  'Notion MCP OAuth',
  'Notion MCP OAuth access token',
  'platform_oauth',
  TRUE
)
ON CONFLICT DO NOTHING;

-- +goose Down

-- Rollback only; normal startup does not execute this section.

DELETE FROM credential_kinds
WHERE code = 'notion_mcp_oauth'
  AND built_in = TRUE;
