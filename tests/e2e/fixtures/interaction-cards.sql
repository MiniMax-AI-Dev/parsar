BEGIN;

-- Keep this browser fixture runnable against a freshly migrated database.
-- These IDs mirror store.DefaultDevFixtureIDs, but the setup is intentionally
-- SQL-only so Playwright does not depend on a Go test side effect or a
-- developer having manually seeded the database first.
INSERT INTO users (id, email, name, status, created_at, updated_at)
VALUES (
  '00000000-0000-0000-0000-000000000001',
  'admin@example.com', 'Dev Admin', 'active', now(), now()
)
ON CONFLICT (id) DO UPDATE SET status = 'active', updated_at = now();

INSERT INTO workspaces (id, name, slug, created_by, created_at, updated_at)
VALUES (
  '00000000-0000-0000-0000-000000000002',
  'Demo Workspace', 'demo',
  '00000000-0000-0000-0000-000000000001', now(), now()
)
ON CONFLICT (id) DO UPDATE SET updated_at = now();

INSERT INTO workspace_members (
  id, workspace_id, user_id, role, status, created_at, updated_at
)
VALUES (
  '00000000-0000-0000-0000-000000000003',
  '00000000-0000-0000-0000-000000000002',
  '00000000-0000-0000-0000-000000000001',
  'owner', 'active', now(), now()
)
ON CONFLICT DO NOTHING;

INSERT INTO agents (
  id, workspace_id, name, slug, description, connector_type,
  status, config, created_by, created_at, updated_at
)
VALUES
  (
    '00000000-0000-0000-0000-000000000006',
    '00000000-0000-0000-0000-000000000002',
    'Product Agent', 'product-agent', 'Interaction card fixture',
    'agent_daemon', 'active',
    '{"daemon_mode":"local","agent_kind":"codex"}'::jsonb,
    '00000000-0000-0000-0000-000000000001', now(), now()
  ),
  (
    '00000000-0000-0000-0000-000000000007',
    '00000000-0000-0000-0000-000000000002',
    'Backend Agent', 'backend-agent', 'Interaction card fixture',
    'agent_daemon', 'active',
    '{"daemon_mode":"local","agent_kind":"codex"}'::jsonb,
    '00000000-0000-0000-0000-000000000001', now(), now()
  )
ON CONFLICT (id) DO UPDATE SET
  status = 'active', config = EXCLUDED.config, updated_at = now();

INSERT INTO conversations (
  id, workspace_id, surface, form, title, status, metadata,
  created_at, updated_at
)
VALUES (
  '00000000-0000-0000-0000-000000000012',
  '00000000-0000-0000-0000-000000000002',
  'web', 'group', 'Interaction Cards', 'active', '{}'::jsonb,
  now(), now()
)
ON CONFLICT (id) DO UPDATE SET status = 'active', updated_at = now();

INSERT INTO agent_runs (
  id, workspace_id, conversation_id, trigger_source, trigger_channel,
  requested_by_type, requested_by_id, agent_id, connector_type,
  status, visibility, metadata, created_at, started_at, updated_at
) VALUES
  (
    '11111111-1111-4111-8111-111111111111',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    'manual', 'web', 'user',
    '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000007',
    'agent_daemon', 'running', 'workspace',
    '{"preview":true}'::jsonb, now(), now(), now()
  ),
  (
    '22222222-2222-4222-8222-222222222222',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    'manual', 'web', 'user',
    '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000006',
    'agent_daemon', 'running', 'workspace',
    '{"preview":true}'::jsonb, now(), now(), now()
  ),
  (
    '33333333-3333-4333-8333-333333333333',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    'manual', 'web', 'user',
    '00000000-0000-0000-0000-000000000001',
    '00000000-0000-0000-0000-000000000006',
    'agent_daemon', 'running', 'workspace',
    '{"preview":true}'::jsonb, now(), now(), now()
  )
ON CONFLICT (id) DO UPDATE SET
  status = 'running',
  finished_at = NULL,
  started_at = now(),
  updated_at = now();

INSERT INTO agent_interactions (
  id, workspace_id, conversation_id, agent_run_id, request_id,
  kind, status, request, response, device_id,
  created_at, expires_at, updated_at
) VALUES
  (
    'aaaaaaaa-1111-4111-8111-111111111111',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    '11111111-1111-4111-8111-111111111111',
    'preview-permission-001', 'permission', 'pending',
    '{
      "request_id":"preview-permission-001",
      "device_id":"preview-device",
      "resource":"Write production configuration",
      "action":"Modify files",
      "detail":"Agent wants to update deploy/production.yaml and restart the service.",
      "payload":{
        "command":"apply deployment configuration",
        "paths":["deploy/production.yaml"],
        "risk":"high"
      }
    }'::jsonb,
    '{}'::jsonb, 'preview-device', now(), now() + interval '10 minutes', now()
  ),
  (
    'aaaaaaaa-2222-4222-8222-222222222222',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    '22222222-2222-4222-8222-222222222222',
    'preview-question-001', 'user_choice', 'pending',
    '{
      "request_id":"preview-question-001",
      "device_id":"preview-device",
      "questions":[
        {
          "id":"q0",
          "header":"Deployment target",
          "question":"Which environment should the Agent deploy to?",
          "options":[
            {"label":"Staging","description":"Deploy to the shared test environment."},
            {"label":"Production","description":"Deploy to the live customer environment."}
          ],
          "multi_select":false,
          "is_other":false,
          "is_secret":false
        },
        {
          "id":"verification_steps",
          "header":"Verification",
          "question":"Which checks should run after deployment?",
          "options":[
            {"label":"Smoke tests","description":"Run the critical user journey checks."},
            {"label":"Full regression","description":"Run the complete test suite."}
          ],
          "multi_select":true,
          "is_other":false,
          "is_secret":false
        }
      ]
    }'::jsonb,
    '{}'::jsonb, 'preview-device', now(), now() + interval '10 minutes', now()
  ),
  (
    'aaaaaaaa-3333-4333-8333-333333333333',
    '00000000-0000-0000-0000-000000000002',
    '00000000-0000-0000-0000-000000000012',
    '33333333-3333-4333-8333-333333333333',
    'preview-secret-001', 'user_choice', 'pending',
    '{
      "request_id":"preview-secret-001",
      "device_id":"preview-device",
      "questions":[
        {
          "id":"q0",
          "header":"Deployment token",
          "question":"Which credential should the Agent use?",
          "options":[
            {"label":"Use stored token","description":"Use the token already available to the runtime."}
          ],
          "multi_select":false,
          "is_other":true,
          "is_secret":true
        }
      ]
    }'::jsonb,
    '{}'::jsonb, 'preview-device', now(), now() + interval '10 minutes', now()
  )
ON CONFLICT (id) DO UPDATE SET
  status = 'pending',
  request = EXCLUDED.request,
  response = '{}'::jsonb,
  claim_token = NULL,
  claimed_at = NULL,
  resolution_source = NULL,
  resolved_actor = NULL,
  resolved_by = NULL,
  created_at = now(),
  expires_at = now() + interval '10 minutes',
  resolved_at = NULL,
  updated_at = now();

COMMIT;
