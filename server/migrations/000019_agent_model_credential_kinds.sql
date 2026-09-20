-- +goose Up

-- Agent model credential selection uses the existing registry even when the
-- deployment has never loaded the development fixture.
INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES
  ('openai_api_key', 'OpenAI API Key', 'Personal OpenAI API key (sk-...)', 'platform_model', TRUE),
  ('anthropic_api_key', 'Anthropic API Key', 'Personal Anthropic API key (sk-ant-...)', 'platform_model', TRUE)
ON CONFLICT DO NOTHING;

-- +goose Down

-- Keep registry entries on rollback: credentials and Agent bindings may already
-- reference them, and older releases can safely retain these metadata rows.
SELECT 1;
