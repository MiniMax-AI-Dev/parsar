-- +goose Up
-- ==============================================================
-- 000001_init — squashed initial schema (2026-06-23, third squash)
-- ==============================================================
-- History:
--   * 2026-06-04 first squash: collapsed upstream 000001..000034 (34
--     migrations) into a single file 000001_init.
--   * 2026-06-11 second squash: folded in the migrations appended after
--     the first squash — 000002..000007 (agent_daemon device owners /
--     capability canonical / credential_kinds UI column cleanup /
--     spec_memory / gateway_sessions / capability_version plugin
--     columns) — plus an orphan remove_admin_state migration that had
--     been misplaced at the top-level migrations/ directory and never
--     actually executed.
--   * 2026-06-23 third squash (this file): folded in the 8 migrations
--     appended after the second squash, 000004..000011:
--       - 000004 workspace_join_requests: workspaces.visibility +
--                workspace_members state machine (status/request_reason/
--                reviewed_by/reviewed_at) + rebuilt indexes
--       - 000005 serialize_runs_cancel_stale: one-shot UPDATE, no-op on
--                an empty DB, **no longer written to init**
--       - 000006 runtime_id: runtime binding column, now merged into
--                agents.runtime_id
--       - 000007 backfill_local_device_runtime_id: one-shot UPDATE,
--                no-op on an empty DB, **no longer written to init**
--       - 000008 pending_credential_form_inflight_slot: conversations
--                gateway_inflight ADR-004 reverse-lookup index
--       - 000009 credential_kind_source: credential_kinds.source column
--                + CHECK constraint + seed rows carry source directly
--       - 000010 drop_project_members: fully removes the project_members
--                table
--       - 000010 prompt_for_user_choice_inflight_slot: conversations
--                gateway_inflight AskUserQuestion reverse-lookup index
--       - 000011 capability_pinning_mode: agent_capabilities.pinning_mode
--   Post-merge we're back to the "single init file" shape; the schema
--   going to production (first deploy) is identical to what the code
--   expects.
--
-- Why squash:
-- History:
--   * This repo has diverged from upstream; every environment
--     (dev / staging / first production deploy) goes through
--     DROP+recreate, so intermediate states carry no value for anyone.
--   * Incremental ALTER/DROP is just noise when reviewing schema;
--     reading the final CREATE is faster.
--   * git holds the historical detail, and the file name stays clean.
--
-- Future schema changes add incremental 000002+ migrations; this file
-- remains the source of truth for the initial schema.
--
-- Down:
--   The Down of an initial migration is equivalent to "wipe the
--   database". We use the brute-force DROP SCHEMA public CASCADE
--   instead of a per-table DROP — goose down to v0 semantically means
--   "back to a clean DB", and listing 29 DROP TABLE statements would
--   fail easily due to constraint ordering.

-- ============================================================
-- Table: users
-- ============================================================
CREATE TABLE IF NOT EXISTS users (
  id         uuid PRIMARY KEY,
  email      text UNIQUE NOT NULL,
  name       text NOT NULL DEFAULT '',
  status     text NOT NULL DEFAULT 'active',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

COMMENT ON TABLE  users IS 'Platform-wide user table';
COMMENT ON COLUMN users.id         IS 'Internal user ID';
COMMENT ON COLUMN users.email      IS 'Business-unique identifier';
COMMENT ON COLUMN users.name       IS 'User display name';
COMMENT ON COLUMN users.status     IS 'Account status: active = normal / disabled = banned';
COMMENT ON COLUMN users.created_at IS 'Registration time';
COMMENT ON COLUMN users.updated_at IS 'Last updated at';
COMMENT ON COLUMN users.deleted_at IS 'Soft-delete timestamp; NULL = not deleted';


-- ============================================================
-- Table: workspaces
-- ============================================================
CREATE TABLE IF NOT EXISTS workspaces (
  id         uuid PRIMARY KEY,
  name       text NOT NULL,
  slug       text UNIQUE NOT NULL,
  visibility text NOT NULL DEFAULT 'private'
    CHECK (visibility IN ('public', 'private')),
  config     jsonb NOT NULL DEFAULT '{}',
  created_by uuid REFERENCES users(id),
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL,
  deleted_at timestamptz
);

CREATE INDEX IF NOT EXISTS idx_workspaces_visibility_active
  ON workspaces(visibility)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  workspaces IS 'Team-level tenant container';
COMMENT ON COLUMN workspaces.id         IS 'Internal workspace ID';
COMMENT ON COLUMN workspaces.name       IS 'Display name';
COMMENT ON COLUMN workspaces.slug       IS 'URL/CLI identifier';
COMMENT ON COLUMN workspaces.visibility IS 'Visibility: public = discoverable and joinable by request / private = invite-only';
COMMENT ON COLUMN workspaces.config     IS 'Workspace JSON config';
COMMENT ON COLUMN workspaces.created_by IS 'Created by';
COMMENT ON COLUMN workspaces.deleted_at IS 'Soft-delete timestamp; NULL = not deleted';
COMMENT ON INDEX  idx_workspaces_visibility_active IS 'Discover endpoint filters by visibility';


-- ============================================================
-- Table: workspace_members
--
-- status state machine:
--   pending  — self-service join request, waiting on owner/admin approval
--   active   — full member; all RBAC queries only accept this state
--   rejected — request rejected; row kept for audit, UNIQUE index excludes
--              it so the user can re-apply
-- ============================================================
CREATE TABLE IF NOT EXISTS workspace_members (
  id             uuid PRIMARY KEY,
  workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  user_id        uuid NOT NULL REFERENCES users(id)      ON DELETE CASCADE,
  role           text NOT NULL,
  status         text NOT NULL DEFAULT 'active'
    CHECK (status IN ('pending', 'active', 'rejected')),
  request_reason text,
  reviewed_by    uuid REFERENCES users(id),
  reviewed_at    timestamptz,
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL,
  deleted_at     timestamptz
);

-- Only one non-rejected active row per workspace+user; rejected rows are
-- kept for audit and don't block a new pending row (user can re-apply
-- after being rejected).
CREATE UNIQUE INDEX IF NOT EXISTS uk_workspace_members_active
  ON workspace_members(workspace_id, user_id)
  WHERE deleted_at IS NULL AND status <> 'rejected';

-- Approval UI queries pending join requests per workspace.
CREATE INDEX IF NOT EXISTS idx_workspace_members_status_pending
  ON workspace_members(workspace_id, status)
  WHERE deleted_at IS NULL AND status = 'pending';

COMMENT ON TABLE  workspace_members IS 'Workspace members and roles (with join-request state machine)';
COMMENT ON COLUMN workspace_members.workspace_id   IS 'Owning workspace';
COMMENT ON COLUMN workspace_members.user_id        IS 'Owning user';
COMMENT ON COLUMN workspace_members.role           IS 'Workspace role: owner / admin / member / viewer (read-only)';
COMMENT ON COLUMN workspace_members.status         IS 'Membership status: pending / active / rejected (row kept for audit)';
COMMENT ON COLUMN workspace_members.request_reason IS 'Reason submitted with the join request (nullable); meaningless on active rows';
COMMENT ON COLUMN workspace_members.reviewed_by    IS 'Reviewer user_id (set on approve or reject)';
COMMENT ON COLUMN workspace_members.reviewed_at    IS 'Review time (set on approve or reject)';
COMMENT ON COLUMN workspace_members.deleted_at     IS 'Soft-delete timestamp; NULL = currently active member';
COMMENT ON INDEX  uk_workspace_members_active           IS 'Only one non-rejected active row per workspace+user; rejected rows kept for audit and do not block a new pending row';
COMMENT ON INDEX  idx_workspace_members_status_pending  IS 'Approval UI queries pending join requests per workspace';


-- ============================================================
-- Table: auth_identities
-- ============================================================
CREATE TABLE IF NOT EXISTS auth_identities (
  id         uuid PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider   text NOT NULL,
  subject    text NOT NULL,
  metadata   jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL,
  updated_at timestamptz NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_auth_identities_provider_subject
  ON auth_identities(provider, subject);

COMMENT ON TABLE  auth_identities IS 'External identity bindings for a user';
COMMENT ON COLUMN auth_identities.user_id  IS 'Local user';
COMMENT ON COLUMN auth_identities.provider IS 'Identity provider: email = local email/password / feishu = Feishu login / oidc = generic OIDC';
COMMENT ON COLUMN auth_identities.subject  IS 'External unique identifier';
COMMENT ON COLUMN auth_identities.metadata IS 'Identity provider extra info';
COMMENT ON INDEX  uk_auth_identities_provider_subject IS 'provider+subject globally unique';


-- ============================================================
-- Table: user_sessions
-- ============================================================
CREATE TABLE IF NOT EXISTS user_sessions (
  id           text PRIMARY KEY,
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  user_agent   text NOT NULL DEFAULT '',
  ip           text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL,
  last_seen_at timestamptz NOT NULL,
  expires_at   timestamptz NOT NULL,
  revoked_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_user_sessions_user_active
  ON user_sessions(user_id, expires_at DESC)
  WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_sessions_expires_at_brin
  ON user_sessions USING brin (expires_at);

COMMENT ON TABLE  user_sessions IS 'User login sessions';
COMMENT ON COLUMN user_sessions.id           IS 'Session token';
COMMENT ON COLUMN user_sessions.user_id      IS 'Owning user';
COMMENT ON COLUMN user_sessions.user_agent   IS 'Login device UA';
COMMENT ON COLUMN user_sessions.ip           IS 'Login source IP';
COMMENT ON COLUMN user_sessions.created_at   IS 'Session creation time';
COMMENT ON COLUMN user_sessions.last_seen_at IS 'Most recent request time';
COMMENT ON COLUMN user_sessions.expires_at   IS 'Expiration time';
COMMENT ON COLUMN user_sessions.revoked_at   IS 'Explicit logout time; NULL = not revoked';
COMMENT ON INDEX  idx_user_sessions_user_active      IS 'Query active sessions by user';
COMMENT ON INDEX  idx_user_sessions_expires_at_brin  IS 'Sweep sessions by expiration time';


-- ============================================================
-- Table: user_credentials
-- ============================================================
CREATE TABLE IF NOT EXISTS user_credentials (
  id            uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       uuid         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind          text         NOT NULL,
  display_name  text         NOT NULL DEFAULT '',
  ciphertext    bytea        NOT NULL,
  key_version   text         NOT NULL DEFAULT 'v1',
  last_used_at  timestamptz,
  created_at    timestamptz  NOT NULL DEFAULT NOW(),
  updated_at    timestamptz  NOT NULL DEFAULT NOW(),
  deleted_at    timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_user_credentials_user_kind_active
  ON user_credentials(user_id, kind)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_user_credentials_user_active
  ON user_credentials(user_id)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  user_credentials IS 'User-owned external capability credentials';
COMMENT ON COLUMN user_credentials.id           IS 'Credential ID';
COMMENT ON COLUMN user_credentials.user_id      IS 'Owning user';
COMMENT ON COLUMN user_credentials.kind         IS 'Credential type; validated against the code-side registry';
COMMENT ON COLUMN user_credentials.display_name IS 'Credential display name';
COMMENT ON COLUMN user_credentials.ciphertext   IS 'Encrypted credential ciphertext';
COMMENT ON COLUMN user_credentials.key_version  IS 'Encryption key version';
COMMENT ON COLUMN user_credentials.last_used_at IS 'Most recent use time';
COMMENT ON COLUMN user_credentials.updated_at   IS 'Last updated at';
COMMENT ON COLUMN user_credentials.deleted_at   IS 'Soft-delete timestamp';
COMMENT ON INDEX  uk_user_credentials_user_kind_active IS 'At most one active record per (user, kind)';
COMMENT ON INDEX  idx_user_credentials_user_active     IS 'Query active credentials by user';


-- ============================================================
-- Table: credential_kinds
-- ============================================================
-- Credential-type registry. Sourced from server/migrations/000003;
-- the 5 kinds originally hard-coded in
-- server/internal/capability/credential_kind.go are seeded here, and
-- admins can inline-add new ones via the capability import UI.
--
-- source classification:
--   * platform_oauth  — providers with a platform-implemented OAuth flow
--                       (currently only github_pat; Slack/Feishu etc. stay
--                        user_defined until integration lands)
--   * platform_model  — LLM provider API keys; the model catalog in the
--                       models table references them via
--                       credential_mode=credential_ref
--   * user_defined    — kinds added ad hoc by admins during capability
--                       import (default value)
-- Application code reads source to split the "Connections" page into an
-- OAuth section + a model API-key section.
CREATE TABLE IF NOT EXISTS credential_kinds (
  id                uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  code              text         NOT NULL,
  display_name      text         NOT NULL,
  description       text         NOT NULL DEFAULT '',
  value_schema      jsonb        NOT NULL DEFAULT '{}'::jsonb,
  source            text         NOT NULL DEFAULT 'user_defined',
  built_in          boolean      NOT NULL DEFAULT FALSE,
  created_by        uuid         REFERENCES users(id),
  created_at        timestamptz  NOT NULL DEFAULT NOW(),
  updated_at        timestamptz  NOT NULL DEFAULT NOW(),
  deleted_at        timestamptz,
  CONSTRAINT credential_kinds_source_chk
    CHECK (source IN ('platform_oauth', 'platform_model', 'user_defined'))
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_credential_kinds_code_active
  ON credential_kinds(code)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_credential_kinds_active
  ON credential_kinds(code)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  credential_kinds IS 'Credential-type registry; admins can add new kinds directly via the capability import UI';
COMMENT ON COLUMN credential_kinds.id           IS 'Credential kind primary key';
COMMENT ON COLUMN credential_kinds.code         IS 'Globally unique short code; matches user_credentials.kind text';
COMMENT ON COLUMN credential_kinds.display_name IS 'Display name (single column for CN/EN); required non-empty';
COMMENT ON COLUMN credential_kinds.description  IS 'Type description; shown to end users';
COMMENT ON COLUMN credential_kinds.value_schema IS 'Value validation schema (reserved); v1 does not enforce it';
COMMENT ON COLUMN credential_kinds.source       IS 'Classification: platform_oauth = built-in OAuth provider / platform_model = LLM provider API key / user_defined = ad-hoc admin addition';
COMMENT ON COLUMN credential_kinds.built_in     IS 'System-seed flag; built_in=true rows cannot be deleted';
COMMENT ON COLUMN credential_kinds.created_by   IS 'Admin who added the row; NULL on built_in=true rows';
COMMENT ON COLUMN credential_kinds.deleted_at   IS 'Soft-delete timestamp; NULL = active';
COMMENT ON INDEX  uk_credential_kinds_code_active IS 'code unique among active records';
COMMENT ON INDEX  idx_credential_kinds_active     IS 'Look up active kinds by code';

-- Seed: write the 5 built-in kinds originally hard-coded in
-- server/internal/capability/credential_kind.go into the registry.
-- built_in=TRUE marks system seeds, which the UI does not allow deleting.
-- ON CONFLICT DO NOTHING keeps the bundled seed idempotent on re-run.
INSERT INTO credential_kinds (code, display_name, description, source, built_in)
VALUES
  ('github_pat',         'GitHub Access Token', 'GitHub Personal Access Token', 'platform_oauth', TRUE),
  ('slack_bot_token',    'Slack Bot Token',     'Slack Bot Token (xoxb-…)',     'user_defined',   TRUE),
  ('postgres_dsn',       'Postgres DSN',        'Postgres DSN',                 'user_defined',   TRUE),
  ('notion_integration', 'Notion Integration Token', 'Notion Integration Token', 'user_defined',   TRUE),
  ('jira_api_token',     'Jira API Token',      'Atlassian Jira API Token',     'user_defined',   TRUE)
ON CONFLICT DO NOTHING;


-- ============================================================
-- Table: secrets
-- ============================================================
-- Organization-level shared encrypted-credential table. All kinds share
-- this single table and the same secrets.Service encryption layer.
-- Known kinds:
--   model_provider     — Shared model API key (bound via models.secret_id)
--   runtime            — Sandbox provider credentials (pointer at
--                        workspaces.config.runtime_credential_secret_id)
--   capability_inline  — MCP capability inline_secret (pointer at
--                        canonical_spec.env_value.secret_id)
--   feishu_bot         — Feishu bot app secret
--   slack_bot          — Per-workspace Slack bot token (xoxb-…), resolved
--                        by metadata->>'team_id'
--                        (ResolveSlackBotSecretByTeam)
CREATE TABLE IF NOT EXISTS secrets (
  id                  uuid PRIMARY KEY,
  slug                text NOT NULL,
  name                text NOT NULL,
  kind                text NOT NULL DEFAULT 'model_provider',
  provider            text NOT NULL,
  auth_type           text NOT NULL,
  encrypted_payload   jsonb NOT NULL,
  key_version         text NOT NULL DEFAULT 'v1',
  metadata            jsonb NOT NULL DEFAULT '{}',
  status              text NOT NULL DEFAULT 'active',
  created_by          uuid REFERENCES users(id),
  created_at          timestamptz NOT NULL,
  updated_at          timestamptz NOT NULL,
  deleted_at          timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_secrets_slug_active
  ON secrets(slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_secrets_kind_active
  ON secrets(kind, status)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  secrets IS 'Organization-level encrypted credential table (shared catalog)';
COMMENT ON COLUMN secrets.id                IS 'Secret primary key';
COMMENT ON COLUMN secrets.slug              IS 'Machine-readable stable identifier (auto-generated, globally unique)';
COMMENT ON COLUMN secrets.name              IS 'Secret display name (may repeat, may be edited)';
COMMENT ON COLUMN secrets.kind              IS 'Secret usage classification: model_provider/runtime/capability_inline/feishu_bot';
COMMENT ON COLUMN secrets.provider          IS 'Provider identifier';
COMMENT ON COLUMN secrets.auth_type         IS 'Auth type';
COMMENT ON COLUMN secrets.encrypted_payload IS 'Encrypted credential payload';
COMMENT ON COLUMN secrets.key_version       IS 'Wrapping key version';
COMMENT ON COLUMN secrets.metadata          IS 'Non-sensitive metadata';
COMMENT ON COLUMN secrets.status            IS 'Enabled state: active = usable / disabled = admin-disabled';
COMMENT ON COLUMN secrets.created_by        IS 'Created by';
COMMENT ON COLUMN secrets.deleted_at        IS 'Soft-delete marker; non-null means deleted';
COMMENT ON INDEX  uk_secrets_slug_active IS 'Secret slug unique among active rows';
COMMENT ON INDEX  idx_secrets_kind_active IS 'Filter secrets by kind/status';

-- ============================================================
-- Table: models
-- ============================================================
-- Organization-level shared model catalog (wiki/bulletin-like semantics).
-- Visible and usable by all users; only the creator (or superadmin) may
-- edit or delete. No workspace_id — the whole company shares one catalog.
-- Provider info is inlined here (replacing the old model_providers side
-- table). Credentials are one of two modes:
--   credential_mode='inline_secret'  → secret_id references secrets (org-shared)
--   credential_mode='credential_ref' → credential_kind_code references
--                                       credential_kinds.code (at runtime,
--                                       user_credentials is looked up by
--                                       caller user_id + kind)
CREATE TABLE IF NOT EXISTS models (
  id                    uuid PRIMARY KEY,
  slug                  text NOT NULL,
  name                  text NOT NULL,
  provider_type         text NOT NULL,
  adapter               text NOT NULL,
  base_url              text NOT NULL DEFAULT '',
  model_key             text NOT NULL,
  credential_mode       text NOT NULL,
  secret_id             uuid REFERENCES secrets(id),
  credential_kind_code  text,
  config                jsonb NOT NULL DEFAULT '{}',
  status                text NOT NULL DEFAULT 'active',
  created_by            uuid REFERENCES users(id),
  created_at            timestamptz NOT NULL,
  updated_at            timestamptz NOT NULL,
  deleted_at            timestamptz,
  CONSTRAINT chk_models_credential_mode CHECK (
    (credential_mode = 'inline_secret' AND credential_kind_code IS NULL)
    OR (credential_mode = 'credential_ref'
        AND secret_id IS NULL
        AND credential_kind_code IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_models_slug_active
  ON models(slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_models_status_active
  ON models(status)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_models_created_by_active
  ON models(created_by)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  models IS 'Organization-level shared LLM model catalog';
COMMENT ON COLUMN models.id                   IS 'Model primary key';
COMMENT ON COLUMN models.slug                 IS 'Machine-readable stable identifier (auto-generated, globally unique)';
COMMENT ON COLUMN models.name                 IS 'Model display name';
COMMENT ON COLUMN models.provider_type        IS 'Provider type: anthropic / openai / ...';
COMMENT ON COLUMN models.adapter              IS 'opencode SDK adapter package name (@ai-sdk/anthropic, ...)';
COMMENT ON COLUMN models.base_url             IS 'API base URL';
COMMENT ON COLUMN models.model_key            IS 'Provider-side model ID';
COMMENT ON COLUMN models.credential_mode      IS 'Credential mode: inline_secret (shared) / credential_ref (user-private)';
COMMENT ON COLUMN models.secret_id            IS 'Shared secret bound under inline_secret mode; NULL = not yet configured';
COMMENT ON COLUMN models.credential_kind_code IS 'credential_kinds.code bound under credential_ref mode';
COMMENT ON COLUMN models.config               IS 'Model config: capabilities/limits/headers/modalities/options/etc.';
COMMENT ON COLUMN models.status               IS 'Enabled state: active / disabled';
COMMENT ON COLUMN models.created_by           IS 'Created by (used for edit/delete permission checks)';
COMMENT ON COLUMN models.deleted_at           IS 'Soft-delete marker';
COMMENT ON INDEX  uk_models_slug_active        IS 'Model slug unique among active rows';
COMMENT ON INDEX  idx_models_status_active     IS 'Filter models by status';
COMMENT ON INDEX  idx_models_created_by_active IS 'Reverse-lookup models by creator';


-- ============================================================
-- Table: runtimes
-- ============================================================
CREATE TABLE IF NOT EXISTS runtimes (
  id                       uuid PRIMARY KEY,
  workspace_id             uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type                     text NOT NULL,
  name                     text NOT NULL,
  provider                 text NOT NULL,
  owner_user_id            uuid REFERENCES users(id) ON DELETE SET NULL,
  version                  text NOT NULL DEFAULT '',
  hostname                 text NOT NULL DEFAULT '',
  config                   jsonb NOT NULL DEFAULT '{}'::jsonb,
  pairing_token_hash       text,
  pairing_token_expires_at timestamptz,
  liveness                 text NOT NULL DEFAULT 'pending_pairing',
  last_heartbeat_at        timestamptz,
  created_at               timestamptz NOT NULL,
  updated_at               timestamptz NOT NULL,
  deleted_at               timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_runtimes_workspace_name_active
  ON runtimes(workspace_id, name)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_runtimes_workspace_type_liveness
  ON runtimes(workspace_id, type, liveness)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_runtimes_online_heartbeat
  ON runtimes(last_heartbeat_at)
  WHERE deleted_at IS NULL
    AND liveness = 'online';

CREATE INDEX IF NOT EXISTS idx_runtimes_pending_pairing
  ON runtimes(pairing_token_hash)
  WHERE deleted_at IS NULL
    AND liveness = 'pending_pairing'
    AND pairing_token_hash IS NOT NULL;

COMMENT ON TABLE  runtimes IS 'Agent runtime registry';
COMMENT ON COLUMN runtimes.id                       IS 'Runtime primary key, generated on the server side';
COMMENT ON COLUMN runtimes.workspace_id             IS 'Owning workspace';
COMMENT ON COLUMN runtimes.type                     IS 'Runtime type: local = user local Runner / sandbox = E2B or other remote sandbox / external = external HTTP Agent';
COMMENT ON COLUMN runtimes.name                     IS 'Runtime name';
COMMENT ON COLUMN runtimes.provider                 IS 'Runtime provider';
COMMENT ON COLUMN runtimes.owner_user_id            IS 'Owning user';
COMMENT ON COLUMN runtimes.version                  IS 'Runner version';
COMMENT ON COLUMN runtimes.hostname                 IS 'Runner hostname';
COMMENT ON COLUMN runtimes.config                   IS 'Runtime config: runner_public_key/runner_credential_hash and other runtime-state fields (formerly metadata, now merged in)';
COMMENT ON COLUMN runtimes.pairing_token_hash       IS 'Pairing token hash';
COMMENT ON COLUMN runtimes.pairing_token_expires_at IS 'Pairing token expiration time';
COMMENT ON COLUMN runtimes.liveness                 IS 'Runtime connectivity: pending_pairing / offline = no heartbeat after pairing / online = heartbeat healthy / error = runtime reported failure';
COMMENT ON COLUMN runtimes.last_heartbeat_at        IS 'Most recent heartbeat time';
COMMENT ON COLUMN runtimes.deleted_at               IS 'Soft-delete marker; non-null means deleted';
COMMENT ON INDEX  uk_runtimes_workspace_name_active  IS 'Runtime name unique among active rows in a workspace';
COMMENT ON INDEX  idx_runtimes_workspace_type_liveness IS 'Query runtimes by workspace/type/liveness';
COMMENT ON INDEX  idx_runtimes_online_heartbeat      IS 'Scan online runtime heartbeats (used by the sweeper)';
COMMENT ON INDEX  idx_runtimes_pending_pairing       IS 'Look up pending-pairing runtimes by pairing token';


-- ============================================================
-- Table: agents
-- ============================================================
CREATE TABLE IF NOT EXISTS agents (
  id             uuid PRIMARY KEY,
  workspace_id   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  name           text NOT NULL,
  slug           text NOT NULL,
  description    text NOT NULL DEFAULT '',
  connector_type text NOT NULL,
  visibility     text NOT NULL DEFAULT 'workspace',
  status         text NOT NULL DEFAULT 'active',
  config         jsonb NOT NULL DEFAULT '{}',
  runtime_id     uuid REFERENCES runtimes(id) ON DELETE SET NULL,
  created_by     uuid REFERENCES users(id),
  created_at     timestamptz NOT NULL,
  updated_at     timestamptz NOT NULL,
  deleted_at     timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_agents_workspace_slug_active
  ON agents(workspace_id, slug)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agents_feishu_app_id
  ON agents ((config->'connectors'->'feishu'->>'app_id'))
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agents_runtime_active
  ON agents(runtime_id)
  WHERE deleted_at IS NULL AND runtime_id IS NOT NULL;

COMMENT ON TABLE  agents IS 'Workspace-level Agent definition table';
COMMENT ON COLUMN agents.id             IS 'Agent ID';
COMMENT ON COLUMN agents.workspace_id   IS 'Owning workspace';
COMMENT ON COLUMN agents.name           IS 'Agent display name';
COMMENT ON COLUMN agents.slug           IS 'Agent identifier within the workspace';
COMMENT ON COLUMN agents.description    IS 'Agent description';
COMMENT ON COLUMN agents.connector_type IS 'Agent connector type';
COMMENT ON COLUMN agents.visibility     IS 'Agent visibility scope';
COMMENT ON COLUMN agents.status         IS 'Enabled state';
COMMENT ON COLUMN agents.config         IS 'Agent JSON config';
COMMENT ON COLUMN agents.runtime_id     IS 'Explicitly bound runtime; NULL = unbound (dispatch errors out and points the user to the agent settings page)';
COMMENT ON COLUMN agents.created_by     IS 'Created by';
COMMENT ON COLUMN agents.created_at     IS 'Created at';
COMMENT ON COLUMN agents.updated_at     IS 'Last updated at';
COMMENT ON COLUMN agents.deleted_at     IS 'Soft-delete timestamp; NULL = active';
COMMENT ON INDEX  uk_agents_workspace_slug_active IS 'Agent slug unique among active rows in a workspace';
COMMENT ON INDEX  idx_agents_feishu_app_id        IS 'Reverse-lookup Agent by Feishu app_id';
COMMENT ON INDEX  idx_agents_runtime_active       IS 'Reverse-lookup agent bindings by runtime (runtime detail / pre-delete in-use check)';


-- ============================================================
-- Table: capability
-- ============================================================
CREATE TABLE IF NOT EXISTS capability (
  id              uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id    uuid         NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  type            text NOT NULL,
  name            text         NOT NULL,
  description     text         NOT NULL DEFAULT '',
  tags            jsonb        NOT NULL DEFAULT '[]',
  visibility      text NOT NULL DEFAULT 'workspace',
  status          text NOT NULL DEFAULT 'active',
  creator_id      uuid         REFERENCES users(id),
  created_at      timestamptz  NOT NULL DEFAULT NOW(),
  updated_at      timestamptz  NOT NULL DEFAULT NOW(),
  deprecated_at   timestamptz,
  deleted_at      timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_capability_workspace_name_active
  ON capability(workspace_id, name)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_capability_workspace_type_active
  ON capability(workspace_id, type)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_capability_tags
  ON capability USING gin (tags);

COMMENT ON TABLE  capability IS 'Agent capability catalog';
COMMENT ON COLUMN capability.id              IS 'Capability ID';
COMMENT ON COLUMN capability.workspace_id    IS 'Owning workspace';
COMMENT ON COLUMN capability.type            IS 'Capability type: skill = opencode built-in tool script; mcp = standard MCP server';
COMMENT ON COLUMN capability.name            IS 'Capability name';
COMMENT ON COLUMN capability.description     IS 'Capability description';
COMMENT ON COLUMN capability.tags            IS 'Categorization tags (jsonb string array); merged in from the capability_tag table';
COMMENT ON COLUMN capability.visibility      IS 'workspace = visible to this workspace / public = visible platform-wide. No tenant tier -- cross-workspace sharing goes through the marketplace flow (000023) rather than the visibility field';
COMMENT ON COLUMN capability.status          IS 'Capability enabled state';
COMMENT ON COLUMN capability.creator_id      IS 'Publisher';
COMMENT ON COLUMN capability.created_at      IS 'Publish time';
COMMENT ON COLUMN capability.updated_at      IS 'Last updated at';
COMMENT ON COLUMN capability.deprecated_at   IS 'Soft-deprecation timestamp';
COMMENT ON COLUMN capability.deleted_at      IS 'Soft-delete timestamp';
COMMENT ON INDEX  uk_capability_workspace_name_active   IS 'Capability name unique among active rows in a workspace';
COMMENT ON INDEX  idx_capability_workspace_type_active  IS 'Query capabilities by workspace/type';
COMMENT ON INDEX  idx_capability_tags                   IS 'jsonb containment queries by tag (GIN)';


-- ============================================================
-- Table: capability_version
-- ============================================================
CREATE TABLE IF NOT EXISTS capability_version (
  id              uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  capability_id   uuid         NOT NULL REFERENCES capability(id) ON DELETE CASCADE,
  version         text         NOT NULL,
  git_repo_url    text,
  git_ref         text,
  path            text,
  content         jsonb,
  source_payload  jsonb,
  schema_version  smallint     NOT NULL DEFAULT 1,
  canonical_spec  jsonb,
  oss_key         varchar(512) NOT NULL DEFAULT '',
  sha256          varchar(64)  NOT NULL DEFAULT '',
  required_credentials jsonb NOT NULL DEFAULT '[]'::jsonb
    CHECK (jsonb_typeof(required_credentials) = 'array'),
  creator_id    uuid         REFERENCES users(id),
  created_at    timestamptz  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_capability_version_capability_version
  ON capability_version(capability_id, version);

CREATE INDEX IF NOT EXISTS idx_capability_version_capability
  ON capability_version(capability_id);

COMMENT ON TABLE  capability_version IS 'Capability versions';
COMMENT ON COLUMN capability_version.id            IS 'Version ID';
COMMENT ON COLUMN capability_version.capability_id IS 'Owning capability';
COMMENT ON COLUMN capability_version.version       IS 'Version string';
COMMENT ON COLUMN capability_version.git_repo_url  IS 'Source repo URL';
COMMENT ON COLUMN capability_version.git_ref       IS 'git tag or commit';
COMMENT ON COLUMN capability_version.path          IS 'Path within the repo';
COMMENT ON COLUMN capability_version.content       IS 'Inline version content (per-scaffold rendered); legacy fallback path';
COMMENT ON COLUMN capability_version.source_payload IS 'Snapshot of the raw pasted content at import time; shape {"format":"json|toml|markdown","body":"…"}';
COMMENT ON COLUMN capability_version.schema_version IS 'canonical_spec schema version; = 1 starting from v1';
COMMENT ON COLUMN capability_version.canonical_spec IS 'Normalized structure after cleaning (canonical.Spec); Renderer turns it into per-scaffold rendered; falls back to content when NULL';
COMMENT ON COLUMN capability_version.oss_key IS 'Plugin type: object key within the OSS bucket; empty string for mcp/skill types';
COMMENT ON COLUMN capability_version.sha256  IS 'Plugin type: SHA-256 digest of the zip file (64-char hex); empty string for mcp/skill types';
COMMENT ON COLUMN capability_version.required_credentials IS 'Credential requirements for this version (array snapshot); each element shaped {kind, required, description}; kind matches user_credentials.kind and is validated against the code registry; corresponds to ${PARSAR_CREDENTIAL:<kind>} placeholders in this version''s content';
COMMENT ON COLUMN capability_version.creator_id    IS 'Version publisher';
COMMENT ON COLUMN capability_version.created_at    IS 'Publish time';
COMMENT ON INDEX  uk_capability_version_capability_version IS 'Version strings unique within a capability';
COMMENT ON INDEX  idx_capability_version_capability IS 'Query versions by capability';

-- ============================================================
-- Table: agent_capabilities
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_capabilities (
  id                    uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id              uuid         NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  capability_id         uuid         NOT NULL REFERENCES capability(id) ON DELETE CASCADE,
  capability_version_id uuid         NOT NULL REFERENCES capability_version(id) ON DELETE RESTRICT,
  pinning_mode          text         NOT NULL DEFAULT 'pinned'
    CHECK (pinning_mode IN ('latest', 'pinned')),
  enabled               boolean      NOT NULL DEFAULT TRUE,
  configuration         jsonb        NOT NULL DEFAULT '{}'::jsonb,
  created_at            timestamptz  NOT NULL DEFAULT NOW(),
  updated_at            timestamptz  NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_agent_capabilities_agent_capability
  ON agent_capabilities(agent_id, capability_id);

CREATE INDEX IF NOT EXISTS idx_agent_capabilities_agent_active
  ON agent_capabilities(agent_id)
  WHERE enabled = TRUE;

CREATE INDEX IF NOT EXISTS idx_agent_capabilities_version
  ON agent_capabilities(capability_version_id);

COMMENT ON TABLE  agent_capabilities IS 'Agent capability binding table';
COMMENT ON COLUMN agent_capabilities.id                    IS 'Binding record ID';
COMMENT ON COLUMN agent_capabilities.agent_id              IS 'Owning agent';
COMMENT ON COLUMN agent_capabilities.capability_id         IS 'Bound capability';
COMMENT ON COLUMN agent_capabilities.capability_version_id IS 'Pinned version; RESTRICT prevents deletion of a version still in use';
COMMENT ON COLUMN agent_capabilities.pinning_mode          IS 'latest = look up the capability''s latest version at dispatch; pinned = lock the capability_version_id column';
COMMENT ON COLUMN agent_capabilities.enabled               IS 'Binding enabled state';
COMMENT ON COLUMN agent_capabilities.configuration         IS 'Capability instance configuration';
COMMENT ON COLUMN agent_capabilities.created_at            IS 'Binding time';
COMMENT ON COLUMN agent_capabilities.updated_at            IS 'Last updated at';
COMMENT ON INDEX  uk_agent_capabilities_agent_capability IS 'Capability binding unique per Agent';
COMMENT ON INDEX  idx_agent_capabilities_agent_active IS 'Query enabled capabilities per Agent';
COMMENT ON INDEX  idx_agent_capabilities_version      IS 'Reverse-lookup capability users by version';


-- ============================================================
-- Table: agent_daemon_device_owners
-- ============================================================
-- Lease table mapping agent_daemon WebSocket devices to owner pods.
-- Sourced from server/migrations/000002. A daemon device (runtime_id) can
-- be held by only one pod at a time; generation is a fencing token, and
-- renew / release must match the current generation to prevent a stale
-- pod from overwriting a new owner's state.
CREATE TABLE IF NOT EXISTS agent_daemon_device_owners (
  device_id        uuid        PRIMARY KEY REFERENCES runtimes(id) ON DELETE CASCADE,
  workspace_id     uuid        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  owner_pod_id     text        NOT NULL,
  owner_url        text        NOT NULL DEFAULT '',
  generation       bigint      NOT NULL DEFAULT 1,
  status           text        NOT NULL DEFAULT 'connected',
  connected_at     timestamptz NOT NULL DEFAULT now(),
  last_seen_at     timestamptz NOT NULL DEFAULT now(),
  lease_expires_at timestamptz NOT NULL,
  updated_at       timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT agent_daemon_device_owners_status_check
    CHECK (status IN ('connected', 'draining', 'expired'))
);

CREATE INDEX IF NOT EXISTS idx_agent_daemon_device_owners_owner_pod
  ON agent_daemon_device_owners(owner_pod_id);

CREATE INDEX IF NOT EXISTS idx_agent_daemon_device_owners_lease
  ON agent_daemon_device_owners(lease_expires_at);

COMMENT ON TABLE  agent_daemon_device_owners IS 'Lease from agent_daemon device_id to its current WebSocket owner pod';
COMMENT ON COLUMN agent_daemon_device_owners.device_id        IS 'agent_daemon runtime/device id';
COMMENT ON COLUMN agent_daemon_device_owners.workspace_id     IS 'Owning workspace of the device';
COMMENT ON COLUMN agent_daemon_device_owners.owner_pod_id     IS 'Pod id currently holding this device''s WebSocket';
COMMENT ON COLUMN agent_daemon_device_owners.owner_url        IS 'Internally reachable URL of the current owner pod, used for cross-pod forwarding';
COMMENT ON COLUMN agent_daemon_device_owners.generation       IS 'Fencing token; incremented on every claim, renew/release must match';
COMMENT ON COLUMN agent_daemon_device_owners.status           IS 'Owner status: connected / draining / expired';
COMMENT ON COLUMN agent_daemon_device_owners.connected_at     IS 'Connection time of the current generation';
COMMENT ON COLUMN agent_daemon_device_owners.last_seen_at     IS 'Most recent renew/heartbeat time from the current owner';
COMMENT ON COLUMN agent_daemon_device_owners.lease_expires_at IS 'Owner lease expiration time';
COMMENT ON INDEX  idx_agent_daemon_device_owners_owner_pod IS 'List all devices held by a pod (for orphan cleanup)';
COMMENT ON INDEX  idx_agent_daemon_device_owners_lease     IS 'Scan expired owners by lease expiration time';


-- ============================================================
-- Table: sandboxes
-- ============================================================
CREATE TABLE IF NOT EXISTS sandboxes (
  id                             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id                   uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_id                       uuid REFERENCES agents(id) ON DELETE CASCADE,
  name                           text,
  cache_key                      text,
  sandbox_id                     text NOT NULL UNIQUE,
  template_id                    text NOT NULL,
  lifecycle_status               text NOT NULL DEFAULT 'running' CHECK (
    lifecycle_status IN (
      'spawning', 'running', 'renewing', 'killing', 'killed', 'killed_orphaned', 'killed_error'
    )
  ),
  allocation_status              text NOT NULL DEFAULT 'bound' CHECK (
    allocation_status IN ('pooled', 'bound', 'released')
  ),
  timeout_seconds                int NOT NULL DEFAULT 3600 CHECK (timeout_seconds > 0),
  auto_renew_threshold_seconds   int NOT NULL DEFAULT 0 CHECK (auto_renew_threshold_seconds >= 0),
  expires_at                     timestamptz,
  last_renewed_at                timestamptz,
  metadata                       jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at                     timestamptz NOT NULL DEFAULT now(),
  last_active_at                 timestamptz NOT NULL DEFAULT now(),
  killed_at                      timestamptz,
  CONSTRAINT sandboxes_bound_shape_check CHECK (
    allocation_status <> 'bound' OR (agent_id IS NOT NULL AND cache_key IS NOT NULL)
  )
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_sandboxes_active_per_agent
  ON sandboxes(workspace_id, agent_id)
  WHERE allocation_status = 'bound'
    AND agent_id IS NOT NULL
    AND killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_workspace_pool_available
  ON sandboxes(workspace_id, template_id, created_at)
  WHERE allocation_status = 'pooled'
    AND lifecycle_status = 'running'
    AND killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_auto_renew_scan
  ON sandboxes(expires_at)
  WHERE killed_at IS NULL
    AND auto_renew_threshold_seconds > 0;

CREATE INDEX IF NOT EXISTS idx_sandboxes_workspace_active
  ON sandboxes(workspace_id, last_active_at DESC)
  WHERE killed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_sandboxes_created_at_brin
  ON sandboxes USING brin (created_at);

COMMENT ON TABLE  sandboxes IS 'Unified sandbox instance table. Records every provider sandbox (e.g. E2B) inside a workspace: pre-warmed pool sandboxes (allocation_status=pooled), sandboxes already bound to an agent (bound), and historically terminated rows (released). Does not store sensitive runtime info such as envd_access_token / endpoint URL; those live only in process memory.';
COMMENT ON COLUMN sandboxes.id                             IS 'Sandbox instance primary key';
COMMENT ON COLUMN sandboxes.workspace_id                   IS 'Owning workspace; the pre-warm pool is also isolated per workspace to prevent credential / usage / permission bleed across tenants';
COMMENT ON COLUMN sandboxes.agent_id                       IS 'Bound agent; empty on pooled rows, required on bound rows';
COMMENT ON COLUMN sandboxes.name                           IS 'Human-readable sandbox name (nullable), mainly for admin UI display';
COMMENT ON COLUMN sandboxes.cache_key                      IS 'Aligned with the connector''s buildPoolKey output; empty on pooled rows, required on bound rows';
COMMENT ON COLUMN sandboxes.sandbox_id                     IS 'True sandbox ID from the backend provider (E2B etc.), globally unique';
COMMENT ON COLUMN sandboxes.template_id                    IS 'Template ID (E2B template_id) determining the sandbox image';
COMMENT ON COLUMN sandboxes.lifecycle_status               IS 'Lifecycle: spawning = creating / running = usable / renewing = renewing / killing = terminating / killed = terminated normally / killed_orphaned = cleaned up by startup scan / killed_error = terminated abnormally';
COMMENT ON COLUMN sandboxes.allocation_status              IS 'Allocation state: pooled = pre-warmed for workspace, claimable / bound = bound to an agent / released = historically released row';
COMMENT ON COLUMN sandboxes.timeout_seconds                IS 'Sandbox renewal seconds; after Renew the provider lifecycle extends to now + timeout_seconds';
COMMENT ON COLUMN sandboxes.auto_renew_threshold_seconds   IS 'Auto-renew threshold: 0 = disabled; >0 = auto-renew when the remaining lifetime drops below this many seconds';
COMMENT ON COLUMN sandboxes.expires_at                     IS 'Current provider-side lifecycle expiration; auto-renew scan depends on this column';
COMMENT ON COLUMN sandboxes.last_renewed_at                IS 'Most recent successful renew or claim handoff time';
COMMENT ON COLUMN sandboxes.metadata                       IS 'Audit context (spawn run_id, E2B metadata, kill reason, source=pool/fresh, etc.); not part of queries';
COMMENT ON COLUMN sandboxes.created_at                     IS 'Sandbox instance creation time';
COMMENT ON COLUMN sandboxes.last_active_at                 IS 'Most recent use / state-update time (idle TTL and admin list depend on this column)';
COMMENT ON COLUMN sandboxes.killed_at                      IS 'Termination time; non-null means the sandbox is unusable and kept only for history';
COMMENT ON CONSTRAINT sandboxes_bound_shape_check ON sandboxes IS 'Bound rows must carry agent_id and cache_key; pooled/released may be empty';
COMMENT ON INDEX  uk_sandboxes_active_per_agent            IS 'Within one workspace, each agent has at most one non-killed bound sandbox at a time';
COMMENT ON INDEX  idx_sandboxes_workspace_pool_available   IS 'Workspace-scoped pool claim query: scans only running + pooled + non-killed pre-warmed sandboxes';
COMMENT ON INDEX  idx_sandboxes_auto_renew_scan            IS 'Auto-renew scan: only indexes non-killed sandboxes with auto-renew enabled';
COMMENT ON INDEX  idx_sandboxes_workspace_active           IS 'Admin UI lists currently active sandboxes within a workspace, sorted by most recent activity';
COMMENT ON INDEX  idx_sandboxes_created_at_brin            IS 'BRIN index for temporal scans by created_at; historical rows keep accumulating, and BRIN uses much less space than btree';


-- ============================================================
-- Table: conversations
-- ============================================================
CREATE TABLE IF NOT EXISTS conversations (
  id                 uuid PRIMARY KEY,
  workspace_id       uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  surface            text NOT NULL DEFAULT 'web',
  form               text NOT NULL DEFAULT 'thread',
  title              text NOT NULL DEFAULT '',
  platform           text NOT NULL DEFAULT '',
  external_id        text NOT NULL DEFAULT '',
  external_thread_id text NOT NULL DEFAULT '',
  source_app_id      text NOT NULL DEFAULT '',
  status             text NOT NULL DEFAULT 'active',
  metadata           jsonb NOT NULL DEFAULT '{}',
  created_at         timestamptz NOT NULL,
  updated_at         timestamptz NOT NULL,
  deleted_at         timestamptz
);

CREATE INDEX IF NOT EXISTS idx_conversations_workspace_active
  ON conversations(workspace_id)
  WHERE deleted_at IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uk_conversations_external_active
  ON conversations(workspace_id, platform, external_id, external_thread_id)
  WHERE deleted_at IS NULL
    AND platform <> ''
    AND external_id <> '';

-- ADR-004 reverse-lookup index: the Feishu credential-form submit callback
-- only carries qkey, so we need O(log n) lookup for "which conversation
-- holds this pending form". The partial WHERE squeezes the index down to
-- "currently open forms".
--
-- NB: uses the jsonb_exists(jsonb, text) function rather than the `jsonb ? text`
-- operator -- the `?` in the operator form gets parsed as a parameter
-- placeholder by JDBC / NineData, and submissions from a work-ticket / GUI
-- client throw "No value specified for parameter 1". The function form is
-- semantically identical, and business code using `?` in queries still hits
-- this index (the planner knows they are equivalent).
CREATE INDEX IF NOT EXISTS idx_conversations_pending_credential_form_qkey
  ON conversations(((metadata->'gateway_inflight'->'pending_credential_form'->>'qkey')))
  WHERE jsonb_exists(metadata->'gateway_inflight', 'pending_credential_form');

-- AskUserQuestion reverse-lookup index: the Feishu card_action callback
-- only carries request_id (embedded in the button value), so we need
-- O(log n) lookup back to the conversation. The partial WHERE squeezes
-- the index down to "currently unanswered ask cards" (usually a handful
-- across an entire shared bot). Uses jsonb_exists() rather than `?` for
-- the same reason as idx_conversations_pending_credential_form_qkey.
CREATE INDEX IF NOT EXISTS idx_conversations_prompt_for_user_choice_request_id
  ON conversations(((metadata->'gateway_inflight'->'prompt_for_user_choice'->>'request_id')))
  WHERE jsonb_exists(metadata->'gateway_inflight', 'prompt_for_user_choice');

COMMENT ON TABLE  conversations IS 'Conversation table';
COMMENT ON COLUMN conversations.id                 IS 'Conversation ID';
COMMENT ON COLUMN conversations.workspace_id       IS 'Owning workspace';
COMMENT ON COLUMN conversations.surface            IS 'Top-level entry point: web = built-in UI; im = instant messaging (see platform column for details); api = external API trigger';
COMMENT ON COLUMN conversations.form               IS 'Conversation form: thread = single-threaded (web default); group = group chat; dm = direct message; oneshot = one-shot request (api default)';
COMMENT ON COLUMN conversations.title              IS 'Conversation title';
COMMENT ON COLUMN conversations.platform           IS 'External platform identifier';
COMMENT ON COLUMN conversations.external_id        IS 'External conversation ID';
COMMENT ON COLUMN conversations.external_thread_id IS 'External thread ID';
COMMENT ON COLUMN conversations.source_app_id      IS 'Source app ID';
COMMENT ON COLUMN conversations.status             IS 'Conversation status';
COMMENT ON COLUMN conversations.metadata           IS 'Conversation metadata';
COMMENT ON COLUMN conversations.deleted_at         IS 'Soft-delete timestamp';
COMMENT ON INDEX  idx_conversations_workspace_active IS 'Query active conversations by workspace';
COMMENT ON INDEX  uk_conversations_external_active IS 'External conversation mapping unique among active rows (within a workspace)';
COMMENT ON INDEX  idx_conversations_pending_credential_form_qkey IS 'ADR-004 reverse lookup: the Feishu credential-form submit callback carries only qkey, and this index resolves the owning conversation in O(log n)';
COMMENT ON INDEX  idx_conversations_prompt_for_user_choice_request_id IS 'AskUserQuestion reverse lookup: the Feishu card_action callback carries only request_id, and this index resolves the owning conversation in O(log n)';


-- ============================================================
-- Table: messages
-- ============================================================
CREATE TABLE IF NOT EXISTS messages (
  id              uuid PRIMARY KEY,
  workspace_id    uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conversation_id uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  sender_type     text NOT NULL,
  sender_id       uuid,
  kind            text NOT NULL DEFAULT 'message',
  content_format  text NOT NULL DEFAULT 'text',
  visibility      text NOT NULL DEFAULT 'workspace',
  content         text NOT NULL DEFAULT '',
  metadata        jsonb NOT NULL DEFAULT '{}',
  created_at      timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL,
  deleted_at      timestamptz
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation_time_active
  ON messages(conversation_id, created_at ASC)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_messages_workspace_time_active
  ON messages(workspace_id, created_at DESC)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  messages IS 'Conversation timeline messages';
COMMENT ON COLUMN messages.id              IS 'Message ID';
COMMENT ON COLUMN messages.workspace_id    IS 'Owning workspace';
COMMENT ON COLUMN messages.conversation_id IS 'Owning conversation';
COMMENT ON COLUMN messages.sender_type     IS 'Sender type: user = human; agent = Agent output; system = Parsar system event; external = external IM user (unregistered)';
COMMENT ON COLUMN messages.sender_id       IS 'Corresponding user_id or agent_id; system / external may be null';
COMMENT ON COLUMN messages.kind            IS 'Message semantic kind: message = normal conversation message; artifact = artifact message; system_event = system event; error. Error source (agent/runtime/validation) lives in metadata.error.source';
COMMENT ON COLUMN messages.content_format  IS 'Message body rendering format: text = plain text; markdown; card = structured card (schema under metadata.card)';
COMMENT ON COLUMN messages.visibility      IS 'Message visibility scope';
COMMENT ON COLUMN messages.content         IS 'Message body';
COMMENT ON COLUMN messages.metadata        IS 'Message metadata';
COMMENT ON COLUMN messages.deleted_at      IS 'Soft-delete timestamp';
COMMENT ON INDEX  idx_messages_conversation_time_active IS 'Query messages by conversation over time';
COMMENT ON INDEX  idx_messages_workspace_time_active    IS 'Query messages by workspace over time';


-- ============================================================
-- Table: agent_runs
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_runs (
  id                 uuid PRIMARY KEY,
  workspace_id       uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  conversation_id    uuid NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
  trigger_message_id uuid REFERENCES messages(id),
  trigger_source     text NOT NULL DEFAULT 'message',
  trigger_channel    text NOT NULL DEFAULT 'web',
  trigger_ref_type   text NOT NULL DEFAULT '',
  trigger_ref_id     uuid,
  requested_by_type  text NOT NULL,
  requested_by_id    uuid,
  agent_id           uuid NOT NULL REFERENCES agents(id),
  connector_type     text NOT NULL,
  external_run_id    text NOT NULL DEFAULT '',
  runtime_id         uuid REFERENCES runtimes(id) ON DELETE SET NULL,
  working_directory  text NOT NULL DEFAULT '',
  status             text NOT NULL DEFAULT 'queued'
                     CHECK (status IN ('queued', 'running', 'completed', 'failed', 'cancelled', 'interrupted')),
  visibility         text NOT NULL DEFAULT 'workspace',
  output_message_id  uuid REFERENCES messages(id),
  failure_reason     text NOT NULL DEFAULT '',
  metadata           jsonb NOT NULL DEFAULT '{}',
  created_at         timestamptz NOT NULL,
  started_at         timestamptz,
  finished_at        timestamptz,
  updated_at         timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_conversation_time
  ON agent_runs(conversation_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_workspace_status
  ON agent_runs(workspace_id, status);

CREATE INDEX IF NOT EXISTS idx_agent_runs_agent_time
  ON agent_runs(agent_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_agent_runs_trigger_message
  ON agent_runs(trigger_message_id);

CREATE INDEX IF NOT EXISTS idx_agent_runs_runtime_queue
  ON agent_runs(runtime_id, status, created_at)
  WHERE runtime_id IS NOT NULL;

COMMENT ON TABLE  agent_runs IS 'Agent execution records';
COMMENT ON COLUMN agent_runs.id                 IS 'Run ID';
COMMENT ON COLUMN agent_runs.workspace_id       IS 'Owning workspace';
COMMENT ON COLUMN agent_runs.conversation_id    IS 'Owning conversation';
COMMENT ON COLUMN agent_runs.trigger_message_id IS 'Triggering message ID';
COMMENT ON COLUMN agent_runs.trigger_source     IS 'Run trigger source (WHAT): message = user message; agent = another agent; scheduled_task; webhook = external event; issue = ticket; manual = admin manual';
COMMENT ON COLUMN agent_runs.trigger_channel    IS 'Run trigger channel (HOW): web = built-in UI; im = instant messaging; api = external API; cron = scheduled; internal = system internal';
COMMENT ON COLUMN agent_runs.trigger_ref_type   IS 'Trigger source object type';
COMMENT ON COLUMN agent_runs.trigger_ref_id     IS 'Trigger source object ID';
COMMENT ON COLUMN agent_runs.requested_by_type  IS 'Requester type';
COMMENT ON COLUMN agent_runs.requested_by_id    IS 'Requester ID';
COMMENT ON COLUMN agent_runs.agent_id           IS 'Agent used for this run';
COMMENT ON COLUMN agent_runs.connector_type     IS 'Connector type snapshot';
COMMENT ON COLUMN agent_runs.external_run_id    IS 'External run ID';
COMMENT ON COLUMN agent_runs.runtime_id         IS 'Runtime carrying execution';
COMMENT ON COLUMN agent_runs.working_directory  IS 'Working-directory snapshot for this run';
COMMENT ON COLUMN agent_runs.status             IS 'Run terminal/transitional state: queued; running; completed/failed (normal terminal); cancelled (user-initiated); interrupted (system interrupt, e.g. runtime crash)';
COMMENT ON COLUMN agent_runs.visibility         IS 'Run visibility scope';
COMMENT ON COLUMN agent_runs.output_message_id  IS 'Output message ID';
COMMENT ON COLUMN agent_runs.failure_reason     IS 'Failure reason';
COMMENT ON COLUMN agent_runs.metadata           IS 'Run metadata';
COMMENT ON COLUMN agent_runs.created_at         IS 'Enqueue time';
COMMENT ON COLUMN agent_runs.started_at         IS 'Execution start time';
COMMENT ON COLUMN agent_runs.finished_at        IS 'Terminal state time';
COMMENT ON COLUMN agent_runs.updated_at         IS 'Refreshed on any field change';
COMMENT ON INDEX  idx_agent_runs_conversation_time   IS 'Query runs by conversation over time';
COMMENT ON INDEX  idx_agent_runs_workspace_status    IS 'Query runs by workspace/status';
COMMENT ON INDEX  idx_agent_runs_agent_time          IS 'Query runs by agent over time';
COMMENT ON INDEX  idx_agent_runs_trigger_message     IS 'Reverse-lookup runs by triggering message';
COMMENT ON INDEX  idx_agent_runs_runtime_queue       IS 'Query pending runs by runtime/status';


-- ============================================================
-- Table: scheduled_tasks
-- ============================================================
CREATE TABLE IF NOT EXISTS scheduled_tasks (
  id                   uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  agent_id             uuid NOT NULL REFERENCES agents(id),
  conversation_id      uuid REFERENCES conversations(id),
  name                 text NOT NULL,
  prompt               text NOT NULL,
  cron_expr            text NOT NULL,
  timezone             text NOT NULL,
  enabled              boolean NOT NULL DEFAULT true,
  feishu_chat_id       text,
  feishu_chat_name     text,
  next_run_at          timestamptz,
  last_run_at          timestamptz,
  last_run_id          uuid REFERENCES agent_runs(id),
  last_status          text NOT NULL DEFAULT '',
  consecutive_failures integer NOT NULL DEFAULT 0,
  claimed_at           timestamptz,
  claimed_by           text NOT NULL DEFAULT '',
  created_by           uuid REFERENCES users(id),
  created_at           timestamptz NOT NULL DEFAULT now(),
  updated_at           timestamptz NOT NULL DEFAULT now(),
  deleted_at           timestamptz
);

CREATE INDEX IF NOT EXISTS idx_scheduled_tasks_due
  ON scheduled_tasks (enabled, next_run_at)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  scheduled_tasks IS 'Scheduled tasks: anchored on an agent, fires one agent run in its own session when due';
COMMENT ON COLUMN scheduled_tasks.agent_id             IS 'Agent to trigger (runnable unit)';
COMMENT ON COLUMN scheduled_tasks.conversation_id      IS 'Conversation of the most recent run (NULL when created, backfilled on each dispatch)';
COMMENT ON COLUMN scheduled_tasks.cron_expr            IS 'Standard 5-field cron expression';
COMMENT ON COLUMN scheduled_tasks.timezone             IS 'IANA timezone used to interpret cron_expr';
COMMENT ON COLUMN scheduled_tasks.feishu_chat_id       IS 'Phase 2 delivery target chat; null = web only';
COMMENT ON COLUMN scheduled_tasks.next_run_at          IS 'Next-fire time computed by the scheduler (UTC)';
COMMENT ON COLUMN scheduled_tasks.last_run_id          IS 'Most recently dispatched agent_run';
COMMENT ON COLUMN scheduled_tasks.consecutive_failures IS 'Consecutive failure count; auto-disables at threshold';
COMMENT ON COLUMN scheduled_tasks.claimed_at           IS 'Multi-pod claim lease time';
COMMENT ON COLUMN scheduled_tasks.claimed_by           IS 'Multi-pod claim holder';
COMMENT ON COLUMN scheduled_tasks.created_by           IS 'Execution identity = creator';
COMMENT ON INDEX  idx_scheduled_tasks_due              IS 'Scheduler scan of due tasks';


-- ============================================================
-- Table: connector_session_bindings
-- ============================================================
CREATE TABLE IF NOT EXISTS connector_session_bindings (
  id                  bigint       GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
  conversation_id     text         NOT NULL,
  connector_type      text         NOT NULL,
  binding_key         text         NOT NULL,
  upstream_session_id text         NOT NULL,
  metadata            jsonb        NOT NULL DEFAULT '{}'::jsonb,
  created_at          timestamptz  NOT NULL DEFAULT now(),
  last_active_at      timestamptz  NOT NULL DEFAULT now(),
  CONSTRAINT uk_connector_session_bindings_conversation_connector_key
    UNIQUE (conversation_id, connector_type, binding_key)
);

CREATE INDEX IF NOT EXISTS idx_connector_session_bindings_connector_key
  ON connector_session_bindings (connector_type, binding_key);

COMMENT ON TABLE  connector_session_bindings IS 'Conversation to upstream connector session bindings';
COMMENT ON COLUMN connector_session_bindings.id                  IS 'Binding record ID';
COMMENT ON COLUMN connector_session_bindings.conversation_id     IS 'Conversation ID';
COMMENT ON COLUMN connector_session_bindings.connector_type      IS 'Connector type, e.g. opencode/claude_code/codex/http_agent';
COMMENT ON COLUMN connector_session_bindings.binding_key         IS 'Connector-private binding key, e.g. OpenCode pool_key';
COMMENT ON COLUMN connector_session_bindings.upstream_session_id IS 'Upstream agent/connector session ID';
COMMENT ON COLUMN connector_session_bindings.metadata            IS 'Connector-private binding metadata';
COMMENT ON COLUMN connector_session_bindings.created_at          IS 'Binding creation time';
COMMENT ON COLUMN connector_session_bindings.last_active_at      IS 'Most recent reuse time';
COMMENT ON INDEX  idx_connector_session_bindings_connector_key   IS 'Query session bindings by connector type and binding key';
COMMENT ON CONSTRAINT uk_connector_session_bindings_conversation_connector_key
  ON connector_session_bindings IS 'Guarantees a single upstream session per (conversation, connector type, binding key)';


-- ============================================================
-- Table: agent_run_events
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_run_events (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id uuid NOT NULL REFERENCES workspaces(id),
  agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  sequence     bigint NOT NULL,
  event_kind   text NOT NULL,
  payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
  occurred_at  timestamptz NOT NULL DEFAULT now(),
  created_at   timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT uk_agent_run_events_run_sequence UNIQUE (agent_run_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_run_seq
  ON agent_run_events(agent_run_id, sequence);

CREATE INDEX IF NOT EXISTS idx_agent_run_events_workspace_time
  ON agent_run_events(workspace_id, occurred_at DESC);

COMMENT ON TABLE  agent_run_events IS 'Streaming events for an agent_run';
COMMENT ON COLUMN agent_run_events.id           IS 'Event row primary key';
COMMENT ON COLUMN agent_run_events.workspace_id IS 'Owning workspace';
COMMENT ON COLUMN agent_run_events.agent_run_id IS 'Owning run';
COMMENT ON COLUMN agent_run_events.sequence     IS 'Sequence number within the run';
COMMENT ON COLUMN agent_run_events.event_kind   IS 'Event type';
COMMENT ON COLUMN agent_run_events.payload      IS 'Event payload';
COMMENT ON COLUMN agent_run_events.occurred_at  IS 'Event occurrence time';
COMMENT ON COLUMN agent_run_events.created_at   IS 'Persistence time';
COMMENT ON CONSTRAINT uk_agent_run_events_run_sequence ON agent_run_events IS 'Sequence unique within a run';
COMMENT ON INDEX  idx_agent_run_events_run_seq      IS 'Replay events by run/sequence';
COMMENT ON INDEX  idx_agent_run_events_workspace_time IS 'Query events by workspace over time';


-- ============================================================
-- Table: agent_run_artifacts
-- ============================================================
CREATE TABLE IF NOT EXISTS agent_run_artifacts (
  id            uuid PRIMARY KEY,
  workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_run_id  uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  name          text NOT NULL,
  medium        text NOT NULL DEFAULT 'file',
  kind          text NOT NULL DEFAULT '',
  uri           text NOT NULL DEFAULT '',
  visibility    text NOT NULL DEFAULT 'workspace',
  metadata      jsonb NOT NULL DEFAULT '{}',
  created_at    timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agent_run_artifacts_run
  ON agent_run_artifacts(agent_run_id);

CREATE INDEX IF NOT EXISTS idx_agent_run_artifacts_workspace_time
  ON agent_run_artifacts(workspace_id, created_at DESC);

COMMENT ON TABLE  agent_run_artifacts IS 'Artifacts produced by an agent_run';
COMMENT ON COLUMN agent_run_artifacts.id            IS 'Artifact ID';
COMMENT ON COLUMN agent_run_artifacts.workspace_id  IS 'Owning workspace';
COMMENT ON COLUMN agent_run_artifacts.agent_run_id  IS 'Owning run';
COMMENT ON COLUMN agent_run_artifacts.name          IS 'Artifact display name';
COMMENT ON COLUMN agent_run_artifacts.medium        IS 'Artifact medium: file = downloadable file; link = external link (URI is a full URL); inline = body inlined via metadata';
COMMENT ON COLUMN agent_run_artifacts.kind          IS 'Artifact semantic classification (free-form, no CHECK constraint): report / log / patch / pr_ref / image_thumbnail / ...; '''' = uncategorized';
COMMENT ON COLUMN agent_run_artifacts.uri           IS 'Artifact URI';
COMMENT ON COLUMN agent_run_artifacts.visibility    IS 'Artifact visibility scope';
COMMENT ON COLUMN agent_run_artifacts.metadata      IS 'Artifact metadata';
COMMENT ON COLUMN agent_run_artifacts.created_at    IS 'Artifact creation time';
COMMENT ON INDEX  idx_agent_run_artifacts_run          IS 'Query artifacts by run';
COMMENT ON INDEX  idx_agent_run_artifacts_workspace_time IS 'Query artifacts by workspace over time';


-- ============================================================
-- Table: usage_logs
-- ============================================================
CREATE TABLE IF NOT EXISTS usage_logs (
  id            uuid PRIMARY KEY,
  workspace_id  uuid NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  agent_run_id  uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
  provider      text NOT NULL DEFAULT '',
  model         text NOT NULL DEFAULT '',
  input_tokens  integer NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
  output_tokens integer NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
  cost_usd      numeric(12,6) NOT NULL DEFAULT 0 CHECK (cost_usd >= 0),
  raw           jsonb NOT NULL DEFAULT '{}',
  created_at    timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_usage_logs_workspace_time
  ON usage_logs(workspace_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_usage_logs_run_time
  ON usage_logs(agent_run_id, created_at DESC);

COMMENT ON TABLE  usage_logs IS 'LLM call usage records';
COMMENT ON COLUMN usage_logs.id            IS 'Billing record primary key';
COMMENT ON COLUMN usage_logs.workspace_id  IS 'Owning workspace';
COMMENT ON COLUMN usage_logs.agent_run_id  IS 'Associated run';
COMMENT ON COLUMN usage_logs.provider      IS 'Provider identifier';
COMMENT ON COLUMN usage_logs.model         IS 'Model key';
COMMENT ON COLUMN usage_logs.input_tokens  IS 'Input token count, non-negative';
COMMENT ON COLUMN usage_logs.output_tokens IS 'Output token count, non-negative';
COMMENT ON COLUMN usage_logs.cost_usd      IS 'Converted cost (USD)';
COMMENT ON COLUMN usage_logs.raw           IS 'Raw call metadata';
COMMENT ON COLUMN usage_logs.created_at    IS 'Record creation time';
COMMENT ON INDEX  idx_usage_logs_workspace_time IS 'Query usage by workspace/time';
COMMENT ON INDEX  idx_usage_logs_run_time     IS 'Query usage by run/time';


-- ============================================================
-- Table: audit_records
-- ============================================================
CREATE TABLE IF NOT EXISTS audit_records (
  id           bigserial    PRIMARY KEY,
  source       text         NOT NULL,
  event_type   text         NOT NULL,
  actor_type   text         NOT NULL,
  actor_id     uuid,
  target_type  text         NOT NULL DEFAULT '',
  target_id    uuid,
  workspace_id uuid         REFERENCES workspaces(id) ON DELETE CASCADE,
  payload      jsonb        NOT NULL DEFAULT '{}',
  occurred_at  timestamptz  NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_audit_records_occurred_at_brin
  ON audit_records USING BRIN (occurred_at);

CREATE INDEX IF NOT EXISTS idx_audit_records_source_event_time
  ON audit_records (source, event_type, occurred_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_records_actor_time
  ON audit_records (actor_type, actor_id, occurred_at DESC)
  WHERE actor_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_records_target_time
  ON audit_records (target_type, target_id, occurred_at DESC)
  WHERE target_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_records_workspace_time
  ON audit_records (workspace_id, occurred_at DESC)
  WHERE workspace_id IS NOT NULL;

COMMENT ON TABLE  audit_records IS 'Compliance audit stream';
COMMENT ON COLUMN audit_records.id           IS 'Auto-increment ID';
COMMENT ON COLUMN audit_records.source       IS 'Audit source classification';
COMMENT ON COLUMN audit_records.event_type   IS 'Audit event type';
COMMENT ON COLUMN audit_records.actor_type   IS 'Actor type';
COMMENT ON COLUMN audit_records.actor_id     IS 'Actor ID; may be null for system events';
COMMENT ON COLUMN audit_records.target_type  IS 'Target object type';
COMMENT ON COLUMN audit_records.target_id    IS 'Target object ID';
COMMENT ON COLUMN audit_records.workspace_id IS 'Owning workspace';
COMMENT ON COLUMN audit_records.payload      IS 'Redacted event context';
COMMENT ON COLUMN audit_records.occurred_at  IS 'Event occurrence time';
COMMENT ON INDEX  idx_audit_records_occurred_at_brin   IS 'Scan audit records by time range';
COMMENT ON INDEX  idx_audit_records_source_event_time  IS 'Query audit records by source/event/time';
COMMENT ON INDEX  idx_audit_records_actor_time         IS 'Query audit records by actor';
COMMENT ON INDEX  idx_audit_records_target_time        IS 'Query audit records by target';
COMMENT ON INDEX  idx_audit_records_workspace_time     IS 'Query audit records by workspace over time';


-- ==============================================================
-- Table: spec_fragments (from 000005_spec_memory)
-- Workspace-level spec fragments -- a flat multi-fragment structure
-- (not a file tree). Every fragment is independently editable,
-- injectable, and write-back-able (title + body + tags).
-- Three write sources are distinguished via the source field; the
-- enum lives in server/internal/specmemory/types.go (Source/*), and
-- the DB layer does not add a CHECK IN constraint so that future
-- values can be added without a new migration.
--   - User writes via the UI:  source='manual',  created_by=<userID>, agent_actor=''
--   - Agent CLI writes:        source='agent',   created_by=NULL,     agent_actor='<connector>:<agentID>'
--   - Text import:             source='import',  created_by=<userID>, agent_actor=''
-- ==============================================================
CREATE TABLE IF NOT EXISTS spec_fragments (
  id           uuid        PRIMARY KEY,
  workspace_id uuid        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
  title        text        NOT NULL,
  body         text        NOT NULL,
  tags         text[]      NOT NULL DEFAULT '{}',
  source       text        NOT NULL,
  created_by   uuid        REFERENCES users(id),
  agent_actor  text        NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL,
  updated_at   timestamptz NOT NULL,
  deleted_at   timestamptz
);

CREATE INDEX IF NOT EXISTS idx_spec_fragments_workspace_active
  ON spec_fragments(workspace_id)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_spec_fragments_tags_active
  ON spec_fragments USING GIN(tags)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  spec_fragments IS 'Workspace-level spec fragments, each independently injectable and editable';
COMMENT ON COLUMN spec_fragments.id           IS 'Internal fragment ID';
COMMENT ON COLUMN spec_fragments.workspace_id IS 'Owning workspace';
COMMENT ON COLUMN spec_fragments.title        IS 'Fragment title';
COMMENT ON COLUMN spec_fragments.body         IS 'Fragment markdown body';
COMMENT ON COLUMN spec_fragments.tags         IS 'Tag array, future use for smart injection by tag';
COMMENT ON COLUMN spec_fragments.source       IS 'Source category (manual/agent/import); values managed by specmemory.Source';
COMMENT ON COLUMN spec_fragments.created_by   IS 'Human creator user_id; NULL on agent writes';
COMMENT ON COLUMN spec_fragments.agent_actor  IS 'On agent writes, records connector:agentID; empty string on human creation';
COMMENT ON COLUMN spec_fragments.deleted_at   IS 'Soft-delete timestamp; NULL = not deleted';
COMMENT ON INDEX  idx_spec_fragments_workspace_active IS 'List non-deleted fragments by workspace';
COMMENT ON INDEX  idx_spec_fragments_tags_active      IS 'GIN index supports filtering by tag';


-- ==============================================================
-- Table: memories (from 000005_spec_memory)
-- user/workspace-level memory share a single table, distinguished
-- via scope. memory_type has 4 categories: user/feedback/workspace/
-- reference; the enum lives in server/internal/specmemory/types.go
-- (MemoryType/*), and the DB layer does not add a CHECK IN
-- constraint. Only the structural scope <-> workspace_id invariant
-- is kept to prevent data rot: when scope='user', workspace_id must
-- be NULL; when scope='workspace', it must be set.
-- conversation_id is set to NULL when the conversation is deleted;
-- memories are not cascade-deleted so audit trails survive.
-- ==============================================================
CREATE TABLE IF NOT EXISTS memories (
  id              uuid        PRIMARY KEY,
  scope           text        NOT NULL,
  user_id         uuid        NOT NULL REFERENCES users(id)    ON DELETE CASCADE,
  workspace_id    uuid        REFERENCES workspaces(id)        ON DELETE CASCADE,
  memory_type     text        NOT NULL,
  title           text        NOT NULL DEFAULT '',
  body            text        NOT NULL,
  why             text        NOT NULL DEFAULT '',
  tags            text[]      NOT NULL DEFAULT '{}',
  source          text        NOT NULL,
  agent_actor     text        NOT NULL DEFAULT '',
  conversation_id uuid        REFERENCES conversations(id)     ON DELETE SET NULL,
  created_at      timestamptz NOT NULL,
  updated_at      timestamptz NOT NULL,
  deleted_at      timestamptz,
  CONSTRAINT memories_scope_workspace_id_match_check
    CHECK ((scope = 'user'      AND workspace_id IS NULL)
        OR (scope = 'workspace' AND workspace_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_memories_user_scope_active
  ON memories(user_id, scope)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_memories_workspace_active
  ON memories(workspace_id)
  WHERE deleted_at IS NULL AND scope = 'workspace';

CREATE INDEX IF NOT EXISTS idx_memories_tags_active
  ON memories USING GIN(tags)
  WHERE deleted_at IS NULL;

COMMENT ON TABLE  memories IS 'user/workspace-level memory, self-written by agents + audited by users afterwards';
COMMENT ON COLUMN memories.id              IS 'Internal memory ID';
COMMENT ON COLUMN memories.scope           IS 'Scope (user/workspace); values managed by specmemory.Scope';
COMMENT ON COLUMN memories.user_id         IS 'Owning user; also set when scope=workspace, for dedup and audit';
COMMENT ON COLUMN memories.workspace_id    IS 'Owning workspace; NULL when scope=user, required when scope=workspace';
COMMENT ON COLUMN memories.memory_type     IS 'Type (user/feedback/workspace/reference); values managed by specmemory.MemoryType';
COMMENT ON COLUMN memories.title           IS 'Short title, optional';
COMMENT ON COLUMN memories.body            IS 'Main body';
COMMENT ON COLUMN memories.why             IS 'Recommended rationale for feedback/workspace types';
COMMENT ON COLUMN memories.tags            IS 'Tag array';
COMMENT ON COLUMN memories.source          IS 'Source (user/agent/auto-review); values managed by specmemory.Source';
COMMENT ON COLUMN memories.agent_actor     IS 'On agent writes, records connector:agentID; empty on human writes';
COMMENT ON COLUMN memories.conversation_id IS 'Associated conversation ID on agent writes; set to NULL when the conversation is deleted, memory is not cascade-deleted';
COMMENT ON COLUMN memories.deleted_at      IS 'Soft-delete timestamp; NULL = not deleted';
COMMENT ON INDEX  idx_memories_user_scope_active IS 'List non-deleted memories by user + scope';
COMMENT ON INDEX  idx_memories_workspace_active  IS 'List non-deleted workspace-scope memories by workspace';
COMMENT ON INDEX  idx_memories_tags_active       IS 'GIN index supports filtering by tag';


-- ============================================================
-- Table: gateway_sessions (from 000006_gateway_sessions)
-- Route-selection state for external Gateway conversations. In
-- group-chat / DM scenarios with the shared Feishu Bot, users switch
-- the current Agent via /select, and the selection is persisted in
-- this table.
-- ============================================================
CREATE TABLE IF NOT EXISTS gateway_sessions (
  id                 uuid        PRIMARY KEY,
  platform           text        NOT NULL,
  external_id        text        NOT NULL,
  external_thread_id text        NOT NULL DEFAULT '',
  selected_agent_id  uuid        REFERENCES agents(id) ON DELETE SET NULL,
  metadata           jsonb       NOT NULL DEFAULT '{}'::jsonb,
  created_at         timestamptz NOT NULL,
  updated_at         timestamptz NOT NULL,
  CONSTRAINT gateway_sessions_key_required_check
    CHECK (btrim(platform) <> '' AND btrim(external_id) <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_gateway_sessions_external_scope
  ON gateway_sessions(platform, external_id, external_thread_id);

CREATE INDEX IF NOT EXISTS idx_gateway_sessions_selected_agent
  ON gateway_sessions(selected_agent_id)
  WHERE selected_agent_id IS NOT NULL;

COMMENT ON TABLE  gateway_sessions IS 'Route-selection state for external Gateway conversations';
COMMENT ON COLUMN gateway_sessions.platform           IS 'External platform identifier, e.g. feishu/slack/webhook';
COMMENT ON COLUMN gateway_sessions.external_id        IS 'External conversation ID, e.g. Feishu chat_id';
COMMENT ON COLUMN gateway_sessions.external_thread_id IS 'External thread ID; empty string for chat-level selection';
COMMENT ON COLUMN gateway_sessions.selected_agent_id  IS 'Currently selected Parsar Agent';
COMMENT ON COLUMN gateway_sessions.metadata           IS 'Gateway session metadata';
COMMENT ON INDEX  uk_gateway_sessions_external_scope  IS 'Only one current selection per external conversation scope';
COMMENT ON INDEX  idx_gateway_sessions_selected_agent IS 'Reverse-lookup sessions by currently selected Agent';

-- +goose Down
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;
