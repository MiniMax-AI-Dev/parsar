-- +goose Up

INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES
  (
    'sentry_mcp_oauth',
    'Sentry MCP OAuth',
    'OAuth access for the official Sentry remote MCP server',
    'platform_oauth',
    TRUE
  ),
  (
    'linear_mcp_oauth',
    'Linear MCP OAuth',
    'OAuth access for the official Linear remote MCP server',
    'platform_oauth',
    TRUE
  ),
  (
    'stripe_mcp_oauth',
    'Stripe MCP OAuth',
    'OAuth access for the official Stripe remote MCP server',
    'platform_oauth',
    TRUE
  )
ON CONFLICT DO NOTHING;

-- +goose Down

DELETE FROM credential_kinds
WHERE code IN (
    'sentry_mcp_oauth',
    'linear_mcp_oauth',
    'stripe_mcp_oauth'
  )
  AND built_in = TRUE;
