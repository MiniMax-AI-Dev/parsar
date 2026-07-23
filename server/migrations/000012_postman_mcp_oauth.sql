-- +goose Up

INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES (
  'postman_mcp_oauth',
  'Postman MCP OAuth',
  'OAuth access for the official Postman remote MCP server',
  'platform_oauth',
  TRUE
)
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM credential_kinds
WHERE code = 'postman_mcp_oauth'
  AND built_in = TRUE;
