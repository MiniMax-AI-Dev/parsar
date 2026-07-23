-- +goose Up

INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES
  (
    'notion_mcp_oauth',
    'Notion MCP OAuth',
    'OAuth access for the official Notion remote MCP server',
    'platform_oauth',
    TRUE
  )
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM credential_kinds
WHERE code = 'notion_mcp_oauth'
  AND built_in = TRUE;
