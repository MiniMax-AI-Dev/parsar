-- +goose Up

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

UPDATE secrets
SET metadata = jsonb_set(
  metadata,
  '{credential_kind_code}',
  '"notion_mcp_oauth"'::jsonb,
  TRUE
)
WHERE kind = 'capability_inline'
  AND provider = 'notion'
  AND auth_type = 'oauth2'
  AND metadata ->> 'credential_kind_code' = 'notion_integration';

UPDATE capability_version
SET canonical_spec = replace(
      canonical_spec::text,
      '"notion_integration"',
      '"notion_mcp_oauth"'
    )::jsonb,
    required_credentials = replace(
      required_credentials::text,
      '"notion_integration"',
      '"notion_mcp_oauth"'
    )::jsonb,
    source_payload = jsonb_set(
      source_payload,
      '{catalog_version}',
      '"1.0.1"'::jsonb,
      TRUE
    )
WHERE source_payload ->> 'source_format' = 'mcp_catalog'
  AND source_payload ->> 'catalog_id' = 'notion'
  AND canonical_spec::text LIKE '%"notion_integration"%';

-- +goose Down

DELETE FROM credential_kinds
WHERE code = 'notion_mcp_oauth'
  AND built_in = TRUE;

UPDATE secrets
SET metadata = jsonb_set(
  metadata,
  '{credential_kind_code}',
  '"notion_integration"'::jsonb,
  TRUE
)
WHERE kind = 'capability_inline'
  AND provider = 'notion'
  AND auth_type = 'oauth2'
  AND metadata ->> 'credential_kind_code' = 'notion_mcp_oauth';

UPDATE capability_version
SET canonical_spec = replace(
      canonical_spec::text,
      '"notion_mcp_oauth"',
      '"notion_integration"'
    )::jsonb,
    required_credentials = replace(
      required_credentials::text,
      '"notion_mcp_oauth"',
      '"notion_integration"'
    )::jsonb,
    source_payload = jsonb_set(
      source_payload,
      '{catalog_version}',
      '"1.0.0"'::jsonb,
      TRUE
    )
WHERE source_payload ->> 'source_format' = 'mcp_catalog'
  AND source_payload ->> 'catalog_id' = 'notion'
  AND canonical_spec::text LIKE '%"notion_mcp_oauth"%';
