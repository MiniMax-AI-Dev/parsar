-- +goose Up

-- Earlier directory builds registered provider-specific *_mcp_oauth kinds.
-- Some databases already reached migration 11/12 on those builds and will
-- therefore skip the current main branch's migration 000010. Register the
-- generic kind again before normalizing their existing rows.

INSERT INTO credential_kinds (
  code, display_name, description, source, built_in
)
VALUES (
  'mcp_oauth',
  'MCP OAuth',
  'OAuth credential for hosted MCP connectors',
  'platform_oauth',
  TRUE
)
ON CONFLICT DO NOTHING;

UPDATE capability_version
SET required_credentials = (
  SELECT COALESCE(
    jsonb_agg(
      CASE
        WHEN entry->>'kind' IN ('notion_mcp_oauth', 'linear_mcp_oauth', 'sentry_mcp_oauth')
          THEN jsonb_set(entry, '{kind}', to_jsonb('mcp_oauth'::text), TRUE)
        ELSE entry
      END
    ),
    '[]'::jsonb
  )
  FROM jsonb_array_elements(required_credentials) AS entry
)
WHERE source_payload->>'catalog_id' IN ('notion', 'linear', 'sentry')
  AND required_credentials::text ~ '(notion|linear|sentry)_mcp_oauth';

UPDATE capability_version
SET canonical_spec = jsonb_set(
  canonical_spec,
  '{mcp,servers,0,headers,Authorization,credential_kind_code}',
  to_jsonb('mcp_oauth'::text),
  TRUE
)
WHERE source_payload->>'catalog_id' IN ('notion', 'linear', 'sentry')
  AND canonical_spec->'mcp'->'servers'->0->'headers'->'Authorization'->>'credential_kind_code'
    IN ('notion_mcp_oauth', 'linear_mcp_oauth', 'sentry_mcp_oauth');

UPDATE secrets
SET metadata = jsonb_set(
      metadata,
      '{credential_kind_code}',
      to_jsonb('mcp_oauth'::text),
      TRUE
    ),
    updated_at = now()
WHERE provider IN ('notion', 'linear', 'sentry')
  AND metadata->>'credential_kind_code'
    IN ('notion_mcp_oauth', 'linear_mcp_oauth', 'sentry_mcp_oauth');

-- Keep each connector binding capability-scoped. Otherwise one Agent using
-- multiple hosted connectors would have those Secrets overwrite each other
-- under the shared mcp_oauth key.
WITH legacy_bindings(catalog_id, legacy_kind) AS (
  VALUES
    ('notion', 'notion_mcp_oauth'),
    ('linear', 'linear_mcp_oauth'),
    ('sentry', 'sentry_mcp_oauth')
), capability_bindings AS (
  SELECT
    ac.id,
    ac.configuration->'credential_bindings'->legacy_bindings.legacy_kind AS binding
  FROM agent_capabilities ac
  JOIN capability_version cv ON cv.id = ac.capability_version_id
  JOIN legacy_bindings
    ON legacy_bindings.catalog_id = cv.source_payload->>'catalog_id'
  WHERE jsonb_typeof(ac.configuration->'credential_bindings'->legacy_bindings.legacy_kind) = 'object'
    AND NOT (
      CASE
        WHEN jsonb_typeof(ac.configuration->'credential_bindings') = 'object'
          THEN ac.configuration->'credential_bindings'
        ELSE '{}'::jsonb
      END ? 'mcp_oauth'
    )
)
UPDATE agent_capabilities ac
SET configuration = jsonb_set(
      ac.configuration,
      '{credential_bindings}',
      (
        CASE
          WHEN jsonb_typeof(ac.configuration->'credential_bindings') = 'object'
            THEN ac.configuration->'credential_bindings'
          ELSE '{}'::jsonb
        END
      ) || jsonb_build_object('mcp_oauth', capability_bindings.binding),
      TRUE
    ),
    updated_at = now()
FROM capability_bindings
WHERE ac.id = capability_bindings.id;

WITH legacy_bindings(catalog_id, legacy_kind) AS (
  VALUES
    ('notion', 'notion_mcp_oauth'),
    ('linear', 'linear_mcp_oauth'),
    ('sentry', 'sentry_mcp_oauth')
), agent_bindings AS (
  SELECT
    ac.id,
    agents.config->'credential_bindings'->legacy_bindings.legacy_kind AS binding
  FROM agent_capabilities ac
  JOIN agents ON agents.id = ac.agent_id
  JOIN capability_version cv ON cv.id = ac.capability_version_id
  JOIN legacy_bindings
    ON legacy_bindings.catalog_id = cv.source_payload->>'catalog_id'
  WHERE jsonb_typeof(agents.config->'credential_bindings'->legacy_bindings.legacy_kind) = 'object'
    AND NOT (
      CASE
        WHEN jsonb_typeof(ac.configuration->'credential_bindings') = 'object'
          THEN ac.configuration->'credential_bindings'
        ELSE '{}'::jsonb
      END ? 'mcp_oauth'
    )
)
UPDATE agent_capabilities ac
SET configuration = jsonb_set(
      ac.configuration,
      '{credential_bindings}',
      (
        CASE
          WHEN jsonb_typeof(ac.configuration->'credential_bindings') = 'object'
            THEN ac.configuration->'credential_bindings'
          ELSE '{}'::jsonb
        END
      ) || jsonb_build_object('mcp_oauth', agent_bindings.binding),
      TRUE
    ),
    updated_at = now()
FROM agent_bindings
WHERE ac.id = agent_bindings.id;

-- Preserve historical rows for foreign-key compatibility, but hide obsolete
-- provider-specific kinds from new credential pickers.
UPDATE credential_kinds
SET deleted_at = COALESCE(deleted_at, now()),
    updated_at = now()
WHERE built_in = TRUE
  AND code IN ('notion_mcp_oauth', 'linear_mcp_oauth', 'sentry_mcp_oauth')
  AND deleted_at IS NULL;

-- +goose Down

-- The data normalization is intentionally not reversed. Restoring the old
-- provider-specific kinds would make current catalog capabilities unusable.
SELECT 1;
