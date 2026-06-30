-- name: CreateDevUser :execrows
insert into users(id, email, name, status, created_at, updated_at)
values (@id::uuid, 'admin@example.com', 'Dev Admin', 'active', @now, @now)
on conflict (email) do nothing;

-- name: GetActiveUserIDByEmail :one
select id::text from users
where email = @email and deleted_at is null;

-- name: GetUserByID :one
-- Fetch user profile by id (active or soft-deleted). Used by member
-- write paths that need to enrich the returned membership row with the
-- user's email / name / status without re-joining inside the write
-- query itself.
select
  id::text as id,
  email,
  name,
  status,
  created_at,
  updated_at
from users
where id = @id::uuid;

-- name: GetActiveUserIDByGatewaySubject :one
select u.id::text
from auth_identities ai
join users u on u.id = ai.user_id
where ai.provider = @provider
  and ai.subject = @subject
  and u.status = 'active'
  and u.deleted_at is null
limit 1;

-- name: CreateDevFeishuAuthIdentity :execrows
insert into auth_identities(id, user_id, provider, subject, metadata, created_at, updated_at)
values (@id::uuid, @user_id::uuid, 'feishu', 'ou_feishu_admin', '{"source":"dev_seed"}', @now, @now)
on conflict (provider, subject) do nothing;

-- name: CreateDevWorkspace :execrows
insert into workspaces(id, name, slug, created_by, created_at, updated_at)
values (@id::uuid, 'Demo Workspace', 'demo', @created_by::uuid, @now, @now)
on conflict (slug) do nothing;

-- name: GetActiveWorkspaceIDBySlug :one
select id::text from workspaces
where slug = @slug and deleted_at is null;

-- name: CreateDevWorkspaceMember :execrows
-- Dev seed: 永远以 active owner 身份插入。子查询里加 status<>'rejected'
-- 与新唯一索引语义对齐(rejected 行不阻塞 owner 再加入)。
insert into workspace_members(id, workspace_id, user_id, role, status, created_at, updated_at)
select @id::uuid, @workspace_id::uuid, @user_id::uuid, 'owner', 'active', @now, @now
where not exists (
  select 1 from workspace_members
  where workspace_id = @workspace_id::uuid and user_id = @user_id::uuid
    and deleted_at is null and status <> 'rejected'
);

-- name: GetWorkspaceMemberRole :one
-- RBAC 根节点:所有 RequireWorkspaceRole / requireWorkspaceMember / requireWorkspaceOwnerOrAdmin
-- 中间件最终都走这一句。必须只承认 status='active' 的成员行;
-- pending(申请中) 和 rejected(已拒) 在此处被自动锁住,无须散落 client 端检查。
select role
from workspace_members
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and deleted_at is null
  and status = 'active';

-- name: GetProjectWorkspace :one
-- 把 project_id 反查到 workspace_id,供 RBAC 中间件先把 project-scoped
-- 路由桥接到 workspace 角色检查。
select workspace_id::text
from projects
where id = @project_id::uuid
  and deleted_at is null;

-- name: GetWorkspaceSettings :one
select
  w.id::text as workspace_id
from workspaces w
where w.id = @workspace_id::uuid
  and w.deleted_at is null;

-- name: GetWorkspaceRuntimeSettings :one
-- v5: runtime_mode column dropped (per-Agent runtime now lives on
-- agents.config.runtime). This query returns workspace-scoped credential
-- info only. sandbox_agent_count is computed via CountSandboxAgentsInWorkspace.
select
  w.id::text as workspace_id,
  coalesce(w.config->>'runtime_credential_secret_id', '')::text as runtime_credential_secret_id,
  coalesce(w.config->'runtime_config', '{}'::jsonb)::jsonb as runtime_config,
  coalesce(s.metadata->>'masked', '')::text as credential_masked
from workspaces w
left join secrets s on s.id = nullif(w.config->>'runtime_credential_secret_id', '')::uuid
  and s.deleted_at is null
where w.id = @workspace_id::uuid
  and w.deleted_at is null;

-- name: CountSandboxAgentsInWorkspace :one
-- Step 5: number of active project-agent bindings in this workspace whose
-- daemon execution mode is sandbox. The execution mode now lives in
-- project_agents.config.daemon_mode rather than agents.config.runtime.
select count(*)::bigint
from project_agents pa
join agents a on a.id = pa.agent_id
  and a.workspace_id = pa.workspace_id
where pa.workspace_id = @workspace_id::uuid
  and pa.deleted_at is null
  and pa.status = 'active'
  and a.deleted_at is null
  and a.status = 'active'
  and a.connector_type = 'agent_daemon'
  and coalesce(pa.config->>'daemon_mode', '') = 'sandbox';

-- name: SetWorkspaceRuntimeCredentialSecret :exec
-- Sets the workspace's E2B (or other sandbox provider) runtime
-- credential pointer to a secret already inserted in `secrets`. The
-- caller is responsible for creating the secret first; this query
-- just flips the pointer + bumps updated_at. Overwrites any prior
-- value (the old secret row stays in `secrets` as an orphan audit
-- trail; v0.1 does not GC it).
update workspaces
   set config = jsonb_set(config, '{runtime_credential_secret_id}', to_jsonb(@secret_id::text), true),
       updated_at = @now
 where id = @workspace_id::uuid
   and deleted_at is null;

-- name: ClearWorkspaceRuntimeCredentialSecret :exec
-- Clears the workspace's runtime credential pointer. The previously
-- referenced secret row stays in `secrets` (orphan, kept for audit);
-- the workspace just loses its sandbox-provider connectivity.
update workspaces
   set config = config - 'runtime_credential_secret_id',
       updated_at = @now
 where id = @workspace_id::uuid
   and deleted_at is null;

-- name: SoftDeleteWorkspaceRuntimeCredentialSecret :exec
-- Soft-delete the secret row whose id matches @secret_id (the current
-- workspaces.config.runtime_credential_secret_id pointer). Used by
-- RegisterWorkspaceRuntimeCredential when rotating, AND by
-- ClearWorkspaceRuntimeCredentialSecret when clearing the pointer.
--
-- Now that `secrets` is org-global (no workspace_id, no (workspace_id,name)
-- unique index), the previous "free up the name slot" rationale no longer
-- applies. We soft-delete the targeted row purely as audit hygiene — the
-- workspace pointer that referenced it is being replaced/cleared, and
-- leaving the encrypted row active makes ListSecrets confusing.
--
-- The row stays in `secrets` as audit trail (only `deleted_at` is set;
-- encrypted_payload and metadata are unchanged).
update secrets
   set deleted_at = @now,
       updated_at = @now
  where id = @secret_id::uuid
    and deleted_at is null;

-- name: UpdateWorkspaceSettings :one
update workspaces
set updated_at = @now
where id = @workspace_id::uuid
  and deleted_at is null
returning id::text as workspace_id;

-- name: CreateDevProject :execrows
insert into projects(id, workspace_id, name, slug, description, status, config, created_by, created_at, updated_at)
select @id::uuid, @workspace_id::uuid, 'Demo Project', 'demo-project', 'Development fixture project', 'active', '{}', @created_by::uuid, @now, @now
where not exists (
  select 1 from projects
  where workspace_id = @workspace_id::uuid and slug = 'demo-project' and deleted_at is null
);

-- name: GetActiveProjectIDBySlug :one
select id::text from projects
where workspace_id = @workspace_id::uuid and slug = @slug and deleted_at is null;

-- name: CreateDevAgent :execrows
insert into agents(id, workspace_id, name, slug, description, connector_type, status, config, created_by, created_at, updated_at)
select @id::uuid, @workspace_id::uuid, @name, @slug, @description, 'agent_daemon', 'active', @config::jsonb, @created_by::uuid, @now, @now
where not exists (
  select 1 from agents
  where workspace_id = @workspace_id::uuid and slug = @slug and deleted_at is null
);

-- name: GetActiveAgentIDBySlug :one
select id::text from agents
where workspace_id = @workspace_id::uuid and slug = @slug and deleted_at is null;

-- name: ActiveAgentSlugExists :one
select exists(
  select 1 from agents
  where workspace_id = @workspace_id::uuid
    and slug = @slug
    and deleted_at is null
    and (@exclude_id::uuid is null or id <> @exclude_id::uuid)
);

-- name: GetAgentForUpdate :one
select id::text, workspace_id::text, name, slug, description, connector_type, visibility, status, config, created_at, updated_at
from agents
where id = @id::uuid
  and deleted_at is null
for update;

-- name: CreateAgentCRUD :one
insert into agents(id, workspace_id, name, slug, description, connector_type, visibility, status, config, created_by, created_at, updated_at)
values (@id::uuid, @workspace_id::uuid, @name, @slug, @description, @connector_type, @visibility, 'active', @config::jsonb, @created_by::uuid, @now, @now)
on conflict do nothing
returning id::text, workspace_id::text, name, slug, description, connector_type, status, config, created_at, updated_at;

-- name: CreateProjectAgentCRUD :one
insert into project_agents(id, workspace_id, project_id, agent_id, status, config, created_by, created_at, updated_at)
select @id::uuid, p.workspace_id, p.id, @agent_id::uuid, 'active', @config::jsonb, @created_by::uuid, @now, @now
from projects p
where p.id = @project_id::uuid
  and p.workspace_id = @workspace_id::uuid
  and p.status = 'active'
  and p.deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- name: UpdateAgentCRUD :one
update agents
set name = @name,
    description = @description,
    connector_type = @connector_type,
    config = @config::jsonb,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, name, slug, description, connector_type, status, config, created_at, updated_at;

-- name: SoftDeleteProjectAgentCRUD :one
update project_agents
set deleted_at = @now,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- name: UpdateProjectAgentConfig :one
update project_agents
set config = @config::jsonb,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- name: GetProjectAgentForUpdate :one
select id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at
from project_agents
where id = @id::uuid
  and deleted_at is null
for update;

-- name: CountInFlightRunsByAgent :one
-- In-flight means actively running or still queued.
select count(1)::bigint
from agent_runs r
join project_agents pa on pa.id = r.project_agent_id
where pa.agent_id = @agent_id::uuid
  and r.status in ('running', 'queued');

-- name: SoftDeleteAgentCRUD :one
update agents
set deleted_at = @now,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, name, slug, description, connector_type, status, config, created_at, updated_at;

-- B phase Feishu IM routing (docs/feishu-routing.md §3.2 + §7.2):
-- UpdateAgentVisibility is a single-column write that emits an audit
-- event in the Go wrapper carrying both old and new visibility. We
-- return the prior visibility so the wrapper can stamp from/to without
-- doing a separate SELECT.

-- name: UpdateAgentVisibility :one
with prior as (
  select id, visibility as old_visibility, workspace_id, name, slug
  from agents
  where id = @id::uuid
    and deleted_at is null
)
update agents
set visibility = @visibility,
    updated_at = @now
from prior
where agents.id = prior.id
returning
  agents.id::text,
  agents.workspace_id::text,
  agents.name,
  agents.slug,
  prior.old_visibility,
  agents.visibility as new_visibility,
  agents.updated_at;

-- name: SoftDeleteProjectAgentsByAgent :many
update project_agents
set deleted_at = @now,
    updated_at = @now
where agent_id = @agent_id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- B phase Feishu IM routing (docs/feishu-routing.md §6.4):
-- GetAgentByFeishuAppID is the inbound router's first DB lookup —
-- given the Bot App ID on the webhook envelope, find the Agent that
-- registered this Bot. Uses idx_agents_feishu_app_id (partial expression
-- index, where deleted_at is null) for O(log n). The `enabled` flag on
-- the connector config is enforced in the WHERE so a disabled Agent
-- looks the same as "no Agent registered" to upstream.

-- name: GetAgentByFeishuAppID :one
select
  a.id::text,
  a.workspace_id::text,
  w.name as workspace_name,
  a.name as agent_name,
  a.slug as agent_slug,
  a.visibility,
  a.config,
  coalesce(a.created_by::text, '')::text as created_by_user_id
from agents a
join workspaces w on w.id = a.workspace_id
where a.config->'connectors'->'feishu'->>'app_id' = @app_id::text
  and (a.config->'connectors'->'feishu'->>'enabled')::boolean = true
  and a.status = 'active'
  and a.deleted_at is null
limit 1;

-- Feishu P0 observability: summarize an Agent's per-Bot inbound/outbound
-- health from conversations.source_app_id + message metadata. This is
-- intentionally read-only and never returns secret refs or raw message bodies.

-- name: GetFeishuConnectorDiagnostics :one
with selected_agent as (
  select id, workspace_id, config
  from agents
  where id = @agent_id::uuid
    and status = 'active'
    and deleted_at is null
), connector as (
  select
    a.id,
    a.workspace_id,
    coalesce((a.config->'connectors'->'feishu'->>'enabled')::boolean, false)::boolean as enabled,
    case lower(coalesce(a.config->'connectors'->'feishu'->>'event_mode', 'webhook'))
      when 'websocket' then 'websocket'
      else 'webhook'
    end::text as event_mode,
    coalesce(a.config->'connectors'->'feishu'->>'app_id', '')::text as app_id,
    coalesce(a.config->'connectors'->'feishu'->>'app_secret_ref', '')::text as app_secret_ref,
    coalesce(a.config->'connectors'->'feishu'->>'verification_token_ref', '')::text as verification_token_ref,
    coalesce(a.config->'connectors'->'feishu'->>'encrypt_key_ref', '')::text as encrypt_key_ref,
    coalesce(a.config->'connectors'->'feishu'->>'bot_open_id', '')::text as bot_open_id
  from selected_agent a
), scoped_conversations as (
  select c.id
  from conversations c
  join connector fc on fc.workspace_id = c.workspace_id
  where fc.app_id <> ''
    and c.platform = 'feishu'
    and c.source_app_id = fc.app_id
    and c.status = 'active'
    and c.deleted_at is null
), scoped_messages as (
  select m.*
  from messages m
  join scoped_conversations c on c.id = m.conversation_id
  where m.deleted_at is null
), inbound_messages as (
  select *
  from scoped_messages
  where sender_type in ('user', 'external')
), outbound_messages as (
  -- Driver-only refactor (Phase 6): gateway_delivery_status is no
  -- longer written; delivery state is derived purely from
  -- gateway_delivered_at being set.
  select
    m.*,
    case
      when coalesce(m.metadata->>'gateway_delivered_at', '') <> '' then 'delivered'
      else 'pending'
    end as delivery_status
  from scoped_messages m
  where m.sender_type = 'agent'
    and coalesce(m.metadata->>'run_id', '') <> ''
), inflight_working as (
  -- Per-conversation in-flight retry state lives in the
  -- gateway_inflight.working slot the driver upserts on every tick
  -- (Phase 2). attempts > 0 means a transient failure is pending
  -- retry; that's what `retrying_outbound_count` reports now.
  select
    c.id as conversation_id,
    coalesce((c.metadata->'gateway_inflight'->'working'->>'attempts')::int, 0) as attempts,
    coalesce(c.metadata->'gateway_inflight'->'working'->>'last_error', '') as last_error,
    coalesce((c.metadata->'gateway_inflight'->'working'->>'updated_at')::timestamptz, c.updated_at) as last_seen_at
  from conversations c
  join scoped_conversations s on s.id = c.id
  where c.metadata->'gateway_inflight'->'working' is not null
), dead_notices as (
  -- Dead-letter notices are persisted as sender_type='system'
  -- messages tagged with metadata.kind = 'feishu_outbound_dead_letter_*'
  -- (see deadLetterKind in retry.go). One notice per (slot, run_id)
  -- pair.
  select *
  from scoped_messages
  where sender_type = 'system'
    and metadata->>'kind' like 'feishu_outbound_dead_letter_%'
), last_error as (
  -- Most recent surfaced error: prefer the dead-letter notice
  -- (operators care about permanently-failed deliveries), fall back
  -- to the live inflight slot's last_error so transient failures
  -- still show up.
  select last_error, last_error_at
  from (
    select content::text as last_error, created_at::timestamptz as last_error_at, 1 as priority
    from dead_notices
    union all
    select last_error, last_seen_at as last_error_at, 2 as priority
    from inflight_working
    where last_error <> ''
  ) errs
  order by priority asc, last_error_at desc
  limit 1
)
select
  fc.id::text as agent_id,
  fc.workspace_id::text as workspace_id,
  (fc.app_id <> ''
    or fc.app_secret_ref <> ''
    or fc.verification_token_ref <> ''
    or fc.encrypt_key_ref <> ''
    or fc.bot_open_id <> '')::boolean as configured,
  fc.enabled,
  fc.event_mode,
  (fc.app_id <> '')::boolean as app_id_set,
  (fc.app_secret_ref <> '')::boolean as app_secret_set,
  (fc.verification_token_ref <> '')::boolean as verification_token_set,
  (fc.encrypt_key_ref <> '')::boolean as encrypt_key_set,
  (fc.bot_open_id <> '')::boolean as bot_open_id_set,
  (select count(1)::int from scoped_conversations) as conversation_count,
  (select count(1)::int from inbound_messages) as inbound_message_count,
  (select count(1)::int from outbound_messages) as outbound_message_count,
  (select count(1)::int from outbound_messages where delivery_status = 'pending') as pending_outbound_count,
  (select count(1)::int from inflight_working where attempts > 0) as retrying_outbound_count,
  (select count(1)::int from dead_notices) as dead_outbound_count,
  (select count(1)::int from outbound_messages where delivery_status = 'delivered') as delivered_outbound_count,
  (select max(created_at)::timestamptz from inbound_messages) as last_inbound_at,
  (select max(created_at)::timestamptz from outbound_messages) as last_outbound_at,
  (select max(updated_at)::timestamptz from outbound_messages where delivery_status = 'delivered') as last_delivered_at,
  coalesce(last_error.last_error, '')::text as last_error,
  last_error.last_error_at
from connector fc
left join last_error on true;
-- B phase Feishu IM routing (docs/feishu-routing.md §2 OSS lazy mode):
-- CountActiveFeishuBotAgents counts how many active Agents have the
-- Feishu connector enabled. The server cmd reads this at startup
-- when PARSAR_FEISHU_OSS_SHARE_OAUTH_APP=true so it can fatal-out
-- when the lazy mode (OAuth App = Bot App) is incompatible with the
-- current data (more than one Bot Agent ⇒ scopes can't be shared).

-- name: CountActiveFeishuBotAgents :one
select count(1)::int
from agents
where (config->'connectors'->'feishu'->>'enabled')::boolean = true
  and status = 'active'
  and deleted_at is null;

-- Multi-platform IM routing (docs/feishu-routing.md §4.1):
-- FindUserByPlatformSubject resolves an inbound sender to its Parsar
-- user_id by (provider, subject). The subject is the cross-tenant stable
-- id the OAuth login flow stores in auth_identities.subject — Feishu keys
-- on union_id, Slack on its workspace user id — never a per-app local id.
-- Returns pgx.ErrNoRows for unregistered senders; the gate translates that
-- into Visibility public/tenant decisions.

-- name: FindUserByPlatformSubject :one
select u.id::text
from auth_identities ai
join users u on u.id = ai.user_id
where ai.provider = @provider::text
  and ai.subject = @subject::text
  and u.deleted_at is null
  and u.status = 'active'
limit 1;

-- name: CreateDevProjectAgent :execrows
insert into project_agents(id, workspace_id, project_id, agent_id, status, config, created_by, created_at, updated_at)
select @id::uuid, @workspace_id::uuid, @project_id::uuid, @agent_id::uuid, 'active', '{"daemon_mode":"sandbox","agent_kind":"opencode"}'::jsonb, @created_by::uuid, @now, @now
where not exists (
  select 1 from project_agents
  where project_id = @project_id::uuid and agent_id = @agent_id::uuid and deleted_at is null
);

-- name: ActivateDevProjectAgent :execrows
update project_agents
set status = 'active', updated_at = @now
where project_id = @project_id::uuid
  and agent_id = @agent_id::uuid
  and deleted_at is null
  and status <> 'active';

-- name: ConfigureDevProjectAgentConnector :one
update agents a
set connector_type = @connector_type,
    config = @agent_config::jsonb,
    updated_at = @now
from project_agents pa
where pa.id = @project_agent_id::uuid
  and pa.agent_id = a.id
  and pa.workspace_id = a.workspace_id
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
returning
  pa.id::text as project_agent_id,
  pa.project_id::text,
  a.id::text as agent_id,
  a.name,
  a.slug,
  a.connector_type,
  a.config as agent_config;

-- name: ConfigureProjectAgentProfile :one
update project_agents pa
set config = @project_agent_config::jsonb,
    updated_at = @now
from agents a
where pa.id = @project_agent_id::uuid
  and pa.agent_id = a.id
  and pa.workspace_id = a.workspace_id
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
returning
  pa.id::text as project_agent_id,
  pa.project_id::text,
  a.id::text as agent_id,
  a.name,
  a.slug,
  a.connector_type,
  a.config as agent_config,
  pa.config as project_agent_config;

-- name: GetProjectAgentWorkspace :one
select pa.workspace_id::text as workspace_id
from project_agents pa
where pa.id = @project_agent_id::uuid
  and pa.status = 'active'
  and pa.deleted_at is null;

-- name: AppendAgentRunMetadata :exec
update agent_runs
set metadata = metadata || @patch::jsonb,
    updated_at = @now
where id = @id::uuid;

-- name: CreateDevConversation :execrows
-- 2026-06-04 schema: conversations.type 拆成 surface + form 两维
-- (web/im/api × thread/group/dm/oneshot)。Dev seed 是内置 web 的群聊
-- (Demo Group),所以 surface='web', form='group'。
insert into conversations(id, workspace_id, project_id, surface, form, title, status, metadata, created_at, updated_at)
select @id::uuid, @workspace_id::uuid, @project_id::uuid, 'web', 'group', 'Demo Group', 'active', '{}', @now, @now
where not exists (
  select 1 from conversations
  where workspace_id = @workspace_id::uuid
    and project_id = @project_id::uuid
    and surface = 'web'
    and form = 'group'
    and title = 'Demo Group'
    and deleted_at is null
);

-- name: CreateProjectConversation :one
-- surface ∈ {web, im, api}; form ∈ {thread, group, dm, oneshot}。
-- 调用方负责传合法组合(e.g. surface='web' 通常配 form='thread',
-- surface='im' + form='group' 是飞书群,surface='api' + form='oneshot'
-- 是外部回调一次性触发)。
insert into conversations(id, workspace_id, project_id, surface, form, title, status, metadata, created_at, updated_at)
values (@id::uuid, @workspace_id::uuid, @project_id::uuid, @surface, @form, @title, 'active', @metadata::jsonb, @now, @now)
returning id::text, workspace_id::text, project_id::text, surface, form, title, status, metadata, created_at, updated_at;

-- name: ListProjectConversations :many
select
  c.id::text as id,
  c.workspace_id::text as workspace_id,
  c.project_id::text as project_id,
  c.surface,
  c.form,
  c.title,
  c.status,
  c.metadata,
  c.created_at,
  c.updated_at,
  coalesce((
    select count(1) from messages m
    where m.conversation_id = c.id
      and m.deleted_at is null
  ), 0)::bigint as message_count,
  (
    select m.created_at from messages m
    where m.conversation_id = c.id
      and m.deleted_at is null
    order by m.created_at desc, m.id desc
    limit 1
  ) as last_message_at,
  coalesce((
    select m.content from messages m
    where m.conversation_id = c.id
      and m.deleted_at is null
    order by m.created_at desc, m.id desc
    limit 1
  ), '')::text as last_message_preview,
  coalesce((
    select m.sender_type::text from messages m
    where m.conversation_id = c.id
      and m.deleted_at is null
    order by m.created_at desc, m.id desc
    limit 1
  ), '')::text as last_message_sender_type,
  coalesce(pa.id::text, '')::text as primary_agent_id,
  coalesce(a.name, '')::text as primary_agent_name
from conversations c
join projects p on p.id = c.project_id
left join project_agents pa
  on pa.id = nullif(c.metadata->>'primary_agent_id', '')::uuid
  and pa.deleted_at is null
  and pa.status = 'active'
left join agents a
  on a.id = pa.agent_id
  and a.deleted_at is null
  and a.status = 'active'
where c.project_id = @project_id::uuid
  and p.status = 'active'
  and p.deleted_at is null
  and c.deleted_at is null
  and (@agent_id::text = '' or c.metadata->>'primary_agent_id' = @agent_id::text)
order by coalesce((
    select m.created_at from messages m
    where m.conversation_id = c.id
      and m.deleted_at is null
    order by m.created_at desc, m.id desc
    limit 1
  ), c.created_at) desc, c.id desc
limit @item_limit;

-- name: GetProjectConversation :one
select
  c.id::text as id,
  c.workspace_id::text as workspace_id,
  c.project_id::text as project_id,
  c.surface,
  c.form,
  c.title,
  c.status,
  c.metadata,
  c.created_at,
  c.updated_at,
  coalesce(pa.id::text, '')::text as primary_agent_id,
  coalesce(a.name, '')::text as primary_agent_name
from conversations c
join projects p on p.id = c.project_id
left join project_agents pa
  on pa.id = nullif(c.metadata->>'primary_agent_id', '')::uuid
  and pa.deleted_at is null
  and pa.status = 'active'
left join agents a
  on a.id = pa.agent_id
  and a.deleted_at is null
  and a.status = 'active'
where c.id = @id::uuid
  and p.status = 'active'
  and p.deleted_at is null
  and c.deleted_at is null;

-- name: ConfigureDevConversationExternalRef :one
-- 把内置 dev seed 对话改成绑定外部 IM 会话(飞书群等):
-- surface 切到 'im',form 由调用方指定(group/dm),并写入外部三件套。
update conversations
set surface = 'im',
    form = @conversation_form,
    platform = @platform,
    external_id = @external_id,
    external_thread_id = @external_thread_id,
    metadata = metadata || @metadata::jsonb,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, platform, external_id, external_thread_id;

-- name: UpdateConversationTitle :execrows
update conversations
set title = @title,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null;

-- name: SoftDeleteConversation :execrows
-- status stays 'active' (constraint is active|archived only); deleted_at
-- is the single source of truth — every read path filters it.
update conversations
set deleted_at = @now,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null;

-- name: GetActiveConversationByTitle :one
select id::text, workspace_id::text, project_id::text
from conversations
where status = 'active'
  and deleted_at is null
  and (title = @title or external_id = @title or lower(replace(title, ' ', '-')) = lower(@title))
order by created_at asc
limit 1;

-- name: GetActiveMentionedProjectAgent :one
-- v5 (2026-05-30): connector_type comes straight from agents — the v3 hack
-- that overloaded pa.config->>'runtime' as a connector_type override is
-- gone. runtime is now per-Agent (a.config->>'runtime') and is never used
-- as a connector_type fallback.
select pa.id::text as project_agent_id, a.id::text as agent_id, a.name, a.slug, a.connector_type
from project_agents pa
join agents a on a.id = pa.agent_id
where pa.project_id = @project_id::uuid
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
  and (a.name = @mention_name or a.slug = @mention_name)
order by a.name asc
limit 1;

-- name: CreateMessage :exec
-- 2026-06-04 schema: messages.message_type 拆成 kind + content_format。
-- 普通会话消息: kind='message', content_format='text' (默认即可)。
-- 想发 markdown / card 的消息请用专门的 CreateRichMessage 或调用方覆盖。
insert into messages(
  id, workspace_id, project_id, conversation_id,
  sender_type, sender_id, kind, content_format, visibility, content, metadata,
  created_at, updated_at
)
values (@id::uuid, @workspace_id::uuid, @project_id::uuid, @conversation_id::uuid, @sender_type, @sender_id::uuid, 'message', 'text', 'project', @content, @metadata::jsonb, @now, @now);

-- name: CreateAgentRun :exec
-- 2026-06-04 schema: trigger_type 拆成 trigger_source(WHAT) + trigger_channel(HOW)。
-- 用户消息触发: trigger_source='message',trigger_channel 由调用方传入
-- (web / im / api),不再用 metadata.platform 间接区分。
insert into agent_runs(
  id, workspace_id, project_id, conversation_id,
  trigger_message_id, trigger_source, trigger_channel, requested_by_type, requested_by_id,
  project_agent_id, connector_type, status, visibility, metadata,
  created_at, updated_at
)
values (@id::uuid, @workspace_id::uuid, @project_id::uuid, @conversation_id::uuid, @trigger_message_id::uuid, 'message', @trigger_channel, 'user', @requested_by_id::uuid, @project_agent_id::uuid, @connector_type, 'queued', 'project', @metadata::jsonb, @now, @now);

-- name: CreateChildAgentRun :exec
-- 子 run 是另一个 agent 通过 hand-off 触发的:
-- trigger_source='agent', trigger_channel='internal' 永远成立。
insert into agent_runs(
  id, workspace_id, project_id, conversation_id,
  trigger_message_id, trigger_source, trigger_channel, requested_by_type, requested_by_id,
  project_agent_id, connector_type, status, visibility, metadata,
  created_at, updated_at
)
values (@id::uuid, @workspace_id::uuid, @project_id::uuid, @conversation_id::uuid, @trigger_message_id::uuid, 'agent', 'internal', 'agent', @requested_by_id::uuid, @project_agent_id::uuid, @connector_type, 'queued', 'project', @metadata::jsonb, @now, @now);

-- name: CreateUsageLog :exec
insert into usage_logs(
  id, workspace_id, project_id, agent_run_id,
  provider, model, input_tokens, output_tokens, cost_usd, raw, created_at
)
values (
  @id::uuid, @workspace_id::uuid, @project_id::uuid, @agent_run_id::uuid,
  @provider, @model, @input_tokens, @output_tokens, @cost_usd, @raw::jsonb, @now
);

-- name: GetCompletableAgentRunForUpdate :one
select
  r.id::text,
  r.workspace_id::text,
  r.project_id::text,
  r.conversation_id::text,
  r.project_agent_id::text,
  pa.agent_id::text,
  r.status,
  r.started_at
from agent_runs r
join conversations c on c.id = r.conversation_id
join projects p on p.id = r.project_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
where r.id = @id::uuid
  and r.workspace_id = c.workspace_id
  and r.workspace_id = p.workspace_id
  and r.project_id = c.project_id
  and r.project_id = pa.project_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and p.status = 'active'
  and p.deleted_at is null
  and c.status = 'active'
  and c.deleted_at is null
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
for update of r;

-- name: GetAgentRunInvocation :one
select
  r.id::text,
  r.workspace_id::text,
  r.project_id::text,
  r.conversation_id::text,
  r.project_agent_id::text,
  pa.agent_id::text,
  a.name as agent_name,
  a.slug as agent_slug,
  r.requested_by_type,
  coalesce(r.requested_by_id::text, ''::text)::text as requested_by_id,
  -- v5 (2026-05-30): connector_type comes from r.connector_type (the run row);
  -- the pa.config->>'runtime' connector override is dead.
  r.connector_type as connector_type,
  r.status,
  coalesce(m.content, ''::text)::text as trigger_message_content,
  coalesce(m.metadata, '{}'::jsonb)::jsonb as trigger_message_metadata,
  (a.config || pa.config)::jsonb as agent_config,
  pa.config::jsonb as project_agent_config,
  -- v6 (2026-06-15): explicit runtime binding on the project_agent. NULL
  -- means the user hasn't picked one yet; dispatch surfaces a setup hint
  -- in that case rather than auto-creating a sandbox runtime.
  -- v7 (2026-06-15): also empty when the bound runtime has been
  -- soft-deleted — the LEFT JOIN below filters those out so a stale
  -- pa.runtime_id pointing at a dead runtime degrades to the same
  -- "未绑定 Runtime" message instead of routing dispatch to a dead device.
  coalesce(rt.id::text, ''::text)::text as runtime_id
from agent_runs r
join conversations c on c.id = r.conversation_id
join projects p on p.id = r.project_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
join workspaces w on w.id = r.workspace_id
left join messages m on m.id = r.trigger_message_id
left join runtimes rt on rt.id = pa.runtime_id and rt.deleted_at is null
where r.id = @id::uuid
  and r.workspace_id = c.workspace_id
  and r.workspace_id = p.workspace_id
  and r.project_id = c.project_id
  and r.project_id = pa.project_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and w.id = r.workspace_id
  and (m.id is null or (m.workspace_id = r.workspace_id and m.project_id = r.project_id and m.conversation_id = r.conversation_id and m.deleted_at is null))
  and p.status = 'active'
  and p.deleted_at is null
  and c.status = 'active'
  and c.deleted_at is null
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
  and w.deleted_at is null;

-- name: ClaimNextQueuedHTTPAgentRun :one
with picked as (
  select r.id
  from agent_runs r
  join conversations c on c.id = r.conversation_id
  join projects p on p.id = r.project_id
  join project_agents pa on pa.id = r.project_agent_id
  join agents a on a.id = pa.agent_id
  where r.connector_type = 'http'
    and r.status = 'queued'
    and r.workspace_id = c.workspace_id
    and r.workspace_id = p.workspace_id
    and r.project_id = c.project_id
    and r.project_id = pa.project_id
    and r.workspace_id = pa.workspace_id
    and r.workspace_id = a.workspace_id
    and p.status = 'active'
    and p.deleted_at is null
    and c.status = 'active'
    and c.deleted_at is null
    and pa.status = 'active'
    and pa.deleted_at is null
    and a.status = 'active'
    and a.deleted_at is null
  order by r.created_at asc, r.id asc
  for update skip locked
  limit 1
)
update agent_runs r
set status = 'running',
    started_at = coalesce(r.started_at, @now),
    updated_at = @now,
    metadata = r.metadata || jsonb_build_object('claimed_by', 'http_runner_once')
from picked
where r.id = picked.id
returning r.id::text;

-- name: AgentRunExists :one
select exists(select 1 from agent_runs where id = @id::uuid);

-- name: CompleteAgentRun :exec
update agent_runs
set status = 'completed',
    output_message_id = @output_message_id::uuid,
    started_at = coalesce(started_at, @now),
    finished_at = @now,
    updated_at = @now,
    metadata = metadata || jsonb_build_object('completed_by', @completed_by::text)
where id = @id::uuid;

-- name: FailAgentRun :execrows
update agent_runs
set status = 'failed',
    finished_at = @now,
    metadata = metadata || @metadata::jsonb,
    updated_at = @now
where id = @id::uuid
  and status not in ('completed', 'cancelled');

-- name: SetAgentRunOutputMessageID :exec
-- Associate a failure-output message with its run so the conversation timeline
-- can reverse-lookup the run from the message id (same lookup pattern as
-- CompleteAgentRun, but for runs that ended via FailAgentRun and only
-- produced a system "run failed" message via SendAgentRunFailureMessage).
-- We guard on output_message_id being null to stay idempotent: re-emitting
-- the failure message must not overwrite an existing association.
update agent_runs
set output_message_id = @output_message_id::uuid,
    updated_at = @now
where id = @id::uuid
  and output_message_id is null;

-- name: RequeueFailedAgentRun :one
update agent_runs
set status = 'queued',
    started_at = null,
    finished_at = null,
    metadata = metadata || @metadata::jsonb,
    updated_at = @now
where id = @id::uuid
  and status = 'failed'
returning
  id::text,
  workspace_id::text,
  project_id::text,
  conversation_id::text,
  project_agent_id::text;

-- name: ListUsageLogsByRun :many
select
  id::text,
  workspace_id::text,
  project_id::text,
  agent_run_id::text,
  provider,
  model,
  input_tokens,
  output_tokens,
  cost_usd,
  raw,
  created_at
from usage_logs
where agent_run_id = @agent_run_id::uuid
  and project_id = @project_id::uuid
order by created_at desc, id desc
limit @item_limit;

-- name: ActiveProjectExists :one
select exists(
  select 1
  from projects
  where id = @id::uuid
    and status = 'active'
    and deleted_at is null
);

-- name: ActiveWorkspaceExists :one
select exists(
  select 1
  from workspaces
  where id = @id::uuid
    and deleted_at is null
);

-- name: GetActiveWorkspaceByID :one
select id::text as id, name, slug, visibility, created_at, updated_at
from workspaces
where id = @id::uuid
  and deleted_at is null;

-- name: GetActiveProjectByID :one
select id::text as id, workspace_id::text as workspace_id, name, slug, description, status, created_at, updated_at
from projects
where id = @id::uuid
  and status = 'active'
  and deleted_at is null;

-- name: GetActiveProjectWorkspace :one
select workspace_id::text as workspace_id
from projects
where id = @id::uuid
  and status = 'active'
  and deleted_at is null;

-- name: ActiveConversationExists :one
select exists(
  select 1
  from conversations c
  join projects p on p.id = c.project_id
  where c.id = @id::uuid
    and c.workspace_id = p.workspace_id
    and c.project_id = p.id
    and c.status = 'active'
    and c.deleted_at is null
    and p.status = 'active'
    and p.deleted_at is null
);

-- name: ListProjectEnabledAgents :many
select
  pa.id::text as project_agent_id,
  pa.project_id::text as project_id,
  a.id::text as agent_id,
  a.name,
  a.slug,
  a.description,
  a.connector_type,
  pa.status,
  pa.config,
  -- Step 5: supported connectors do not use top-level runtime for
  -- execution placement. Keep only historical non-empty runtime values
  -- for legacy rows; daemon placement lives in pa.config.daemon_mode.
  case when a.connector_type = 'agent_daemon' then '' else coalesce(nullif(a.config->>'runtime', ''), '') end::text as runtime,
  (a.config || pa.config)::jsonb as agent_config,
  a.visibility,
  coalesce(a.created_by::text, '')::text as created_by_user_id,
  coalesce(u.name, '')::text as created_by_name,
  pa.created_at as enabled_at,
  -- v6 (2026-06-15): explicit runtime binding on the project_agent so the
  -- admin list can render "Local · my-mac" / "Sandbox · prod-linux" without
  -- a second round-trip. LEFT JOIN drops soft-deleted runtimes; the
  -- coalesce-to-empty-string pattern matches every other "optional text"
  -- column here so the sqlc generator picks `string` not `*string`.
  coalesce(rt.id::text, ''::text)::text as runtime_id,
  coalesce(rt.name, ''::text)::text as runtime_name,
  coalesce(rt.type, ''::text)::text as runtime_kind,
  coalesce(rt.liveness, ''::text)::text as runtime_liveness,
  -- v7 (2026-06-16): currently-bound sandbox for this project_agent, if any.
  -- The list renders "Sandbox · <e2b-id prefix>" + live dot using these.
  -- Same `allocation_status = 'bound' AND killed_at IS NULL` predicate as
  -- GetActiveSandboxBindingForAgent (matches the partial unique index that
  -- guarantees at most one such row), so this stays consistent with the
  -- sandbox-tab on the detail page.
  coalesce(sb.sandbox_id, ''::text)::text as sandbox_external_id,
  coalesce(sb.lifecycle_status, ''::text)::text as sandbox_status
from project_agents pa
join projects p on p.id = pa.project_id
join agents a on a.id = pa.agent_id
left join users u on u.id = a.created_by and u.deleted_at is null
left join runtimes rt on rt.id = pa.runtime_id and rt.deleted_at is null
left join sandboxes sb on sb.project_agent_id = pa.id
  and sb.allocation_status = 'bound'
  and sb.killed_at is null
where pa.project_id = @project_id::uuid
  and pa.workspace_id = p.workspace_id
  and pa.workspace_id = a.workspace_id
  and p.status = 'active'
  and p.deleted_at is null
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
order by a.name asc;

-- name: ListProjectAgentsAdmin :many
select
  pa.id::text as project_agent_id,
  pa.project_id::text as project_id,
  a.id::text as agent_id,
  a.name,
  a.slug,
  a.description,
  a.connector_type,
  pa.status,
  pa.config,
  -- Step 5: supported connectors do not use top-level runtime for
  -- execution placement. Keep only historical non-empty runtime values
  -- for legacy rows; daemon placement lives in pa.config.daemon_mode.
  case when a.connector_type = 'agent_daemon' then '' else coalesce(nullif(a.config->>'runtime', ''), '') end::text as runtime,
  (a.config || pa.config)::jsonb as agent_config,
  a.visibility,
  coalesce(a.created_by::text, '')::text as created_by_user_id,
  coalesce(u.name, '')::text as created_by_name,
  pa.created_at as enabled_at,
  coalesce(rt.id::text, ''::text)::text as runtime_id,
  coalesce(rt.name, ''::text)::text as runtime_name,
  coalesce(rt.type, ''::text)::text as runtime_kind,
  coalesce(rt.liveness, ''::text)::text as runtime_liveness,
  coalesce(sb.sandbox_id, ''::text)::text as sandbox_external_id,
  coalesce(sb.lifecycle_status, ''::text)::text as sandbox_status
from project_agents pa
join projects p on p.id = pa.project_id
join agents a on a.id = pa.agent_id
left join users u on u.id = a.created_by and u.deleted_at is null
left join runtimes rt on rt.id = pa.runtime_id and rt.deleted_at is null
left join sandboxes sb on sb.project_agent_id = pa.id
  and sb.allocation_status = 'bound'
  and sb.killed_at is null
where pa.project_id = @project_id::uuid
  and pa.workspace_id = p.workspace_id
  and pa.workspace_id = a.workspace_id
  and p.status = 'active'
  and p.deleted_at is null
  and pa.deleted_at is null
  and a.deleted_at is null
order by case when pa.status = 'active' then 0 else 1 end, a.name asc;

-- name: ListConversationMessages :many
select
  m.id::text,
  m.workspace_id::text,
  m.project_id::text,
  m.conversation_id::text,
  m.sender_type,
  -- system-authored messages (e.g. scheduled-task triggers) have a NULL
  -- sender_id; coalesce so the row scans into a non-nullable string.
  coalesce(m.sender_id::text, ''::text)::text as m_sender_id,
  m.kind,
  m.content_format,
  m.content,
  m.metadata,
  m.created_at
from messages m
join conversations c on c.id = m.conversation_id
join projects p on p.id = c.project_id
where m.conversation_id = @conversation_id::uuid
  and m.workspace_id = c.workspace_id
  and m.workspace_id = p.workspace_id
  and m.project_id = c.project_id
  and m.project_id = p.id
  and m.deleted_at is null
  and c.status = 'active'
  and c.deleted_at is null
  and p.status = 'active'
  and p.deleted_at is null
order by m.created_at asc, m.id asc
limit @item_limit;

-- name: ListConversationAgentRuns :many
select
  r.id::text,
  r.workspace_id::text,
  r.project_id::text,
  r.conversation_id::text,
  coalesce(r.trigger_message_id::text, ''::text)::text as trigger_message_id,
  coalesce(r.output_message_id::text, ''::text)::text as output_message_id,
  r.project_agent_id::text,
  pa.agent_id::text,
  a.name as agent_name,
  a.slug as agent_slug,
  r.connector_type,
  r.status,
  r.metadata,
  r.created_at,
  r.started_at,
  r.finished_at
from agent_runs r
join conversations c on c.id = r.conversation_id
join projects p on p.id = r.project_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
where r.conversation_id = @conversation_id::uuid
  and r.workspace_id = c.workspace_id
  and r.workspace_id = p.workspace_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and r.project_id = c.project_id
  and r.project_id = p.id
  and r.project_id = pa.project_id
  and c.status = 'active'
  and c.deleted_at is null
  and p.status = 'active'
  and p.deleted_at is null
  and pa.deleted_at is null
  and a.deleted_at is null
order by r.created_at asc, r.id asc
limit @item_limit;

-- name: GetGatewayInboundMessageByExternalID :one
select
  m.id::text,
  m.created_at
from messages m
where m.conversation_id = @conversation_id::uuid
  and m.metadata->>'source' = 'gateway'
  and m.metadata->>'gateway' = @gateway::text
  and m.metadata->>'external_message_id' = @external_message_id::text
  and m.deleted_at is null
order by m.created_at asc
limit 1;

-- name: ListAgentRunsByTriggerMessage :many
select id::text
from agent_runs
where trigger_message_id = @trigger_message_id::uuid
order by created_at asc, id asc;

-- name: GetAgentRunForRead :one
select
  r.id::text,
  r.workspace_id::text,
  r.project_id::text,
  r.conversation_id::text,
  coalesce(r.trigger_message_id::text, ''::text)::text as trigger_message_id,
  coalesce(r.output_message_id::text, ''::text)::text as output_message_id,
  r.requested_by_type,
  coalesce(r.requested_by_id::text, ''::text)::text as requested_by_id,
  r.project_agent_id::text,
  pa.agent_id::text,
  a.name as agent_name,
  a.slug as agent_slug,
  r.connector_type,
  r.external_run_id,
  r.status,
  r.metadata,
  r.created_at,
  r.started_at,
  r.finished_at,
  r.updated_at,
  pa.config as project_agent_config,
  a.config as agent_config,
  coalesce(csb.upstream_session_id, ''::text)::text as bound_device_id,
  coalesce(csb.metadata, '{}'::jsonb) as binding_metadata,
  coalesce(rt.config, '{}'::jsonb) as runtime_config,
  coalesce(r.working_directory, ''::text)::text as working_directory,
  coalesce(r.runtime_id::text, ''::text)::text as runtime_id,
  coalesce(rt.name, ''::text)::text as runtime_name,
  coalesce(rt.type, ''::text)::text as runtime_type,
  coalesce(rt.provider, ''::text)::text as runtime_provider,
  coalesce(rt.liveness, ''::text)::text as runtime_liveness,
  coalesce(rt.hostname, ''::text)::text as runtime_hostname,
  coalesce(rt.version, ''::text)::text as runtime_version,
  rt.last_heartbeat_at
from agent_runs r
join conversations c on c.id = r.conversation_id
join projects p on p.id = r.project_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
left join connector_session_bindings csb
  on csb.conversation_id = r.conversation_id::text
  and csb.connector_type = r.connector_type
  and csb.binding_key = r.project_agent_id::text
left join runtimes rt on rt.id = r.runtime_id
  and rt.workspace_id = r.workspace_id
where r.id = @id::uuid
  and r.workspace_id = c.workspace_id
  and r.workspace_id = p.workspace_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and r.project_id = c.project_id
  and r.project_id = p.id
  and r.project_id = pa.project_id
  and c.status = 'active'
  and c.deleted_at is null
  and p.status = 'active'
  and p.deleted_at is null
  and pa.deleted_at is null
  and a.deleted_at is null;

-- name: GetOutputMessageByRunID :one
select
  m.id::text,
  m.workspace_id::text,
  m.project_id::text,
  m.conversation_id::text,
  m.sender_type,
  m.sender_id::text,
  m.kind,
  m.content_format,
  m.content,
  m.metadata,
  m.created_at
from agent_runs r
join messages m on m.id = r.output_message_id
where r.id = @run_id::uuid
  and m.workspace_id = r.workspace_id
  and m.project_id = r.project_id
  and m.conversation_id = r.conversation_id
  and m.deleted_at is null;

-- name: MarkGatewayOutboundDelivered :one
-- Stamps gateway_delivered_at on the messages row whose run the
-- inflight driver just shipped a terminal card for. The driver-only
-- refactor (Phase 5/6) deleted the rest of the gateway_outbound
-- bookkeeping (claimed_at, retry_count, delivery_id, delivery_status,
-- dead-letter status). The single delivered_at stamp is all the
-- claim filter in ClaimActiveFeishuInflightConversations LEFT-JOINs
-- to skip conversations whose terminal card already landed.
-- Idempotent: re-calling with a different delivery_id is a no-op
-- because we coalesce against the existing value.
--
-- @delivery_id is accepted for parity with older callers but is no
-- longer persisted; operators correlate Feishu side-by-side via the
-- gateway_inflight slot.
update messages
set metadata = metadata || jsonb_build_object(
  'gateway_delivered_at', coalesce(metadata->'gateway_delivered_at', to_jsonb(@now::timestamptz))
),
updated_at = @now
where id = @message_id::uuid
  and sender_type = 'agent'
  and deleted_at is null
returning id::text, metadata;

-- ============================================================
-- Inbound typing-reaction state (sharedbot Feishu, P4):
-- ============================================================
-- When the inbound webhook accepts a user message we add a "Typing"
-- emoji reaction on it (im/v1/messages/{id}/reactions) so the user
-- sees an immediate ack while the Agent thinks. The terminal outbound
-- (DoneCard / ErrorCard / NoticeCard) needs to undo that reaction
-- the moment the reply lands.
--
-- We store the (reaction_id, app_id) pair on the INBOUND message's
-- own metadata under gateway_reaction.{reaction_id,app_id,added_at}.
-- The outbound message carries metadata.in_reply_to_external_msg_id
-- to point back at the user's external_message_id; on delivered we
-- find the inbound message by (gateway,external_message_id) and pull
-- the reaction_id out for the DELETE call. Mirrors the gateway_*
-- namespace already in use on messages.metadata.
-- ============================================================

-- name: RecordFeishuInboundReaction :exec
-- Stamps the reaction_id Feishu returned onto the inbound user message
-- right after we successfully add the emoji. Fire-and-forget from the
-- caller's perspective: failure here just means the terminal path
-- won't find the id and will skip the delete, which is fine — losing
-- the typing indicator is a UX regression not a correctness issue.
update messages
set metadata = metadata || jsonb_build_object(
  'gateway_reaction', jsonb_build_object(
    'reaction_id', @reaction_id::text,
    'app_id',      @app_id::text,
    'emoji_type',  @emoji_type::text,
    'added_at',    to_jsonb(@now::timestamptz)
  )
),
updated_at = @now
where id = @message_id::uuid
  and sender_type in ('user', 'external')
  and deleted_at is null;

-- name: FindLatestFeishuInboundReactionByConversation :one
-- Looks up the most recent inbound user/external message in a given
-- conversation that still has a gateway_reaction subtree, and pulls
-- the (reaction_id, app_id, external_message_id) tuple for the
-- outbound terminal path to undo. Bounded to the gateway='feishu'
-- row to keep cross-platform conversations safe.
--
-- This is the "no producer-side change" wiring: the outbound worker
-- already knows the conversation_id from PendingOutboundMessage, so
-- it can resolve the reaction without us threading
-- in_reply_to_external_msg_id through every Agent-side sender.
--
-- The "latest" choice intentionally matches Stewardhouse's
-- per-conversation closure semantics (one in-flight typing reaction
-- at a time); the race window for two rapid-fire user messages is
-- the same as theirs and we accept it consciously.
select
  m.id::text                                                          as message_id,
  m.workspace_id::text                                                as workspace_id,
  coalesce(m.metadata->>'external_message_id', '')::text              as external_message_id,
  coalesce(m.metadata->'gateway_reaction'->>'reaction_id', '')::text  as reaction_id,
  coalesce(m.metadata->'gateway_reaction'->>'app_id', '')::text       as app_id
from messages m
where m.conversation_id = @conversation_id::uuid
  and m.metadata->>'gateway' = 'feishu'
  and m.sender_type in ('user', 'external')
  and m.metadata ? 'gateway_reaction'
  and m.deleted_at is null
order by m.created_at desc, m.id desc
limit 1;

-- name: FindFeishuInboundReactionByExternalID :one
-- Looks up the reaction_id (+ app_id for credentials) that was attached
-- to a Feishu user message by its external_message_id. Used by the
-- outbound terminal path to find what to DELETE when the reply lands.
-- Returns the bare strings, no row metadata; missing row → standard
-- pgx.ErrNoRows from sqlc which the caller treats as "nothing to undo".
select
  m.id::text                                  as message_id,
  m.workspace_id::text                        as workspace_id,
  coalesce(m.metadata->'gateway_reaction'->>'reaction_id', '')::text as reaction_id,
  coalesce(m.metadata->'gateway_reaction'->>'app_id', '')::text      as app_id
from messages m
where m.metadata->>'gateway' = 'feishu'
  and m.metadata->>'external_message_id' = @external_message_id::text
  and m.sender_type in ('user', 'external')
  and m.deleted_at is null
order by m.created_at desc
limit 1;

-- name: ClearFeishuInboundReaction :exec
-- Removes the gateway_reaction subtree once the outbound terminal has
-- successfully (or unsuccessfully) issued the DELETE call to Feishu.
-- Idempotent on repeated runs because jsonb #- on a missing key is a
-- no-op. The reaction_id itself stays in DB if the DELETE call to
-- Feishu fails — we accept a stale id in metadata over a duplicate
-- DELETE attempt that would just 404 on retry.
update messages
set metadata = metadata #- '{gateway_reaction}',
    updated_at = @now
where id = @message_id::uuid
  and sender_type in ('user', 'external')
  and deleted_at is null;

-- ============================================================
-- P2 phase (sharedbot Feishu card inflight driver):
-- ============================================================
-- The inflight driver upserts working/permission card state into
-- conversations.metadata.gateway_inflight.{slot} so the outbound
-- worker can re-PATCH the same Feishu message_id across many event
-- ticks rather than spamming new cards on every step. The slot
-- schema is documented in docs/feishu-routing.md and is also
-- enforced in code by feishuoutbound/inflight_driver.go.
--
-- These queries are intentionally targeted (no joins, single-table
-- mutations on conversations or single-table reads of agent_run_events)
-- so the driver can run thousands of ticks per minute without
-- saturating the conversation row.
-- ============================================================

-- name: ListActiveFeishuInflightConversations :many
-- The driver passes @finished_cutoff (typically now - 5m) explicitly
-- rather than expressing it as an interval literal in the query; sqlc
-- v1.31's parser stumbles on `interval '5 minutes'` so we do the
-- subtraction in Go.
--
-- Mirror of ClaimActiveFeishuInflightConversations' filter shape (see
-- ~line 1644) — kept aligned so admin/debug callers see the same
-- candidate set the live claimer would pick. In particular, runs
-- whose output_message_id row already carries gateway_delivered_at
-- (i.e. P2 driver already landed the terminal card) are filtered out
-- so the list doesn't show "stuck" conversations that are actually
-- done. The run_event_max CTE's event_kind set must also mirror the
-- claim query — driver and list have to agree on what counts as a
-- "wake-worthy" sequence number, otherwise seq_emitted can never
-- catch up to max_seq and the driver spins.
with run_event_max as (
  select agent_run_id, max(sequence)::bigint as max_seq
  from agent_run_events
  where event_kind in ('tool.call', 'message.delta', 'message.thinking', 'permission.asked', 'prompt_for_user_choice.asked', 'run.started', 'run.completed', 'run.failed')
  group by agent_run_id
)
select c.id::text                 as conversation_id,
       c.workspace_id::text       as workspace_id,
       c.project_id::text         as project_id,
       c.external_id              as external_chat_id,
       c.external_thread_id       as external_thread_id,
       c.source_app_id            as source_app_id,
       c.metadata                 as conversation_metadata,
       r.id::text                 as agent_run_id,
       r.status                   as run_status,
       r.started_at               as run_started_at,
       r.finished_at              as run_finished_at,
       coalesce(r.output_message_id::text, ''::text)::text as output_message_id,
       coalesce(rem.max_seq, 0::bigint) as max_event_sequence,
       -- Per-card Agent display name resolved via the run's
       -- project_agent binding. LEFT JOINs so a soft-deleted /
       -- detached binding doesn't drop the row entirely — the driver
       -- falls back to the brand title (FeishuCardTitle) on empty.
       coalesce(a.name, '')::text as agent_name,
       -- sender_open_id is the raw Feishu open_id of the user who
       -- triggered this run. The inflight driver consumes it to add
       -- an `<at user_id="ou_xxx">` mention to the text-message ping
       -- that follows the terminal / permission card, so the user
       -- gets a desktop / mobile push notification instead of relying
       -- on the silent interactive card landing in a busy thread.
       -- LEFT JOIN so a missing / legacy trigger row degrades to ''
       -- (the ping helper sends a plain-text message in that case
       -- rather than failing the whole tick).
       coalesce(trig.metadata->>'sender_open_id', '')::text as sender_open_id
from conversations c
join agent_runs r on r.conversation_id = c.id
left join run_event_max rem on rem.agent_run_id = r.id
left join messages m on m.id = r.output_message_id
left join messages trig on trig.id = r.trigger_message_id
left join project_agents pa on pa.id = r.project_agent_id and pa.deleted_at is null
left join agents a on a.id = pa.agent_id and a.deleted_at is null
where c.platform = 'feishu'
  and c.status = 'active'
  and c.deleted_at is null
  and c.external_id <> ''
  and r.status in ('queued', 'running', 'completed', 'failed')
  and (r.finished_at is null or r.finished_at > @finished_cutoff::timestamptz)
  and coalesce((c.metadata->'gateway_inflight'->'working'->>'seq_emitted')::bigint, 0::bigint)
      < coalesce(rem.max_seq, 0::bigint)
  and (
    r.finished_at is null
    or coalesce(m.metadata->>'gateway_delivered_at', '') = ''
  )
  -- Mirror per-run terminal-delivery filter from
  -- ClaimActiveFeishuInflightConversations. `run_ids` is the set;
  -- `run_id` is the legacy single-value shape kept readable.
  and not (
    coalesce(c.metadata->'gateway_inflight'->'terminal_delivered'->>'run_id', '') = r.id::text
    or coalesce(c.metadata->'gateway_inflight'->'terminal_delivered'->'run_ids', '[]'::jsonb) ? r.id::text
  )
order by r.created_at desc
limit @item_limit;

-- name: ClaimActiveFeishuInflightConversations :many
-- Multi-pod-safe sibling of ListActiveFeishuInflightConversations.
-- Without claim semantics, 4 server pods all SELECT the same row,
-- all enter the driver's first-send branch, and all call Feishu
-- SendMessage — the user ends up with N working cards instead of
-- one being patched in place. The optimistic-lock on the metadata
-- Upsert only guards the metadata write; it cannot un-send a
-- message Feishu already accepted.
--
-- Mirrors ClaimPendingFeishuOutbound's pattern:
--   1) WITH picked AS (SELECT ... FOR UPDATE OF c SKIP LOCKED LIMIT N)
--      — row-locks the conversation each pod sees; sibling pods
--      see disjoint batches.
--   2) UPDATE conversations ... FROM picked — stamps
--      gateway_inflight_claim subtree under metadata so subsequent
--      SELECTs (including from this pod's own next tick) see the
--      claim.
--
-- Stale claims (claim_at older than @stale_before) are recoverable:
-- a sibling pod sees them as candidate rows and overwrites the
-- subtree with its own claim_at. Same window the driver passes —
-- typically ~30s, much larger than the 1-2s tick cadence so a
-- healthy pod never loses its claim, much smaller than any
-- plausible "card stuck" tolerance.
--
-- The @claimed_by branch (`= @claimed_by`) lets a pod re-acquire
-- its OWN claim regardless of age — without this clause, a pod
-- whose tick happened to overlap @stale_before by a millisecond
-- would lose its conv to itself on the next SELECT, producing a
-- pointless metadata write (and a confusing audit trail).
--
-- Run-event filter: we include user-visible events (tool.call,
-- Run-event filter: we include user-visible events (tool.call,
-- message.delta, message.thinking, permission.asked) AND the
-- lifecycle events that the driver itself needs to react to:
--   - run.started: wake the driver the moment a run begins so it
--     sends a placeholder card and locks in the one-and-only
--     message_id for this conversation. Every subsequent tick
--     patches the same card.
--   - run.completed / run.failed: wake the driver when seq_emitted
--     has already caught up to max_seq but the terminal Done /
--     Error card patch still needs to fire. Without these in the
--     set, a run whose last user-visible event arrived before
--     completion would leave the driver stuck on "executing" and a
--     second card would get written by a downstream consumer
--     (historically the P1 outbound worker, which produced one
--     half of the "two cards per query" bug). The driver consults
--     c.RunStatus directly to decide which terminal card to render.
--
-- run.cancelled / run.requeued / run.superseded are NOT in this
-- set: those go through runtime's explicit status-flip +
-- ClearSlot path, not the card driver.
--
-- Terminal-card idempotency: once a run reaches completed/failed and
-- the driver has sent (or patched) the terminal card, it stamps
-- gateway_delivered_at onto the agent_runs.output_message_id row via
-- MarkGatewayOutboundDelivered. The LEFT JOIN below threads that
-- marker into the claim filter so a finished + delivered run stops
-- being re-picked on every tick — without it, the driver would
-- repeatedly hit the terminal branch and re-send the same Done card
-- (the 2026-06-12 sharedbot card-spam bug, the other half of the
-- "two cards per query" symptom). For runs still in-flight
-- (finished_at IS NULL) the marker check is skipped so mid-run
-- patches keep flowing.
with run_event_max as (
  select agent_run_id, max(sequence)::bigint as max_seq
  from agent_run_events
  where event_kind in ('tool.call', 'message.delta', 'message.thinking', 'permission.asked', 'prompt_for_user_choice.asked', 'run.started', 'run.completed', 'run.failed')
  group by agent_run_id
),
picked as (
  select c.id, c.platform, r.id as run_id
  from conversations c
  join agent_runs r on r.conversation_id = c.id
  -- INNER (not LEFT) join: a run is only claimable once it has >=1
  -- renderable event. Without a rem row the first-send branch below
  -- (agent_run_id <> r.id) would otherwise claim a run carrying only
  -- non-renderable lifecycle events (run.cancelled/requeued/superseded)
  -- or a freshly-queued run with no events yet, and spam an empty card.
  -- The sibling ListActive query reaches the same exclusion via its
  -- `seq_emitted < max_seq` predicate.
  join run_event_max rem on rem.agent_run_id = r.id
  left join messages m on m.id = r.output_message_id
  -- Platform predicate is parameterized (was hardcoded 'feishu') so the
  -- driver can claim any platform whose neutral Channel is registered on
  -- the worker. The worker passes only platforms it can actually deliver
  -- to, so a row is never claimed without a sink.
  where c.platform = any(@platforms::text[])
    and c.status = 'active'
    and c.deleted_at is null
    and c.external_id <> ''
    and r.status in ('queued', 'running', 'completed', 'failed')
    and (r.finished_at is null or r.finished_at > @finished_cutoff::timestamptz)
    and (
      -- Slot belongs to this run: only re-claim when there are new
      -- events past seq_emitted.
      (
        coalesce(c.metadata->'gateway_inflight'->'working'->>'agent_run_id', '') = r.id::text
        and coalesce((c.metadata->'gateway_inflight'->'working'->>'seq_emitted')::bigint, 0::bigint)
            < coalesce(rem.max_seq, 0::bigint)
      )
      -- Slot empty or owned by a previous run: claim so the driver
      -- enters first-send for this run. Without this branch the new
      -- run never gets claimed (its max_seq starts at 0 while the
      -- leftover slot still holds the previous run's high seq).
      or coalesce(c.metadata->'gateway_inflight'->'working'->>'agent_run_id', '') <> r.id::text
    )
    and (
      -- Mid-run: terminal-delivery filter doesn't apply; keep patching.
      r.finished_at is null
      -- Run finished but P2 hasn't recorded a terminal delivery yet:
      -- claim so the driver can send/patch the Done card exactly once.
      -- The OR branch is also true when output_message_id is NULL,
      -- which covers the malformed-row case (LEFT JOIN → m IS NULL).
      or coalesce(m.metadata->>'gateway_delivered_at', '') = ''
    )
    -- Per-run terminal-delivery fingerprint. Closes the gate for runs
    -- that failed before producing an output_message_id (where the
    -- messages.gateway_delivered_at marker above is unreachable).
    -- Set membership so two failed runs in the same conv both stay
    -- marked — single-value `run_id` overwrote and let the earlier
    -- run get re-claimed (prod 2026-06-22 card storm). `run_id` OR is
    -- the legacy-shape read.
    and not (
      coalesce(c.metadata->'gateway_inflight'->'terminal_delivered'->>'run_id', '') = r.id::text
      or coalesce(c.metadata->'gateway_inflight'->'terminal_delivered'->'run_ids', '[]'::jsonb) ? r.id::text
    )
    and (
      -- Retry / dead-letter gate: rows in mid-backoff get parked out
      -- of the working set until next_retry_at is reached. Empty
      -- string means "no failure yet" — the normal happy path.
      coalesce(c.metadata->'gateway_inflight'->'working'->>'next_retry_at', '') = ''
      or (c.metadata->'gateway_inflight'->'working'->>'next_retry_at')::timestamptz <= @now::timestamptz
    )
    and (
      coalesce(c.metadata->'gateway_inflight_claim'->>'claimed_at', '') = ''
      or (c.metadata->'gateway_inflight_claim'->>'claimed_at')::timestamptz
         < @stale_before::timestamptz
      or coalesce(c.metadata->'gateway_inflight_claim'->>'claimed_by', '') = @claimed_by::text
    )
  order by r.created_at desc
  limit @item_limit
  for update of c skip locked
),
claimed as (
  -- Stamp the claim once PER CONVERSATION, then fan back out to one
  -- row per run in the final SELECT. The previous shape did
  -- `update conversations c ... from picked ... returning picked.run_id`,
  -- but Postgres UPDATE...FROM updates each target row once and emits a
  -- single RETURNING row even when picked holds several runs for that
  -- conversation — so a conv with two claimable runs (e.g. two failed
  -- runs) returned only one. The dropped run never reached the
  -- terminal_delivered fingerprint and got re-claimed every tick (the
  -- other half of the prod 2026-06-22 card storm; see fingerprint note
  -- above). Updating `where c.id in (select id from picked)` keeps the
  -- claim stamp idempotent per conversation while the run fan-out moves
  -- to the join below.
  update conversations c
  set metadata = jsonb_set(
        coalesce(c.metadata, '{}'::jsonb),
        '{gateway_inflight_claim}',
        jsonb_build_object(
          'claimed_at', to_jsonb(@now::timestamptz),
          'claimed_by', @claimed_by::text
        ),
        true
      ),
      updated_at = @now::timestamptz
  where c.id in (select id from picked)
  returning c.id, c.workspace_id, c.project_id, c.external_id,
            c.external_thread_id, c.source_app_id, c.metadata
)
select claimed.id::text                 as conversation_id,
       claimed.workspace_id::text       as workspace_id,
       claimed.project_id::text         as project_id,
       claimed.external_id              as external_chat_id,
       claimed.external_thread_id       as external_thread_id,
       claimed.source_app_id            as source_app_id,
       picked.platform                  as platform,
       claimed.metadata                 as conversation_metadata,
       picked.run_id::text              as agent_run_id,
       r.status                         as run_status,
       r.started_at                     as run_started_at,
       r.finished_at                    as run_finished_at,
       coalesce(r.output_message_id::text, ''::text)::text as output_message_id,
       coalesce(rem.max_seq, 0::bigint) as max_event_sequence,
       -- Per-card Agent display name resolved via the run's
       -- project_agent binding. LEFT JOINs so a soft-deleted /
       -- detached binding doesn't drop the row entirely — the
       -- driver falls back to FeishuCardTitle on empty.
       coalesce(a.name, '')::text       as agent_name,
       -- sender_open_id mirrors ListActiveFeishuInflightConversations
       -- — captured from the inbound trigger message so the driver
       -- can build an @-mention text message that wakes the user up.
       -- Empty string when the trigger row is missing or carries no
       -- open_id (legacy fixtures, system-initiated runs); the ping
       -- helper degrades to a plain-text message.
       coalesce(trig.metadata->>'sender_open_id', '')::text as sender_open_id,
       -- tenant_key is the platform workspace id (Slack team_id, Feishu
       -- tenant_key) the inbound router stamped onto the trigger message
       -- metadata. The neutral outbound path threads it into the
       -- ReplyTarget so a multi-workspace Slack channel resolves the
       -- per-team bot token at send time. Feishu ignores it (its token
       -- comes from the transport-injected cache, unchanged). Empty when
       -- the trigger row is missing (legacy fixtures); the resolver falls
       -- back to the static/env token.
       coalesce(trig.metadata->>'tenant_key', '')::text as tenant_key
from picked
join claimed on claimed.id = picked.id
join agent_runs r on r.id = picked.run_id
left join messages trig on trig.id = r.trigger_message_id
left join project_agents pa on pa.id = r.project_agent_id and pa.deleted_at is null
left join agents a on a.id = pa.agent_id and a.deleted_at is null
left join (
  select agent_run_id, max(sequence)::bigint as max_seq
  from agent_run_events
  -- Must mirror the event_kind set used in the picked CTE above —
  -- otherwise max_event_sequence on the returned row underflows the
  -- value the picked CTE used to filter, and the driver's
  -- seq_emitted compare gets confused. See same set ~line 1705.
  where event_kind in ('tool.call', 'message.delta', 'message.thinking', 'permission.asked', 'prompt_for_user_choice.asked', 'run.started', 'run.completed', 'run.failed')
  group by agent_run_id
) rem on rem.agent_run_id = picked.run_id;

-- name: ListAgentRunEventsAfterSeq :many
-- Pull the slice of events the driver hasn't rendered yet, in
-- sequence order. The driver folds these into a (steps[], latest
-- streaming text, latest permission request) tuple before rendering
-- the card. after_seq=0 returns everything; the unique index on
-- (agent_run_id, sequence) keeps this O(rowcount) without a sort.
--
-- Column `sequence` is a PostgreSQL reserved word in some contexts;
-- we qualify and alias it so sqlc's query parser doesn't reject the
-- statement.
select ev.sequence::bigint as seq,
       ev.event_kind,
       ev.payload,
       ev.occurred_at
from agent_run_events ev
where ev.agent_run_id = @agent_run_id::uuid
  and ev.sequence > @after_seq::bigint
order by ev.sequence asc
limit @item_limit;

-- name: UpsertConversationInflightWorkingCard :one
-- Optimistic-lock on the slot's agent_run_id so a new run in the same
-- conversation can't inherit the previous run's card. Empty
-- @expected_old_run_id is the first-send path (matches no slot or a
-- slot whose agent_run_id was cleared).
--
-- jsonb `||` concat instead of jsonb_set so the gateway_inflight
-- parent is materialised on demand (jsonb_set silently no-ops when
-- an intermediate key is missing).
update conversations
set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
      'gateway_inflight',
      coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
        'working', @payload::jsonb
      )
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
  and coalesce(metadata->'gateway_inflight'->'working'->>'agent_run_id', '') = @expected_old_run_id::text
returning id::text, (metadata->'gateway_inflight'->'working') as working_slot;

-- name: UpsertConversationInflightPermissionCard :one
-- Same optimistic-lock pattern as the working slot, but pinned on the
-- permission_request_id rather than external_msg_id — the permission
-- request id is the stable handle the upstream agent_run_events
-- payload carries, while the external_msg_id is what Feishu hands
-- back on send. The driver writes both into the slot; this query
-- protects against creating two concurrent permission slots for the
-- same request.
--
-- Same Phase 6 implementation note as the working variant: jsonb_set
-- silently no-ops when the gateway_inflight parent key is missing, so
-- we use jsonb concatenation to materialise the parent as needed.
update conversations
set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
      'gateway_inflight',
      coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
        'permission', @payload::jsonb
      )
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
  and coalesce(metadata->'gateway_inflight'->'permission'->>'permission_request_id', '') = @expected_old_request_id::text
returning id::text, (metadata->'gateway_inflight'->'permission') as permission_slot;

-- name: UpsertConversationInflightTerminalDelivered :exec
-- Append run_id to gateway_inflight.terminal_delivered.run_ids
-- (jsonb set; `- @run_id || array[@run_id]` keeps it deduped). Wipes
-- the legacy single-value `run_id` field — reads still accept it via
-- an OR, so the migration is lazy across a rolling deploy.
update conversations
set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
      'gateway_inflight',
      coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
        'terminal_delivered',
        (coalesce(metadata->'gateway_inflight'->'terminal_delivered', '{}'::jsonb)
          - 'run_id')
        || jsonb_build_object(
          'run_ids',
          (coalesce(metadata->'gateway_inflight'->'terminal_delivered'->'run_ids', '[]'::jsonb)
            - @run_id::text)
          || jsonb_build_array(@run_id::text),
          'delivered_at', to_jsonb(@now::timestamptz)
        )
      )
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null;

-- name: GetConversationInflightCards :one
-- Read both inflight slots in one shot. Either field is null when
-- the slot is empty. Used by:
--   - the driver to recover its working state after a server
--     restart (no need for in-memory cache)
--   - handleCardAction (P3) to find the conversation that owns a
--     permission_request_id without scanning the whole table
select id::text,
       workspace_id::text,
       project_id::text,
       external_id            as external_chat_id,
       external_thread_id,
       source_app_id,
       (metadata->'gateway_inflight'->'working')                 as working_slot,
       (metadata->'gateway_inflight'->'permission')              as permission_slot,
       (metadata->'gateway_inflight'->'prompt_for_user_choice')  as prompt_for_user_choice_slot
from conversations
where id = @conversation_id::uuid
  and deleted_at is null;

-- name: ClearConversationInflightSlot :exec
-- Empty @expected_agent_run_id skips the run guard; non-empty deletes
-- only when the slot's agent_run_id matches.
update conversations
set metadata = metadata #- array['gateway_inflight', sqlc.arg(slot)::text],
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
  and (
    @expected_agent_run_id::text = ''
    or coalesce(metadata->'gateway_inflight'->sqlc.arg(slot)::text->>'agent_run_id', '') = @expected_agent_run_id::text
  );

-- name: FindConversationByPermissionRequestID :one
-- P3 callback lookup: given the permission_request_id encoded into the
-- card button's `value`, find the conversation that's waiting on a
-- decision. Returns the inflight payload so the caller can route the
-- decision back to connector.SubmitPermission without re-querying.
select id::text,
       workspace_id::text,
       project_id::text,
       external_id            as external_chat_id,
       source_app_id,
       (metadata->'gateway_inflight'->'permission') as permission_slot
from conversations
where deleted_at is null
  and metadata->'gateway_inflight'->'permission'->>'permission_request_id' = @permission_request_id::text
limit 1;

-- name: ListStaleFeishuPermissionInflightCards :many
-- Returns conversations whose permission inflight slot has been
-- waiting longer than @stale_cutoff. The driver auto-expires these
-- (forces a Deny verdict + patches the card to "已超时") so the
-- agent run can resume rather than hanging indefinitely on a card
-- the user never clicked. Stewardhouse uses a 5-minute window; the
-- cutoff is passed in explicitly so the driver can experiment with
-- shorter / longer windows without a schema change.
select id::text                  as conversation_id,
       workspace_id::text        as workspace_id,
       project_id::text          as project_id,
       external_id               as external_chat_id,
       source_app_id             as source_app_id,
       (metadata->'gateway_inflight'->'permission') as permission_slot
from conversations
where deleted_at is null
  and metadata->'gateway_inflight'->'permission' is not null
  and (metadata->'gateway_inflight'->'permission'->>'updated_at')::timestamptz < @stale_cutoff::timestamptz
order by (metadata->'gateway_inflight'->'permission'->>'updated_at')::timestamptz asc
limit @item_limit;

-- name: UpsertConversationInflightPromptForUserChoiceCard :one
-- Same optimistic-lock pattern as UpsertConversationInflightPermissionCard,
-- pinned on the request_id so two pods racing the same AskUserQuestion
-- frame can't both create a slot. The daemon-side 10-minute timer is
-- the primary safety net; this guard prevents a multi-pod duplicate.
update conversations
set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
      'gateway_inflight',
      coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
        'prompt_for_user_choice', @payload::jsonb
      )
    ),
    updated_at = @now
where id = @conversation_id::uuid
  and deleted_at is null
  and coalesce(metadata->'gateway_inflight'->'prompt_for_user_choice'->>'request_id', '') = @expected_old_request_id::text
returning id::text, (metadata->'gateway_inflight'->'prompt_for_user_choice') as prompt_for_user_choice_slot;

-- name: FindConversationByPromptForUserChoiceRequestID :one
-- The card_action callback only carries the request_id we baked into
-- the button's `value`; this query resolves it to the conversation
-- owning the slot. Uses the partial expression index from migration
-- 000010 so it's O(log n) even on a busy bot.
select id::text,
       workspace_id::text,
       project_id::text,
       external_id            as external_chat_id,
       source_app_id,
       (metadata->'gateway_inflight'->'prompt_for_user_choice') as prompt_for_user_choice_slot
from conversations
where deleted_at is null
  and metadata->'gateway_inflight'->'prompt_for_user_choice'->>'request_id' = @request_id::text
limit 1;

-- name: ListStaleFeishuPromptForUserChoiceInflightCards :many
-- Server-side belt for the daemon-side 10-min watchdog: any
-- prompt_for_user_choice slot older than @stale_cutoff has either
-- already been answered (daemon timer fired, server didn't see the
-- decision yet) or the daemon went away. The outbound driver
-- auto-expires these by patching the card to "已超时" and clearing
-- the slot so the slot index doesn't leak.
select id::text                  as conversation_id,
       workspace_id::text        as workspace_id,
       project_id::text          as project_id,
       external_id               as external_chat_id,
       source_app_id             as source_app_id,
       (metadata->'gateway_inflight'->'prompt_for_user_choice') as prompt_for_user_choice_slot
from conversations
where deleted_at is null
  and metadata->'gateway_inflight'->'prompt_for_user_choice' is not null
  and (metadata->'gateway_inflight'->'prompt_for_user_choice'->>'updated_at')::timestamptz < @stale_cutoff::timestamptz
order by (metadata->'gateway_inflight'->'prompt_for_user_choice'->>'updated_at')::timestamptz asc
limit @item_limit;

-- name: ClaimPendingQueuedFeishuRuns :many
-- Multi-pod-safe replacement for the prior ListPendingQueuedFeishuRuns.
-- Without a claim, every sibling pod's tick SELECT-ed the same queued
-- runs, every pod called Feishu SendMessage, and the user saw N
-- duplicate "排队中" cards (the prod regression on 2026-06-15:
-- 4 queued runs × 2 pods → up to 8 cards, only 1 ended up with the
-- queue_card_sent_at stamp because StampQueueCardSent's last-writer-
-- wins UPDATE hid the storm). The metadata stamp is too late to
-- prevent the duplicate sends.
--
-- This claim variant mirrors ClaimActiveFeishuInflightConversations:
--   1) WITH picked AS (SELECT ... FOR UPDATE OF r SKIP LOCKED LIMIT N)
--      — row-locks the agent_runs row each pod sees; sibling pods
--      see disjoint batches.
--   2) UPDATE agent_runs ... FROM picked — stamps
--      queue_card_claim subtree under metadata so subsequent SELECTs
--      (including from this pod's own next tick) see the claim.
--
-- Stale claims (claim_at older than @stale_before) are recoverable:
-- a sibling pod sees them as candidate rows and overwrites the
-- subtree with its own claim_at. Same window the inflight driver
-- passes — typically ~30s, much larger than the 1-2s tick cadence so
-- a healthy pod never loses its claim, much smaller than any
-- plausible "send stuck" tolerance.
--
-- The @claimed_by branch (`= @claimed_by`) lets a pod re-acquire its
-- OWN claim regardless of age — without it, a pod whose tick happened
-- to overlap @stale_before by a millisecond would lose its run to
-- itself on the next SELECT, producing a pointless metadata write.
--
-- Filters (unchanged from the listing variant beyond claim):
--   - r.status = 'queued' so we don't race the inflight driver after
--     the run flips to running (the inflight slot owns the working
--     card from that point on)
--   - coalesce(...->>'queue_card_sent_at','') = '' for the
--     post-send idempotency stamp; once StampQueueCardSent fires
--     subsequent ticks skip the row entirely
--   - r.created_at > @cutoff bounds the work: a queued run older than
--     a few minutes is almost certainly stuck behind a dead inflight
--     sibling; chasing it here wouldn't help the user understand
--     anything we couldn't surface from the regular failure path
--
with picked as (
  select r.id
  from agent_runs r
  join conversations c on c.id = r.conversation_id
  where c.platform = 'feishu'
    and c.status = 'active'
    and c.deleted_at is null
    and c.external_id <> ''
    and r.status = 'queued'
    and coalesce(r.metadata->>'queue_card_sent_at', '') = ''
    and r.created_at > @cutoff::timestamptz
    and (
      coalesce(r.metadata->'queue_card_claim'->>'claimed_at', '') = ''
      or (r.metadata->'queue_card_claim'->>'claimed_at')::timestamptz < @stale_before::timestamptz
      or coalesce(r.metadata->'queue_card_claim'->>'claimed_by', '') = @claimed_by::text
    )
  order by r.created_at asc
  limit @item_limit
  for update of r skip locked
),
claimed as (
  update agent_runs r
  set metadata = coalesce(r.metadata, '{}'::jsonb)
                 || jsonb_build_object(
                      'queue_card_claim',
                      jsonb_build_object(
                        'claimed_at', to_jsonb(@now::timestamptz),
                        'claimed_by', @claimed_by::text
                      )
                    ),
      updated_at = @now::timestamptz
  from picked
  where r.id = picked.id
  returning r.id, r.workspace_id, r.project_id, r.conversation_id, r.project_agent_id
)
select claimed.id::text               as run_id,
       claimed.workspace_id::text     as workspace_id,
       claimed.project_id::text       as project_id,
       claimed.conversation_id::text  as conversation_id,
       c.external_id                  as external_chat_id,
       c.external_thread_id           as external_thread_id,
       c.source_app_id                as source_app_id,
       -- Per-card Agent display name resolved via the run's
       -- project_agent binding. LEFT JOINs so a soft-deleted /
       -- detached binding doesn't drop the row entirely — the
       -- driver falls back to FeishuCardTitle on empty.
       coalesce(a.name, '')::text    as agent_name
from claimed
join conversations c on c.id = claimed.conversation_id
left join project_agents pa on pa.id = claimed.project_agent_id and pa.deleted_at is null
left join agents a on a.id = pa.agent_id and a.deleted_at is null;

-- name: StampQueueCardSent :exec
-- Idempotency marker for ClaimPendingQueuedFeishuRuns. The queue-card
-- driver calls this after a successful Feishu send so subsequent
-- ticks won't re-send the same notice card. Uses jsonb concatenation
-- to materialise metadata when the column is null (matches the
-- pattern used by UpsertConversationInflightWorkingCard ~line 1657).
update agent_runs
set metadata = coalesce(metadata, '{}'::jsonb)
               || jsonb_build_object('queue_card_sent_at', to_jsonb(@now::timestamptz)),
    updated_at = @now::timestamptz
where id = @run_id::uuid;

-- name: ListAgentRunArtifacts :many
-- 2026-06-04 schema: artifact_type 拆成 medium(载体: file/link/inline)
-- + kind(语义: log/transcript/code-patch/screenshot/...,业务定义)。
select
  id::text,
  agent_run_id::text,
  name,
  medium,
  kind,
  uri,
  visibility,
  metadata,
  created_at
from agent_run_artifacts
where agent_run_id = @run_id::uuid
  and project_id = @project_id::uuid
order by created_at asc, id asc;

-- name: ListProjectAgentRunsPage :many
-- 2026-06-15 redesign: replaces the previous ListProjectAgentRuns +
-- ListProjectAgentRunsByStatus pair.
--   * ORDER BY ... DESC          — admin list shows newest first.
--   * (created_at, id) tie-break — keep OFFSET pagination stable when
--     multiple rows share a created_at.
--   * @statuses::text[]          — empty array = "no status filter";
--     non-empty filters via `= ANY(...)`. Lets the UI "进行中" tab union
--     {running, queued} in one round-trip.
--   * LIMIT/OFFSET                — classic pager; pair with the
--     CountProjectAgentRuns query below for the page-count.
select
  r.id::text,
  r.workspace_id::text,
  r.project_id::text,
  r.conversation_id::text,
  coalesce(r.trigger_message_id::text, ''::text)::text as trigger_message_id,
  coalesce(r.output_message_id::text, ''::text)::text as output_message_id,
  r.project_agent_id::text,
  pa.agent_id::text,
  a.name as agent_name,
  a.slug as agent_slug,
  r.connector_type,
  r.status,
  r.metadata,
  r.created_at,
  r.started_at,
  r.finished_at
from agent_runs r
join projects p on p.id = r.project_id
join conversations c on c.id = r.conversation_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
where r.project_id = @project_id::uuid
  and r.workspace_id = p.workspace_id
  and r.workspace_id = c.workspace_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and r.project_id = c.project_id
  and r.project_id = pa.project_id
  and p.status = 'active'
  and p.deleted_at is null
  and c.deleted_at is null
  and pa.deleted_at is null
  and a.deleted_at is null
  and (cardinality(@statuses::text[]) = 0
       or r.status = ANY(@statuses::text[]))
order by r.created_at desc, r.id desc
limit @item_limit offset @item_offset;

-- name: CountProjectAgentRuns :one
-- Companion to ListProjectAgentRunsPage. Returns the total row count
-- under the SAME filter so the UI can render "第 X-Y 条,共 N 条" and
-- decide when to disable the "next page" button. Joins mirror the list
-- query (same active-project guard) so counts and rows never disagree.
select count(*)::bigint as total
from agent_runs r
join projects p on p.id = r.project_id
join conversations c on c.id = r.conversation_id
join project_agents pa on pa.id = r.project_agent_id
join agents a on a.id = pa.agent_id
where r.project_id = @project_id::uuid
  and r.workspace_id = p.workspace_id
  and r.workspace_id = c.workspace_id
  and r.workspace_id = pa.workspace_id
  and r.workspace_id = a.workspace_id
  and r.project_id = c.project_id
  and r.project_id = pa.project_id
  and p.status = 'active'
  and p.deleted_at is null
  and c.deleted_at is null
  and pa.deleted_at is null
  and a.deleted_at is null
  and (cardinality(@statuses::text[]) = 0
       or r.status = ANY(@statuses::text[]));

-- name: GetProjectAgentMetrics :one
-- Aggregates a single project_agent's run history over the last
-- @window_days days for the agent-detail "近 N 天表现" panel:
--   * completed_count   — finished runs (status = 'completed')
--   * failed_count      — failed/cancelled/interrupted (for success rate)
--   * total_count       — completed + failed (excludes still-running/queued)
--   * avg_duration_ms   — mean wall-clock of completed runs only
--                         (started_at → finished_at). NULL if no
--                         completed runs in window.
-- All filtered by created_at to keep the window stable as runs finish
-- later. The active-project / workspace guards mirror ListProjectAgentRunsPage.
select
  count(*) filter (where r.status = 'completed')::bigint as completed_count,
  count(*) filter (where r.status in ('failed','cancelled','interrupted'))::bigint as failed_count,
  count(*) filter (where r.status in ('completed','failed','cancelled','interrupted'))::bigint as total_count,
  coalesce(avg(extract(epoch from (r.finished_at - r.started_at)) * 1000) filter (
    where r.status = 'completed' and r.started_at is not null and r.finished_at is not null
  ), 0)::double precision as avg_duration_ms
from agent_runs r
join projects p on p.id = r.project_id
join project_agents pa on pa.id = r.project_agent_id
where pa.id = @project_agent_id::uuid
  and r.project_id = @project_id::uuid
  and r.workspace_id = pa.workspace_id
  and r.project_id = pa.project_id
  and p.status = 'active'
  and p.deleted_at is null
  and pa.deleted_at is null
  and r.created_at >= now() - make_interval(days => @window_days::int);

-- name: ListProjectUsageLogs :many
select
  id::text,
  workspace_id::text,
  project_id::text,
  agent_run_id::text,
  provider,
  model,
  input_tokens,
  output_tokens,
  cost_usd,
  raw,
  created_at
from usage_logs
where project_id = @project_id::uuid
order by created_at desc, id desc
limit @item_limit;

-- name: ListProjectUsageLogsByRun :many
select
  id::text,
  workspace_id::text,
  project_id::text,
  agent_run_id::text,
  provider,
  model,
  input_tokens,
  output_tokens,
  cost_usd,
  raw,
  created_at
from usage_logs
where project_id = @project_id::uuid
  and agent_run_id = @agent_run_id::uuid
order by created_at desc, id desc
limit @item_limit;

-- name: CreateSecret :one
-- 组织级共享 secret. slug 由调用方传入 (用 generateAutoSlug("secret")),
-- name 是展示名(可重复).
insert into secrets(
  id, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_by, created_at, updated_at
)
values (
  @id::uuid, @slug, @name, @kind, @provider, @auth_type, @encrypted_payload::jsonb, @key_version, 'active', @metadata::jsonb, @created_by::uuid, @now, @now
)
returning id::text, slug, name, kind, provider, auth_type, key_version, status, metadata, created_at, updated_at;

-- name: ListSecrets :many
-- 组织级,不再按 workspace 过滤. 可按 kind 过滤(传空字符串则返回所有 kind).
select id::text, slug, name, kind, provider, auth_type, key_version, status, metadata, created_at, updated_at
from secrets
where (@kind_filter::text = '' or kind = @kind_filter::text)
  and deleted_at is null
order by created_at desc, id desc
limit @item_limit;

-- name: GetSecretPayload :one
select id::text, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_at, updated_at
from secrets
where id = @id::uuid
  and deleted_at is null;

-- name: ResolveSlackBotSecretByTeam :one
-- Resolve the active Slack bot-token secret for a workspace, keyed by the
-- Slack team_id stamped in metadata at install time. kind='slack_bot' is a
-- free-text convention (the secrets table has no kind CHECK), so no migration
-- is needed; metadata->>'team_id' scopes the row to one workspace. The neutral
-- Slack channel decrypts encrypted_payload to mint the per-call Web API bearer,
-- so a re-installed app rotates without a process restart. Newest active row
-- wins when two installs share a team (re-install supersedes).
select id::text, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_at, updated_at
from secrets
where kind = 'slack_bot'
  and status = 'active'
  and metadata->>'team_id' = @team_id::text
  and deleted_at is null
order by created_at desc, id desc
limit 1;

-- name: ResolveDiscordBotSecretByGuild :one
-- Resolve the active Discord bot-token secret for a guild, keyed by the Discord
-- guild_id stamped in metadata at install time. kind='discord_bot' is a
-- free-text convention (the secrets table has no kind CHECK), so no migration
-- is needed; metadata->>'guild_id' scopes the row to one guild. The neutral
-- Discord channel decrypts encrypted_payload to mint the per-call API/Gateway
-- bearer, so a re-installed bot rotates without a process restart. Newest active
-- row wins when two installs share a guild (re-install supersedes).
select id::text, slug, name, kind, provider, auth_type, encrypted_payload, key_version, status, metadata, created_at, updated_at
from secrets
where kind = 'discord_bot'
  and status = 'active'
  and metadata->>'guild_id' = @guild_id::text
  and deleted_at is null
order by created_at desc, id desc
limit 1;

-- name: DisableSecret :one
update secrets
set status = 'disabled', updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, slug, name, kind, provider, auth_type, key_version, status, metadata, created_at, updated_at;

-- name: ActiveSecretSlugExists :one
select exists (
  select 1 from secrets
  where slug = @slug
    and deleted_at is null
) as exists_active;

-- name: CreateModel :one
-- 组织级共享 model. slug 由调用方传入(generateAutoSlug("model")). 凭据二选一:
-- mode='inline_secret' → secret_id 非空, credential_kind_code 必空
-- mode='credential_ref' → credential_kind_code 非空, secret_id 必空
insert into models(
  id, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode, secret_id, credential_kind_code,
  config, created_by, created_at, updated_at
)
values (
  @id::uuid, @slug, @name, @provider_type, @adapter, @base_url, @model_key,
  @credential_mode,
  nullif(@secret_id::text, '')::uuid,
  nullif(@credential_kind_code::text, ''),
  @config::jsonb, @created_by::uuid, @now, @now
)
returning
  id::text, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode,
  coalesce(secret_id::text, '')::text as secret_id,
  coalesce(credential_kind_code, '')::text as credential_kind_code,
  status, config, coalesce(created_by::text, '')::text as created_by,
  created_at, updated_at;

-- name: UpdateModel :one
-- 可编辑: name / model_key / base_url / config / 凭据绑定.
-- provider_type / adapter / credential_mode 不可改 — 想要换语义就建新 model.
update models
set
  name                 = @name,
  model_key            = @model_key,
  base_url             = @base_url,
  secret_id            = nullif(@secret_id::text, '')::uuid,
  credential_kind_code = nullif(@credential_kind_code::text, ''),
  config               = @config::jsonb,
  updated_at           = @now
where id = @id::uuid
  and deleted_at is null
returning
  id::text, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode,
  coalesce(secret_id::text, '')::text as secret_id,
  coalesce(credential_kind_code, '')::text as credential_kind_code,
  status, config, coalesce(created_by::text, '')::text as created_by,
  created_at, updated_at;

-- name: SoftDeleteModel :execrows
update models
set deleted_at = @now, updated_at = @now
where id = @id::uuid
  and deleted_at is null;

-- name: DisableModel :one
update models
set status = 'disabled', updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning
  id::text, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode,
  coalesce(secret_id::text, '')::text as secret_id,
  coalesce(credential_kind_code, '')::text as credential_kind_code,
  status, config, coalesce(created_by::text, '')::text as created_by,
  created_at, updated_at;

-- name: GetModel :one
select
  id::text, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode,
  coalesce(secret_id::text, '')::text as secret_id,
  coalesce(credential_kind_code, '')::text as credential_kind_code,
  status, config, coalesce(created_by::text, '')::text as created_by,
  created_at, updated_at
from models
where id = @id::uuid
  and deleted_at is null;

-- name: ListModels :many
-- 列出所有活跃 model. 全公司共享,不再按 workspace 过滤.
select
  id::text, slug, name, provider_type, adapter, base_url, model_key,
  credential_mode,
  coalesce(secret_id::text, '')::text as secret_id,
  coalesce(credential_kind_code, '')::text as credential_kind_code,
  status, config, coalesce(created_by::text, '')::text as created_by,
  created_at, updated_at
from models
where deleted_at is null
order by created_at desc, id desc
limit @item_limit;

-- name: ActiveModelSlugExists :one
select exists (
  select 1 from models
  where slug = @slug
    and deleted_at is null
) as exists_active;

-- name: GetModelStatus :one
select status as model_status
from models
where id = @id::uuid
  and deleted_at is null;

-- name: ResolveModelRuntime :one
-- model + (optional) inline_secret join.
-- credential_ref 模式不在这里 join user_credentials, 因为它要 by-user 查;
-- 调用方收到 ModelRuntime 后再单独查 user_credentials.
select
  m.id::text as model_id,
  m.name as model_name,
  m.model_key,
  m.provider_type,
  m.adapter,
  m.base_url,
  m.config as model_config,
  m.credential_mode,
  coalesce(m.secret_id::text, '')::text as secret_id,
  coalesce(m.credential_kind_code, '')::text as credential_kind_code,
  coalesce(s.encrypted_payload, '{}'::jsonb) as secret_encrypted_payload,
  coalesce(s.status, '')::text as secret_status
from models m
left join secrets s
  on m.credential_mode = 'inline_secret'
  and s.id = m.secret_id
  and s.deleted_at is null
where m.id = @id::uuid
  and m.status = 'active'
  and m.deleted_at is null;

-- name: DisableProjectAgent :one
update project_agents
set status = 'disabled', updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- name: EnableProjectAgent :one
update project_agents
set status = 'active', updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text, workspace_id::text, project_id::text, agent_id::text, status, config, created_at, updated_at;

-- name: GetProjectAgentDetailForRead :one
select
  pa.id::text as id,
  pa.workspace_id::text as workspace_id,
  pa.project_id::text as project_id,
  pa.agent_id::text as agent_id,
  pa.status,
  pa.config,
  pa.created_by::text as created_by,
  pa.created_at,
  pa.updated_at,
  a.name as agent_name,
  a.slug as agent_slug,
  coalesce(a.description, '')::text as description,
  a.connector_type
from project_agents pa
join agents a on a.id = pa.agent_id
where pa.id = @id::uuid
  and pa.deleted_at is null
  and a.deleted_at is null;

-- name: ListWorkspaceMembers :many
-- 管理 UI 成员列表只看 active 成员;pending/rejected 走专门的 join-request 端点。
select
  wm.id::text as id,
  wm.workspace_id::text as workspace_id,
  wm.user_id::text as user_id,
  wm.role,
  wm.created_at,
  wm.updated_at,
  u.email as user_email,
  u.name as user_name,
  u.status as user_status
from workspace_members wm
join users u on u.id = wm.user_id
join workspaces w on w.id = wm.workspace_id
where wm.workspace_id = @workspace_id::uuid
  and wm.deleted_at is null
  and wm.status = 'active'
  and u.deleted_at is null
  and w.deleted_at is null
order by wm.created_at asc, wm.id asc
limit @item_limit;

-- name: ListActiveWorkspaceOwnerNames :many
-- 飞书 visibility=workspace 拒绝卡片里展示 “管理员: A、B” 用。只取 role='owner' +
-- status='active'; 按 created_at asc 让最早的 owner(通常是创建者)排在前面,稳定
-- 截断。返回 display_name 用 users.name,name 为空时退回 email,前端层不必再判 NULL。
select coalesce(nullif(u.name, ''), u.email)::text as display_name
from workspace_members wm
join users u on u.id = wm.user_id
join workspaces w on w.id = wm.workspace_id
where wm.workspace_id = @workspace_id::uuid
  and wm.role = 'owner'
  and wm.status = 'active'
  and wm.deleted_at is null
  and u.deleted_at is null
  and w.deleted_at is null
order by wm.created_at asc, wm.id asc
limit @item_limit;

-- name: GetWorkspaceVisibilityByID :one
-- 飞书拒绝路径用: 只在 visibility=workspace 且非成员时调用,所以单独抽一个最小
-- 投影,不复用 GetWorkspaceForOwnerView 之类带 join 的查询。返回 'public' /
-- 'private',workspace 不存在或被软删时 sql.ErrNoRows。
select visibility
from workspaces
where id = @id::uuid
  and deleted_at is null;

-- name: ListUserWorkspaces :many
-- `/api/v1/me/workspaces` 当前用户的工作区列表;只返回 active 成员行,
-- pending(申请中) 不出现在主切换器,审批通过后自动出现。
select
  w.id::text as id,
  w.name,
  w.slug,
  w.visibility,
  w.created_at,
  w.updated_at,
  wm.role
from workspace_members wm
join workspaces w on w.id = wm.workspace_id
where wm.user_id = @user_id::uuid
  and wm.deleted_at is null
  and wm.status = 'active'
  and w.deleted_at is null
order by w.name asc, w.id asc
limit @item_limit;

-- name: ListAllActiveWorkspaces :many
-- Platform-admin only: returns every non-deleted workspace regardless
-- of membership. Role is reported as 'owner' so the switcher renders
-- the entry the same way as a real owner-membership row.
select
  w.id::text as id,
  w.name,
  w.slug,
  w.visibility,
  w.created_at,
  w.updated_at,
  'owner'::text as role
from workspaces w
where w.deleted_at is null
order by w.name asc, w.id asc
limit @item_limit;

-- name: ListWorkspaceProjects :many
-- Every active project the calling workspace member can see.
-- The join on workspace_members also doubles as an active-membership
-- gate: a deleted_at / status<>'active' row drops every project,
-- mirroring requireWorkspaceMember.
select
  p.id::text as id,
  p.workspace_id::text as workspace_id,
  p.name,
  p.slug,
  p.description,
  p.status,
  p.created_at,
  p.updated_at
from projects p
join workspace_members wm
  on wm.workspace_id = p.workspace_id
 and wm.user_id = @user_id::uuid
 and wm.deleted_at is null
 and wm.status = 'active'
where p.workspace_id = @workspace_id::uuid
  and p.deleted_at is null
  and p.status = 'active'
order by p.name asc, p.id asc
limit @item_limit;

-- name: ListWorkspaceProjectsAdmin :many
-- Platform-admin only: same shape as ListWorkspaceProjects but without
-- the workspace_members gate. Mirrors ListAllActiveWorkspaces.
select
  p.id::text as id,
  p.workspace_id::text as workspace_id,
  p.name,
  p.slug,
  p.description,
  p.status,
  p.created_at,
  p.updated_at
from projects p
where p.workspace_id = @workspace_id::uuid
  and p.deleted_at is null
  and p.status = 'active'
order by p.name asc, p.id asc
limit @item_limit;

-- name: UpsertUserByEmail :one
-- Idempotent user upsert by email. If a non-deleted user already exists
-- with this email, returns that user's id (status / name untouched).
-- Otherwise inserts a fresh user row and returns the new id. `created`
-- tells the caller whether they're looking at a fresh row or a pre-
-- existing one.
with inserted as (
  insert into users(id, email, name, status, created_at, updated_at)
  select @id::uuid, @email, @name, 'active', @now, @now
  where not exists (
    select 1 from users
    where users.email = @email and users.deleted_at is null
  )
  returning users.id, users.email, users.name, users.status, users.created_at, users.updated_at
)
select
  i.id::text as id,
  i.email,
  i.name,
  i.status,
  i.created_at,
  i.updated_at,
  true as created
from inserted i
union all
select
  u.id::text as id,
  u.email,
  u.name,
  u.status,
  u.created_at,
  u.updated_at,
  false as created
from users u
where u.email = @email
  and u.deleted_at is null
  and not exists (select 1 from inserted)
limit 1;

-- name: AddWorkspaceMember :one
-- Add a user to a workspace with the given role + status. Idempotent on
-- (workspace_id, user_id). If a non-deleted row exists (active 或 pending),
-- 直接返回它(让调用方据 status 自行判断是 "已是成员" 还是 "申请中"); 若行被
-- 软删除则复活成请求的 (role, status); 否则插入新行。
--
-- 注意:这里没有区分 active / pending —— 调用方传 @status,handler 自己决定:
--   - owner 直接 add member 入口:@status = 'active'
--   - 用户自助申请加入入口:@status = 'pending'
--   - bootstrap:@status = 'active'
--
-- 唯一索引 uk_workspace_members_active 排除了 rejected 行,因此被拒后
-- 复活 / 重新插入都不冲突;若数据库里残留一条 rejected 行,SoftDeleteRejectedJoinRequest
-- 会在申请入口先把它清掉,再走这个 query。
with revived as (
  update workspace_members
  set role = @role, status = @status,
      request_reason = case when @status = 'pending' then @request_reason::text else null end,
      reviewed_by = null, reviewed_at = null,
      updated_at = @now, deleted_at = null
  where workspace_id = @workspace_id::uuid
    and user_id = @user_id::uuid
    and deleted_at is not null
  returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, status, created_at, updated_at
), existing as (
  select id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, status, created_at, updated_at
  from workspace_members
  where workspace_id = @workspace_id::uuid
    and user_id = @user_id::uuid
    and deleted_at is null
    and status <> 'rejected'
), inserted as (
  insert into workspace_members(id, workspace_id, user_id, role, status, request_reason, created_at, updated_at)
  select @id::uuid, @workspace_id::uuid, @user_id::uuid, @role, @status,
         case when @status = 'pending' then @request_reason::text else null end,
         @now, @now
  where not exists (select 1 from existing)
    and not exists (select 1 from revived)
  returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, status, created_at, updated_at
)
select id, workspace_id, user_id, role, status, created_at, updated_at
from revived
union all
select id, workspace_id, user_id, role, status, created_at, updated_at
from existing
union all
select id, workspace_id, user_id, role, status, created_at, updated_at
from inserted
limit 1;

-- name: UpdateWorkspaceMemberRole :one
-- 调整 active 成员的角色。pending(申请中) 或 rejected 行不在此处理 ——
-- 申请走 join-request approve/reject 端点。
update workspace_members
set role = @role, updated_at = @now
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and deleted_at is null
  and status = 'active'
returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, created_at, updated_at;

-- name: SoftDeleteWorkspaceMember :one
-- 移除 active 成员。pending(申请中) 行不在此处理(走 reject)。
update workspace_members
set deleted_at = @now, updated_at = @now
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and deleted_at is null
  and status = 'active'
returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, created_at, updated_at;

-- name: WorkspaceMembershipExists :one
-- 用于把用户加入 project 前预检:必须是 workspace 的 active 成员才行。
-- pending(申请中) 不算,因此 join request 通过前不能被加 project。
select exists (
  select 1 from workspace_members
  where workspace_id = @workspace_id::uuid
    and user_id = @user_id::uuid
    and deleted_at is null
    and status = 'active'
) as exists;

-- name: CreateWorkspace :one
-- Admin-side workspace create (Phase 2 dev path). Slug is globally
-- unique; ON CONFLICT does not fire — callers detect the duplicate via
-- ErrDuplicateWorkspaceSlug after we probe with SlugExists.
-- @visibility 接受 'public' / 'private';默认私密,owner 可在设置里改。
insert into workspaces(id, name, slug, visibility, created_by, created_at, updated_at)
values (@id::uuid, @name, @slug, @visibility, @created_by::uuid, @now, @now)
returning id::text as id, name, slug, visibility, created_at, updated_at;

-- name: WorkspaceSlugExists :one
select exists(
  select 1 from workspaces
  where slug = @slug
    and deleted_at is null
) as exists;

-- name: UpdateWorkspace :one
-- Rename a workspace (name and/or slug) 并可调整 visibility。调用方传期望的
-- 最终值;若想保持某字段不动就传当前值。返回新行。
update workspaces
set name = @name,
    slug = @slug,
    visibility = @visibility,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, name, slug, visibility, created_at, updated_at;

-- name: ArchiveWorkspace :one
-- Soft-delete a workspace (sets deleted_at). The caller is responsible
-- for cascading any UI side effects (e.g. clearing the active workspace
-- in localStorage). Children rows in workspace_members / projects are
-- left intact; queries already filter them via the workspaces JOIN.
update workspaces
set deleted_at = @now, updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, name, slug, created_at, updated_at;

-- name: HasMarketplaceDependentsForWorkspace :one
select exists(
  select 1
  from capability c
  join agent_capabilities ac on ac.capability_id = c.id
  join project_agents pa on pa.id = ac.project_agent_id
  join projects p on p.id = pa.project_id
  where c.workspace_id = @workspace_id::uuid
    and p.workspace_id != c.workspace_id
    and ac.enabled = true
    and c.deleted_at is null
    and p.deleted_at is null
) as exists;

-- name: CreateProject :one
-- Admin-side project create inside a workspace (Phase 2 dev path).
-- (workspace_id, slug) is unique among non-deleted rows, so duplicate
-- slugs are caught by the partial unique index; callers probe with
-- ProjectSlugExistsInWorkspace beforehand to return a friendly 409.
insert into projects(id, workspace_id, name, slug, description, status, config, created_by, created_at, updated_at)
values (@id::uuid, @workspace_id::uuid, @name, @slug, @description, 'active', '{}', @created_by::uuid, @now, @now)
returning id::text as id, workspace_id::text as workspace_id, name, slug, description, status, created_at, updated_at;

-- name: ProjectSlugExistsInWorkspace :one
select exists(
  select 1 from projects
  where workspace_id = @workspace_id::uuid
    and slug = @slug
    and deleted_at is null
) as exists;

-- name: UpdateProject :one
update projects
set name = @name,
    slug = @slug,
    description = @description,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, name, slug, description, status, created_at, updated_at;

-- name: ArchiveProject :one
-- Soft-archive a project (sets status = 'archived'; not deleted_at, so
-- the row stays joinable for historical lookups). Active=true list
-- queries (ListWorkspaceProjects) filter status, so this hides it from
-- the switcher without breaking past references.
update projects
set status = 'archived', updated_at = @now
where id = @id::uuid
  and status = 'active'
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, name, slug, description, status, created_at, updated_at;

-- ============================================================
-- user_sessions (Phase 3 real-auth)
-- ============================================================

-- name: CreateSession :exec
insert into user_sessions(
  id, user_id, created_at, last_seen_at, expires_at, user_agent, ip
) values (
  @id, @user_id::uuid, @now, @now, @expires_at, @user_agent, @ip
);

-- name: GetActiveSession :one
-- Single PK lookup; returns the user id alongside so the middleware
-- can avoid a second round-trip. `expires_at > $now` filters expired
-- rows so an attacker can't reuse a leaked token after timeout.
select
  s.id,
  s.user_id::text as user_id,
  s.created_at,
  s.last_seen_at,
  s.expires_at
from user_sessions s
where s.id = @id
  and s.revoked_at is null
  and s.expires_at > @now;

-- name: TouchSession :exec
update user_sessions
set last_seen_at = @now
where id = @id
  and revoked_at is null;

-- name: RevokeSession :exec
update user_sessions
set revoked_at = @now
where id = @id
  and revoked_at is null;

-- name: RevokeAllSessionsForUser :exec
update user_sessions
set revoked_at = @now
where user_id = @user_id::uuid
  and revoked_at is null;

-- name: ListActiveSessionsForUser :many
-- Powers the "active sessions" admin list (e.g. show device + last
-- seen, allow per-row revoke). Caller filters by expires_at >= now
-- in code if it cares about "active right now" vs "not yet expired".
select
  s.id,
  s.created_at,
  s.last_seen_at,
  s.expires_at,
  s.user_agent,
  s.ip
from user_sessions s
where s.user_id = @user_id::uuid
  and s.revoked_at is null
  and s.expires_at > @now
order by s.last_seen_at desc;

-- ============================================================
-- auth identities (Phase 3 real-auth: Feishu OIDC upsert)
-- ============================================================

-- name: UpsertAuthIdentity :exec
-- Idempotent upsert keyed by (provider, subject). On conflict the
-- identity is rebound to the most recent user id + metadata refresh;
-- the row is the authoritative "which user is this Feishu union_id".
--
-- Caller is responsible for the metadata jsonb shape; OIDC paths
-- typically stash { "name": ..., "avatar_url": ..., "email": ... }
-- so admin tooling can surface the auth-provider profile fields
-- without re-fetching the OAuth user_info.
insert into auth_identities(id, user_id, provider, subject, metadata, created_at, updated_at)
values (@id::uuid, @user_id::uuid, @provider, @subject, @metadata::jsonb, @now, @now)
on conflict (provider, subject) do update
set user_id    = excluded.user_id,
    metadata   = excluded.metadata,
    updated_at = excluded.updated_at;

-- ============================================================
-- sandboxes (Phase 4: Persistent Sandbox Provider)
-- ============================================================
--
-- These queries back PersistentSandboxProvider's DB-side audit
-- + admin lookup. The in-memory cache is the source of truth at
-- runtime; the DB row is "sandbox X currently exists and is bound
-- to this project agent" for admin UI listings and post-restart sweep.
-- Pool entries use the same table with allocation_status='pooled'.
--
-- envd_access_token / endpoint are intentionally NOT in the
-- schema — see migration 000008 for the rationale.

-- name: CreateSandboxBinding :one
-- Insert a new binding row when the provider just spawned a
-- sandbox for a (workspace, project_agent) key. Caller MUST
-- ensure no other live bound sandbox exists for the same
-- (workspace_id, project_agent_id) — the partial unique index
-- enforces this and the insert will fail loudly if a stale
-- binding was not killed first.
insert into sandboxes(
  id, workspace_id, project_agent_id, cache_key,
  sandbox_id, template_id, lifecycle_status, allocation_status,
  created_at, last_active_at, metadata
)
values (
  @id::uuid, @workspace_id::uuid, @project_agent_id::uuid, @cache_key,
  @sandbox_id, @template_id, @status, 'bound',
  @now, @now, @metadata::jsonb
)
returning
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata;

-- name: GetActiveSandboxBindingForAgent :one
-- Return the live bound sandbox for one (workspace, project_agent), if
-- any. Used by admin GET /sandbox status endpoint.
select
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata
from sandboxes
where workspace_id    = @workspace_id::uuid
  and project_agent_id = @project_agent_id::uuid
  and allocation_status = 'bound'
  and killed_at is null;

-- name: ListActiveSandboxBindingsForWorkspace :many
-- Admin UI workspace overview: active bound sandboxes, newest-active first.
select
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata
from sandboxes
where workspace_id = @workspace_id::uuid
  and allocation_status = 'bound'
  and killed_at is null
order by last_active_at desc
limit @limit_n::int;

-- name: TouchSandboxBinding :exec
-- Bump last_active_at when the provider's Acquire hits a cache
-- entry — gives admin UI a freshness signal even when the
-- binding hasn't been mutated otherwise.
update sandboxes
set last_active_at = @now
where id = @id::uuid
  and allocation_status = 'bound'
  and killed_at is null;

-- name: TouchSandboxBindingByCacheKey :exec
-- Same as TouchSandboxBinding but keyed by cache_key, used by
-- the persistent sandbox Provider's OnCacheHit hook.
update sandboxes
set last_active_at = @now
where cache_key = @cache_key
  and allocation_status = 'bound'
  and killed_at is null;

-- name: MarkSandboxBindingKilled :exec
-- Terminal state transition. Caller supplies the final lifecycle status
-- ('killed' for Kill API / 'killed_orphaned' for startup sweep /
-- 'killed_error' for provider runtime fault). killed_at + now
-- written atomically.
update sandboxes
set lifecycle_status  = @status,
    allocation_status = 'released',
    killed_at         = @now,
    last_active_at    = @now
where id = @id::uuid
  and killed_at is null;

-- name: SweepOrphanedSandboxBindings :execrows
-- Server-startup sweep: every live bound sandbox has lost its
-- in-memory envd token and cannot be re-attached to. Mark it
-- killed_orphaned so the next prompt re-spawns cleanly.
update sandboxes
set lifecycle_status  = 'killed_orphaned',
    allocation_status = 'released',
    killed_at         = @now,
    last_active_at    = @now
where killed_at is null
  and allocation_status = 'bound'
  and lifecycle_status in ('running', 'spawning', 'killing', 'renewing');

-- name: ReserveSandboxBindingSlot :one
-- Multi-pod cold-start coordination: try to grab the
-- (workspace, project_agent) slot before doing any expensive
-- sandbox / runtime work. Inserts a 'spawning' bound row holding
-- a placeholder sandbox_id; the partial unique index
-- uk_sandboxes_active_per_agent enforces single-winner across
-- pods. On conflict the caller switches to WaitForActiveSandboxBindingByAgent
-- to follow the winner's progress instead of racing it.
--
-- sandbox_id is required NOT NULL and must be globally unique
-- (sandboxes_sandbox_id_key); we reserve a "pending-<uuid>"
-- placeholder here and overwrite it in FinalizeSandboxBindingSpawning
-- once the real e2b sandbox id is known.
insert into sandboxes(
  id, workspace_id, project_agent_id, cache_key,
  sandbox_id, template_id, lifecycle_status, allocation_status,
  created_at, last_active_at, metadata
)
values (
  @id::uuid, @workspace_id::uuid, @project_agent_id::uuid, @cache_key,
  @placeholder_sandbox_id, @template_id, 'spawning', 'bound',
  @now, @now, @metadata::jsonb
)
returning
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata;

-- name: FinalizeSandboxBindingSpawning :exec
-- Winner-only update after cold-start finishes successfully.
-- Flips spawning → running and overwrites the placeholder
-- sandbox_id with the real e2b id; metadata is replaced (caller
-- merged any prior fields it wants kept). WHERE clause limits the
-- update to the row's spawning state to avoid clobbering a row
-- that was already killed_error by a concurrent failure path.
update sandboxes
set lifecycle_status  = 'running',
    sandbox_id        = @sandbox_id,
    metadata          = @metadata::jsonb,
    last_active_at    = @now
where id = @id::uuid
  and lifecycle_status = 'spawning'
  and killed_at is null;

-- name: GetActiveSandboxBindingByAgentForWait :one
-- Loser-side polling query: read whatever bound row exists for
-- (workspace, project_agent) today, regardless of lifecycle state.
-- Caller distinguishes spawning vs running vs killed_* from the
-- returned status. Distinct from GetActiveSandboxBindingForAgent
-- which intentionally hides spawning rows from admin listings.
select
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata
from sandboxes
where workspace_id    = @workspace_id::uuid
  and project_agent_id = @project_agent_id::uuid
  and allocation_status = 'bound'
  and killed_at is null;

-- name: ListIdleSandboxBindings :many
-- Idle TTL sweeper (Phase 4 milestone B): pick up live bound sandboxes
-- whose last_active_at is older than the cutoff.
select
  id::text            as id,
  workspace_id::text  as workspace_id,
  project_agent_id,
  name,
  cache_key,
  sandbox_id,
  template_id,
  lifecycle_status    as status,
  created_at,
  last_active_at,
  killed_at,
  metadata
from sandboxes
where killed_at is null
  and allocation_status = 'bound'
  and last_active_at < @idle_before
order by last_active_at asc
limit @limit_n::int;

-- name: GetProjectAgentRuntime :one
-- Phase 4 milestone E (sandbox warm): admin-side fetch for the
-- merged-config inputs the OpenCode Connector needs to spawn a
-- sandbox without a real prompt. Pulls (workspace, project_agent)
-- and joins the parent agent so the caller can mergeConfig the
-- two jsonb blobs the same way Prompt path does. Excludes deleted
-- + disabled project_agents so the warm endpoint cannot revive
-- a rebound agent that has been turned off.
select
  pa.id::text                  as project_agent_id,
  pa.workspace_id::text        as workspace_id,
  pa.project_id::text          as project_id,
  pa.agent_id::text            as agent_id,
  pa.status                    as project_agent_status,
  pa.config                    as project_agent_config,
  a.connector_type             as connector_type,
  a.config                     as agent_config
from project_agents pa
join agents a on a.id = pa.agent_id
join workspaces w on w.id = pa.workspace_id
where pa.id = @project_agent_id::uuid
  and pa.workspace_id = @workspace_id::uuid
  and pa.status = 'active'
  and pa.deleted_at is null
  and a.status = 'active'
  and a.deleted_at is null
  and w.deleted_at is null;

-- name: CountActiveWorkspaceOwners :one
-- Bootstrap gate: returns the number of active workspace_members rows
-- whose role is 'owner' AND whose workspace is not soft-deleted. The
-- server/internal/bootstrap layer uses this to decide whether the
-- first-owner provisioning path is still open: count == 0 means the
-- DB is empty (the install never finished setup) so the bootstrap
-- HTTP/CLI endpoint accepts a Create call; count > 0 means setup
-- already completed and the endpoint must refuse so a leaked
-- PARSAR_BOOTSTRAP_TOKEN cannot back-door a fresh admin in.
--
-- 必须只数 status='active' 的 owner 行 —— pending 不算 owner;
-- 否则一条 pending join-request 行会把 bootstrap 闸门误锁。
select count(*)::bigint as owner_count
from workspace_members wm
join workspaces w on w.id = wm.workspace_id
where wm.role = 'owner'
  and wm.deleted_at is null
  and wm.status = 'active'
  and w.deleted_at is null;

-- name: InsertAgentRunEvent :one
insert into agent_run_events(
  workspace_id, project_id, agent_run_id, sequence,
  event_kind, payload, occurred_at, created_at
)
select
  r.workspace_id,
  r.project_id,
  r.id,
  @sequence::bigint,
  @event_kind,
  @payload::jsonb,
  @occurred_at,
  @created_at
from agent_runs r
join projects p on p.id = r.project_id
where r.id = @agent_run_id::uuid
  and r.workspace_id = p.workspace_id
  and p.status = 'active'
  and p.deleted_at is null
on conflict (agent_run_id, sequence) do nothing
returning
  id::text,
  workspace_id::text,
  project_id::text,
  agent_run_id::text,
  sequence,
  event_kind,
  payload,
  occurred_at,
  created_at;

-- name: ListAgentRunEventsByRun :many
select
  id::text,
  workspace_id::text,
  project_id::text,
  agent_run_id::text,
  sequence,
  event_kind,
  payload,
  occurred_at,
  created_at
from agent_run_events
where agent_run_id = @agent_run_id::uuid
  and project_id = @project_id::uuid
  and sequence > @after_sequence::bigint
order by sequence asc;

-- name: ListToolEventsForRuns :many
select id::text, agent_run_id::text, sequence, event_kind, payload, occurred_at
from agent_run_events
where agent_run_id = any(@run_ids::uuid[])
  and event_kind in ('tool.call', 'tool.result')
order by agent_run_id, sequence asc;

-- name: GetConnectorSessionBinding :one
-- Persisted upstream-session lookup. Returns the connector-owned
-- session id for (conversation_id, connector_type, binding_key), or
-- pgx.ErrNoRows when no binding exists. last_active_at is bumped on
-- cache hits via a separate UPDATE so the read stays a plain SELECT.
select upstream_session_id::text
from connector_session_bindings
where conversation_id = @conversation_id::text
  and connector_type = @connector_type::text
  and binding_key = @binding_key::text;

-- name: TouchConnectorSessionBinding :exec
-- Cache-hit refresh of last_active_at. Idempotent NO-OP if no row
-- exists (which never happens after a successful Get, but the WHERE
-- keeps the contract honest).
update connector_session_bindings
set last_active_at = now()
where conversation_id = @conversation_id::text
  and connector_type = @connector_type::text
  and binding_key = @binding_key::text;

-- name: ListConnectorSessionBindings :many
-- Enumerate all bindings for one conversation and connector type.
-- Backs connector diagnostic dumps without exposing one connector's
-- upstream sessions to another connector. `metadata` is returned so
-- connectors that overload the column (e.g. agent_daemon stashes
-- agent_kind / claude_session_id / work_dir there) can reconstruct
-- the full binding in one query; callers that don't care just ignore
-- it (opencode/bindingstore.go is one such caller).
select binding_key::text, upstream_session_id::text, metadata
from connector_session_bindings
where conversation_id = @conversation_id::text
  and connector_type = @connector_type::text
order by binding_key asc;

-- name: UpsertConnectorSessionBinding :exec
-- A Put follows a fresh upstream CreateSession, so the most-recent
-- session id wins. last_active_at is set to now() because the call
-- site has just observed the binding being used.
insert into connector_session_bindings (
  conversation_id, connector_type, binding_key, upstream_session_id, metadata, created_at, last_active_at
)
values (
  @conversation_id::text, @connector_type::text, @binding_key::text, @upstream_session_id::text, coalesce(@metadata::jsonb, '{}'::jsonb), now(), now()
)
on conflict (conversation_id, connector_type, binding_key) do update
set upstream_session_id = excluded.upstream_session_id,
    metadata = excluded.metadata,
    last_active_at = now();

-- name: DeleteConnectorSessionBindingsByBindingKey :exec
-- Connector eviction hook. Drops every binding for one connector type
-- pointing at the evicted connector-private binding_key.
delete from connector_session_bindings
where connector_type = @connector_type::text
  and binding_key = @binding_key::text;

-- name: DeleteConnectorSessionBindingsByConversation :exec
-- Connector.Close drops every binding for one conversation and
-- connector type so the next Prompt starts fresh.
delete from connector_session_bindings
where conversation_id = @conversation_id::text
  and connector_type = @connector_type::text;

-- name: DeleteConnectorSessionBindingsByUpstreamSession :exec
-- Drops every binding for one connector type that points at a given
-- upstream_session_id. agent_daemon uses this when a device goes
-- offline / is revoked so the next prompt against any conversation
-- that was using it falls through to a fresh device pick instead of
-- trying to send to a dead WS session. Connector-scoped so we never
-- accidentally evict another connector's bindings that happen to
-- share an upstream id.
delete from connector_session_bindings
where connector_type = @connector_type::text
  and upstream_session_id = @upstream_session_id::text;

-- name: CountConnectorSessionBindings :one
-- Test-only helper mirroring the in-memory size().
select count(*)::bigint as total
from connector_session_bindings;

-- ============================================================

-- name: CreateCapability :one
insert into capability(
  id, workspace_id, type, name, description, visibility, status,
  creator_id, created_at, updated_at
)
values (
  @id::uuid, @workspace_id::uuid, @type, @name, @description,
  @visibility, @status,
  @creator_id::uuid, @now, @now
)
returning id::text as id, workspace_id::text as workspace_id, type, name,
  description, visibility, status, creator_id::text as creator_id,
  created_at, updated_at, deleted_at, deprecated_at;


-- name: GetUserDisplayName :one
select coalesce(nullif(name, ''), email, id::text)::text as display_name
from users
where id = @id::uuid
  and deleted_at is null;

-- name: CreateSystemMessageOnce :execrows
-- 2026-06-04 schema: messages.message_type 拆成 kind + content_format。
-- 系统事件消息: kind='system_event', content_format='text'。
-- 去重逻辑还是用 metadata->>'kind'(系统侧自定义事件名,例如
-- 'runtime_paired')区分,WHERE EXISTS 查的是 messages.kind。
insert into messages(
  id, workspace_id, project_id, conversation_id,
  sender_type, sender_id, kind, content_format, visibility, content, metadata,
  created_at, updated_at
)
select
  @id::uuid,
  @workspace_id::uuid,
  @project_id::uuid,
  @conversation_id::uuid,
  'system',
  null,
  'system_event',
  'text',
  @visibility,
  @content,
  @metadata::jsonb,
  @now,
  @now
where not exists (
  select 1
  from messages m
  where m.conversation_id = @conversation_id::uuid
    and m.sender_type = 'system'
    and m.kind = 'system_event'
    and m.metadata->>'kind' = @kind::text
    and m.deleted_at is null
);

-- name: CreateRuntimeErrorSystemMessage :exec
-- 2026-06-04 schema: 旧 message_type='runtime_error' 被折叠进
-- kind='error' + metadata.error.source='runtime'(error 类别下还有
-- 'agent' / 'validation' 等其它来源,通过 metadata 区分)。
-- 这里在插入时把 source='runtime' 合进 @metadata,防止调用方漏写。
insert into messages(
  id, workspace_id, project_id, conversation_id,
  sender_type, sender_id, kind, content_format, visibility, content, metadata,
  created_at, updated_at
)
values (
  @id::uuid,
  @workspace_id::uuid,
  @project_id::uuid,
  @conversation_id::uuid,
  'system',
  null,
  'error',
  'text',
  @visibility,
  @content,
  @metadata::jsonb || jsonb_build_object('error', jsonb_build_object('source', 'runtime')),
  @now,
  @now
);

-- name: CreateSandboxOfflineNotice :exec
-- 系统级通知:沙箱离线提示。和 runtime_error 不同 —— 这不是 "error"
-- 类别,是 system_event 类别下的一个 sub-kind,前端按
-- metadata.kind='sandbox_offline_notice' 渲染成灰底警告。允许同
-- conversation 多次插入(无 dedup),用户可能在多个时段碰到沙箱掉线。
insert into messages(
  id, workspace_id, project_id, conversation_id,
  sender_type, sender_id, kind, content_format, visibility, content, metadata,
  created_at, updated_at
)
values (
  @id::uuid,
  @workspace_id::uuid,
  @project_id::uuid,
  @conversation_id::uuid,
  'system',
  null,
  'system_event',
  'text',
  @visibility,
  @content,
  @metadata::jsonb,
  @now,
  @now
);

-- name: ListCapabilityCredentialMissingForRun :many
-- Lists every runtime_error system message for a given run where the
-- soft-degrade resolver tagged it as a credential-missing notice. The
-- outbound driver folds these into the credential-form card rendered
-- in place of the regular DoneCard so the user can bind the missing
-- kinds without re-sending their query.
--
-- We filter on metadata.sub_kind rather than message kind because
-- 'runtime_error' is a single Postgres column value; the discriminator
-- lives in metadata to avoid a schema migration every time a new
-- failure mode joins the family.
--
-- Ordered by created_at so the form card builder can de-duplicate
-- (kind, capability_id) pairs by "first wins" — keeps the visual
-- layout stable across ticks even if the resolver emits the same
-- gap twice in the same run.
select id::text                                                  as message_id,
       coalesce(metadata->>'capability_id', '')                   as capability_id,
       coalesce(metadata->>'capability_name', '')                 as capability_name,
       coalesce(metadata->>'credential_kind', '')                 as credential_kind,
       created_at
from messages
where conversation_id = @conversation_id::uuid
  and kind = 'error'
  and metadata->>'sub_kind' = 'capability_credential_missing'
  and metadata->>'run_id' = @run_id
order by created_at asc;

-- name: GetInboundUserMessageForRun :one
-- Returns the inbound user-text message that triggered the given run,
-- so the Feishu outbound credential-form path can recover the
-- raw_query the user typed and stash it on the qkey row.
--
-- H5: looks up by agent_runs.trigger_message_id (set at run-creation
-- time, immutable for the run's lifetime) rather than "most recent
-- user message <= run.started_at". The old criterion was bypassed when
-- the user typed a fresh message AFTER the credential-form submit but
-- BEFORE the daemon stamped run.started_at — the new typed message
-- satisfied <= started_at and ReenqueuedFrom was empty, so the
-- anti-rerun loop guard didn't fire and the form card was emitted
-- again for the same dead-end credential. Tying to trigger_message_id
-- — the invariant set in the same tx that created the run — closes
-- that gap without depending on wall-clock ordering of fresh inbounds
-- vs the daemon's run-status flip.
--
-- Also returns metadata.reenqueued_from so the form-card path can
-- detect "this turn already came back from a credential-form submit
-- and the same kind is STILL missing" — that means the user mistyped
-- and a second form card would just loop. In that case the caller
-- falls through to the regular terminal card (the user still has the
-- per-conversation in-chat system message telling them to bind via
-- the web UI).
--
-- sender_open_id is the inbound sender's raw Feishu open_id, captured
-- by CreateInboundIMMessage so the credential-form submit-card callback
-- can verify the click is from the same person who triggered the inbound
-- (callback.Operator.OpenID is open_id; we can't fall back to union_id
-- because callback.Operator has no union field). Without this pinning
-- ANY chat member could submit the form on behalf of the initiator.
select m.id::text                                    as message_id,
       coalesce(m.content, '')                        as raw_query,
       coalesce(m.metadata->>'target_agent_id', '')   as target_agent_id,
       coalesce(m.metadata->>'external_chat_id', '')  as external_chat_id,
       coalesce(m.metadata->>'external_thread_id', '') as external_thread_id,
       coalesce(m.metadata->>'external_message_id', '') as external_message_id,
       coalesce(m.metadata->>'sender_open_id', '')    as sender_open_id,
       coalesce(m.metadata->>'reenqueued_from', '')   as reenqueued_from,
       coalesce(m.sender_id::text, '')                as sender_id
from agent_runs r
join messages m on m.id = r.trigger_message_id
where r.id = @run_id::uuid
  and m.conversation_id = @conversation_id::uuid
  and m.sender_type = 'user';

-- name: GetGuestReplyHintForRun :one
-- Returns metadata.guest_reply_hint stamped onto the inbound that
-- triggered the run, or '' when absent. visibility=public lets
-- unregistered users into agent execution; the routing layer stashes a
-- "go register" hint here, but the terminal Feishu card has no other
-- channel to surface it. Keyed on agent_runs.trigger_message_id, no
-- sender_type filter — guests land as sender_type='external', and
-- GetInboundUserMessageForRun's 'user'-only filter (intentional, the
-- credential-form path requires a known sender_id) drops their hint.
select coalesce(m.metadata->>'guest_reply_hint', '')::text as guest_reply_hint
from agent_runs r
join messages m on m.id = r.trigger_message_id
where r.id = @run_id::uuid
  and m.conversation_id = @conversation_id::uuid;

-- name: GetCapability :one
select c.id::text as id, c.workspace_id::text as workspace_id, c.type, c.name,
  c.description, c.visibility, c.status, lv.required_credentials,
  case when lv.id is null then '' else lv.id::text end as latest_version_id, coalesce(lv.version, ''::text) as latest_version,
  lv.created_at as latest_version_created_at, c.creator_id::text as creator_id,
  c.created_at, c.updated_at, c.deleted_at, c.deprecated_at
from capability c
left join lateral (
  select id, version, created_at, required_credentials
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) lv on true
where c.id = @id::uuid;

-- name: GetCapabilityByName :one
select c.id::text as id, c.workspace_id::text as workspace_id, c.type, c.name,
  c.description, c.visibility, c.status, lv.required_credentials,
  case when lv.id is null then '' else lv.id::text end as latest_version_id, coalesce(lv.version, ''::text) as latest_version,
  lv.created_at as latest_version_created_at, c.creator_id::text as creator_id,
  c.created_at, c.updated_at, c.deleted_at, c.deprecated_at
from capability c
left join lateral (
  select id, version, created_at, required_credentials
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) lv on true
where c.workspace_id = @workspace_id::uuid
  and c.name = @name
  and c.deleted_at is null;

-- name: ListCapabilities :many
select c.id::text as id, c.workspace_id::text as workspace_id, c.type, c.name,
  c.description, c.visibility, c.status, lv.required_credentials,
  case when lv.id is null then '' else lv.id::text end as latest_version_id, coalesce(lv.version, ''::text) as latest_version,
  lv.created_at as latest_version_created_at, c.creator_id::text as creator_id,
  c.created_at, c.updated_at, c.deleted_at, c.deprecated_at
from capability c
left join lateral (
  select id, version, created_at, required_credentials
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) lv on true
where c.workspace_id = @workspace_id::uuid
  and c.deleted_at is null
order by c.name asc, c.created_at desc;

-- name: ListCapabilitiesByType :many
select c.id::text as id, c.workspace_id::text as workspace_id, c.type, c.name,
  c.description, c.visibility, c.status, lv.required_credentials,
  case when lv.id is null then '' else lv.id::text end as latest_version_id, coalesce(lv.version, ''::text) as latest_version,
  lv.created_at as latest_version_created_at, c.creator_id::text as creator_id,
  c.created_at, c.updated_at, c.deleted_at, c.deprecated_at
from capability c
left join lateral (
  select id, version, created_at, required_credentials
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) lv on true
where c.workspace_id = @workspace_id::uuid
  and c.type::text = @type
  and c.deleted_at is null
order by c.name asc, c.created_at desc;

-- name: UpdateCapability :one
update capability
set name = @name,
    description = @description,
    visibility = @visibility,
    status = @status,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, type, name,
  description, visibility, status, creator_id::text as creator_id,
  created_at, updated_at, deleted_at, deprecated_at;

-- name: SoftDeleteCapability :one
-- 原子写:NOT EXISTS 子查询和 UPDATE 在同一条语句里,DB 一致性快照下没有
-- "查空 binding → 别人插一条 → 我们删"这种窗口。0 行返回时调用方再 fetch
-- 一次 count 用于 409 报告(纯失败路径,正常删除只发一条 SQL)。
-- workspace_id 守卫这里也加上,defense-in-depth:handler 上层走过
-- GetCapability,但这里独立保护被绕过的情况。
update capability
set deleted_at = @now,
    updated_at = @now
where id = @id::uuid
  and workspace_id = @workspace_id::uuid
  and deleted_at is null
  and not exists (
    select 1 from agent_capabilities
    where capability_id = @id::uuid
  )
returning id::text as id, workspace_id::text as workspace_id, type, name,
  description, visibility, status, creator_id::text as creator_id,
  created_at, updated_at, deleted_at, deprecated_at;

-- name: UpdateCapabilityMarketplaceState :one
update capability
set visibility = @visibility,
    deprecated_at = sqlc.narg('deprecated_at')::timestamptz,
    updated_at = @now
where id = @id::uuid
  and workspace_id = @workspace_id::uuid
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, type, name,
  description, visibility, status, creator_id::text as creator_id,
  created_at, updated_at, deleted_at, deprecated_at;

-- name: ListMarketplaceCapabilities :many
with installed as (
  select distinct ac.capability_id
  from agent_capabilities ac
  join project_agents pa on pa.id = ac.project_agent_id
  join projects p on p.id = pa.project_id
  where p.workspace_id = @target_workspace_id::uuid
)
select
  c.id::text as capability_id,
  c.workspace_id::text as source_workspace_id,
  w.name as source_workspace_name,
  c.type,
  c.name,
  c.description,
  c.visibility,
  c.status,
  cv.required_credentials,
  c.created_at,
  c.updated_at,
  c.deprecated_at,
  cv.id::text as latest_version_id,
  cv.version as latest_version,
  cv.created_at as latest_version_created_at,
  (installed.capability_id is not null)::bool as installed,
  (c.workspace_id = @target_workspace_id::uuid)::bool as self_published
from capability c
join workspaces w on w.id = c.workspace_id
join lateral (
  select id, version, created_at, required_credentials
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) cv on true
left join installed on installed.capability_id = c.id
where c.visibility = 'public'
  and c.status = 'active'
  and c.deprecated_at is null
  and c.deleted_at is null
order by c.name asc, cv.created_at desc;

-- name: ListWorkspaceMarketplaceInstalls :many
select distinct
  c.id::text as capability_id,
  c.name,
  c.description,
  c.type,
  cv.required_credentials,
  c.workspace_id::text as source_workspace_id,
  src_ws.name as source_workspace_name,
  ac.capability_version_id::text as pinned_version_id,
  cv.version as pinned_version,
  c.deprecated_at,
  latest.id::text as latest_version_id,
  latest.version as latest_published_version,
  latest.created_at as latest_version_created_at,
  (
    select count(distinct pa2.id)::bigint
    from agent_capabilities ac2
    join project_agents pa2 on ac2.project_agent_id = pa2.id
    join projects p2 on pa2.project_id = p2.id
    where ac2.capability_id = c.id
      and p2.workspace_id = @target_workspace_id::uuid
  ) as enabled_agent_count
from agent_capabilities ac
join project_agents pa on ac.project_agent_id = pa.id
join projects p on pa.project_id = p.id
join capability_version cv on ac.capability_version_id = cv.id
join capability c on cv.capability_id = c.id
join workspaces src_ws on src_ws.id = c.workspace_id
join lateral (
  select id, version, created_at
  from capability_version
  where capability_id = c.id
  order by created_at desc, version desc
  limit 1
) latest on true
where p.workspace_id = @target_workspace_id::uuid
  and c.workspace_id != @target_workspace_id::uuid
  and c.visibility = 'public'
  and c.deleted_at is null
order by c.name asc, cv.version asc;

-- name: CountInstalls :one
select count(distinct p.workspace_id)::bigint
from agent_capabilities ac
join project_agents pa on ac.project_agent_id = pa.id
join projects p on pa.project_id = p.id
join capability c on c.id = ac.capability_id
where ac.capability_id = @source_capability_id::uuid
  and p.workspace_id != c.workspace_id;

-- 数所有 agent_capabilities 引用——包括本 workspace 内部的 agent 绑定和跨 workspace
-- 的 marketplace 装机方。用于删除拦截:capability 被任何 agent 绑着就不能删,
-- 否则那些 agent 会突然看到 "capability not found"。
-- name: CountAgentBindingsForCapability :one
select count(distinct ac.project_agent_id)::bigint
from agent_capabilities ac
where ac.capability_id = @capability_id::uuid;

-- name: ListEnabledAgentsForMarketplaceCapability :many
select distinct
  pa.id::text as project_agent_id,
  pa.agent_id::text as agent_id,
  pa.project_id::text as project_id,
  a.name as agent_name,
  (pa.status = 'active')::bool as enabled,
  ac.capability_version_id::text as capability_version_id,
  cv.version as version
from agent_capabilities ac
join project_agents pa on pa.id = ac.project_agent_id
join agents a on a.id = pa.agent_id
join projects p on p.id = pa.project_id
join capability_version cv on cv.id = ac.capability_version_id
where p.workspace_id = @target_workspace_id::uuid
  and ac.capability_id = @source_capability_id::uuid
order by a.name asc, pa.id asc;

-- name: UninstallWorkspaceMarketplaceCapability :execrows
delete from agent_capabilities ac
using project_agents pa, projects p
where ac.project_agent_id = pa.id
  and pa.project_id = p.id
  and p.workspace_id = @target_workspace_id::uuid
  and ac.capability_id = @source_capability_id::uuid;

-- name: CreateCapabilityVersion :one
insert into capability_version(
  id, capability_id, version, git_repo_url, git_ref, path,
  content, source_payload, schema_version, canonical_spec,
  required_credentials, oss_key, sha256, creator_id, created_at
)
values (
  @id::uuid, @capability_id::uuid, @version,
  sqlc.narg('git_repo_url')::text, sqlc.narg('git_ref')::text,
  sqlc.narg('path')::text, sqlc.narg('content')::jsonb,
  sqlc.narg('source_payload')::jsonb, @schema_version::smallint,
  sqlc.narg('canonical_spec')::jsonb,
  @required_credentials::jsonb,
  @oss_key::text, @sha256::text,
  @creator_id::uuid, @now
)
returning id::text as id, capability_id::text as capability_id, version,
  git_repo_url, git_ref, path, content,
  source_payload, schema_version, canonical_spec,
  required_credentials, oss_key, sha256,
  creator_id::text as creator_id, created_at;

-- name: GetCapabilityVersion :one
select id::text as id, capability_id::text as capability_id, version,
  git_repo_url, git_ref, path, content,
  source_payload, schema_version, canonical_spec,
  required_credentials, oss_key, sha256,
  creator_id::text as creator_id, created_at
from capability_version
where id = @id::uuid;

-- name: ListCapabilityVersionsByCapability :many
select id::text as id, capability_id::text as capability_id, version,
  git_repo_url, git_ref, path, content,
  source_payload, schema_version, canonical_spec,
  required_credentials, oss_key, sha256,
  creator_id::text as creator_id, created_at
from capability_version
where capability_id = @capability_id::uuid
order by created_at desc, version desc;

-- name: GetLatestCapabilityVersionByCapability :one
select id::text as id, capability_id::text as capability_id, version,
  git_repo_url, git_ref, path, content,
  source_payload, schema_version, canonical_spec,
  required_credentials, oss_key, sha256,
  creator_id::text as creator_id, created_at
from capability_version
where capability_id = @capability_id::uuid
order by created_at desc, version desc
limit 1;

-- name: CreateUserCredential :one
insert into user_credentials(
  id, user_id, kind, display_name, ciphertext, key_version,
  last_used_at, created_at, updated_at
)
values (
  @id::uuid, @user_id::uuid, @kind, @display_name,
  @ciphertext::bytea, @key_version, sqlc.narg('last_used_at')::timestamptz,
  @now, @now
)
returning id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at;

-- name: SoftDeleteUserCredentialByUserKind :execrows
-- Soft-delete every active row matching (user_id, kind). Returns the
-- number of rows newly soft-deleted so the Go wrapper can tell the
-- caller whether they replaced an existing credential vs. wrote a new
-- one. Used by the credential-form submit path (and any other
-- "user provides new credential of an existing kind" flow) to clear
-- the slot before INSERTing the fresh row — keeps the partial unique
-- index `(user_id, kind) WHERE deleted_at IS NULL` happy without
-- needing ON CONFLICT plumbing through sqlc.
update user_credentials
set deleted_at = @now,
    updated_at = @now
where user_id = @user_id::uuid
  and kind = @kind
  and deleted_at is null;

-- name: GetUserCredential :one
select id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at
from user_credentials
where id = @id::uuid;

-- name: GetUserCredentialByUserKind :one
select id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at
from user_credentials
where user_id = @user_id::uuid
  and kind = @kind
  and deleted_at is null;

-- name: ListUserCredentialsByUser :many
select id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at
from user_credentials
where user_id = @user_id::uuid
  and deleted_at is null
order by kind asc, display_name asc;

-- name: UpdateUserCredential :one
update user_credentials
set display_name = @display_name,
    ciphertext = @ciphertext::bytea,
    key_version = @key_version,
    last_used_at = sqlc.narg('last_used_at')::timestamptz,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at;

-- name: SoftDeleteUserCredential :one
update user_credentials
set deleted_at = @now,
    updated_at = @now
where id = @id::uuid
  and deleted_at is null
returning id::text as id, user_id::text as user_id, kind, display_name,
  ciphertext, key_version, last_used_at, created_at, updated_at, deleted_at;

-- name: CreateAgentCapability :one
insert into agent_capabilities(
  id, project_agent_id, capability_id, capability_version_id,
  enabled, configuration, pinning_mode, created_at, updated_at
)
values (
  @id::uuid, @project_agent_id::uuid, @capability_id::uuid,
  @capability_version_id::uuid, @enabled::bool, @configuration::jsonb,
  @pinning_mode::text, @now, @now
)
returning id::text as id, project_agent_id::text as project_agent_id,
  capability_id::text as capability_id, capability_version_id::text as capability_version_id,
  enabled, configuration, pinning_mode, created_at, updated_at;

-- name: GetAgentCapability :one
select id::text as id, project_agent_id::text as project_agent_id,
  capability_id::text as capability_id, capability_version_id::text as capability_version_id,
  enabled, configuration, pinning_mode, created_at, updated_at
from agent_capabilities
where id = @id::uuid;

-- name: ListAgentCapabilitiesByAgent :many
select ac.id::text as id, ac.project_agent_id::text as project_agent_id,
  ac.capability_id::text as capability_id, ac.capability_version_id::text as capability_version_id,
  ac.enabled, ac.configuration, ac.pinning_mode, ac.created_at, ac.updated_at
from agent_capabilities ac
join capability c on c.id = ac.capability_id
where ac.project_agent_id = @project_agent_id::uuid
  and c.deleted_at is null
order by c.name asc;

-- name: UpdateAgentCapability :one
update agent_capabilities
set capability_version_id = @capability_version_id::uuid,
    enabled = @enabled::bool,
    configuration = @configuration::jsonb,
    pinning_mode = @pinning_mode::text,
    updated_at = @now
where id = @id::uuid
returning id::text as id, project_agent_id::text as project_agent_id,
  capability_id::text as capability_id, capability_version_id::text as capability_version_id,
  enabled, configuration, pinning_mode, created_at, updated_at;

-- name: UpgradeAgentCapability :one
update agent_capabilities ac
set capability_version_id = @new_version_id::uuid,
    pinning_mode = @pinning_mode::text,
    updated_at = @now
from capability_version cv, capability c
where ac.project_agent_id = @project_agent_id::uuid
  and ac.capability_id = @capability_id::uuid
  and cv.id = @new_version_id::uuid
  and cv.capability_id = ac.capability_id
  and c.id = ac.capability_id
  and c.visibility = 'public'
  and c.status = 'active'
  and c.deleted_at is null
  and c.deprecated_at is null
returning ac.id::text as id, ac.project_agent_id::text as project_agent_id,
  ac.capability_id::text as capability_id, ac.capability_version_id::text as capability_version_id,
  ac.enabled, ac.configuration, ac.pinning_mode, ac.created_at, ac.updated_at;

-- name: DeleteAgentCapability :exec
delete from agent_capabilities
where id = @id::uuid;

-- name: GetEnabledCapabilitiesForAgent :many
select
  ac.id::text as agent_capability_id,
  ac.project_agent_id::text as project_agent_id,
  ac.enabled,
  ac.configuration,
  ac.pinning_mode,
  c.id::text as capability_id,
  c.workspace_id::text as workspace_id,
  src_ws.name as source_workspace_name,
  c.type,
  c.name,
  c.description,
  c.visibility,
  c.status,
  c.deprecated_at,
  cv.required_credentials,
  cv.id::text as capability_version_id,
  cv.version,
  latest.id::text as latest_version_id,
  latest.version as latest_version,
  latest.created_at as latest_version_created_at,
  -- pinning_mode='latest' 走 latest.* 字段(reupload 后自动跟随);
  -- pinning_mode='pinned' 走 cv.* 字段(锁版本)。两套同时取出,
  -- 让 daemon resolver 不必再发第二次 query。
  latest.oss_key        as latest_oss_key,
  latest.sha256         as latest_sha256,
  latest.canonical_spec as latest_canonical_spec,
  latest.schema_version as latest_schema_version,
  cv.git_repo_url,
  cv.git_ref,
  cv.path,
  cv.content,
  cv.canonical_spec,
  cv.schema_version,
  cv.oss_key,
  cv.sha256,
  c.tags,
  -- Carries c.creator_id so the runtime resolver can enforce the
  -- anti-siphon invariant on the legacy content-fallback path
  -- (no canonical_spec → scan content for owner placeholders). The
  -- import-time validator already blocks cross-user pins on the
  -- write side; this column is the defence-in-depth column that
  -- lets the runtime fail closed if a legacy row slips through.
  coalesce(c.creator_id::text, '')::text as capability_creator_id
from agent_capabilities ac
join capability c on c.id = ac.capability_id
join capability_version cv on cv.id = ac.capability_version_id
join workspaces src_ws on src_ws.id = c.workspace_id
join lateral (
  select id, version, created_at,
    oss_key, sha256, canonical_spec, schema_version
  from capability_version
  where capability_id = c.id
    -- 当 capability 被 deprecated 后,latest binding 应当冻结在
    -- deprecation 之前发布的最新版本,而不是继续自动追 deprecation
    -- 之后的版本(那批已经不在受支持范围内,跟 UpgradeAgentCapability
    -- 显式拒绝 deprecated 升级一致)。c.deprecated_at IS NULL 时
    -- 这一行恒真,等同于原来的"无条件取最新"。
    and (c.deprecated_at is null or capability_version.created_at <= c.deprecated_at)
  order by created_at desc, version desc
  limit 1
) latest on true
where ac.project_agent_id = @project_agent_id::uuid
  and ac.enabled = true
  and c.deleted_at is null
order by c.name asc;

-- ============================================================
-- sandbox pool view over sandboxes (workspace-scoped admin lifecycle)
-- ============================================================
--
-- Pool is in-memory source-of-truth at runtime; these queries write
-- through for admin UI listings and post-restart cleanup. Failure
-- to write does NOT abort the pool path (best-effort; pool.go logs
-- and continues).
--
-- The pool is no longer a physical table. Pool entries are provider
-- sandbox instances in sandboxes with allocation_status='pooled'.
-- Timeout / expiry / auto-renew are sandbox lifecycle attributes, so
-- they live on the same row that later becomes allocation_status='bound'
-- when an agent claims it.

-- name: CreateSandboxPoolEntry :exec
-- Insert when admin batch-spawn creates a fresh blank pool sandbox.
insert into sandboxes(
  id, workspace_id, sandbox_id, template_id,
  lifecycle_status, allocation_status,
  created_at, last_active_at, last_renewed_at,
  expires_at, timeout_seconds
)
values (
  gen_random_uuid(), @workspace_id::uuid, @sandbox_id, @template_id,
  'running', 'pooled',
  @now, @now, @now,
  @expires_at, @timeout_seconds
)
on conflict (sandbox_id) do nothing;

-- name: TouchSandboxPoolRenewed :exec
-- Bump last_renewed_at + roll expires_at forward after a successful
-- Renew (manual or auto). Idempotent against rows killed concurrently.
update sandboxes
set last_renewed_at  = @now,
    last_active_at   = @now,
    expires_at       = @expires_at,
    lifecycle_status = 'running'
where sandbox_id = @sandbox_id
  and killed_at is null;

-- name: SetSandboxPoolAutoRenewThreshold :exec
-- Admin PATCH: set per-sandbox auto-renew threshold. 0 turns it off.
update sandboxes
set auto_renew_threshold_seconds = @threshold_seconds
where sandbox_id = @sandbox_id
  and killed_at is null;

-- name: MarkSandboxPoolEntryClaimed :exec
-- Claim handoff: same sandbox row becomes a bound sandbox for one
-- workspace/project_agent/cache_key. The in-memory pool owns the actual
-- claim selection; this query records the attribution.
update sandboxes
set allocation_status = 'bound',
    project_agent_id   = @project_agent_id::uuid,
    cache_key          = @cache_key,
    last_renewed_at    = @now,
    last_active_at     = @now,
    lifecycle_status   = 'running'
where sandbox_id = @sandbox_id
  and workspace_id = @workspace_id::uuid
  and allocation_status = 'pooled'
  and killed_at is null;

-- name: MarkSandboxPoolEntryKilled :exec
-- Terminal kill for pool/admin paths. Use status 'killed' or
-- 'killed_orphaned'. The row remains as sandbox history.
update sandboxes
set lifecycle_status  = @status,
    allocation_status = 'released',
    killed_at         = @now,
    last_renewed_at   = @now,
    last_active_at    = @now
where sandbox_id = @sandbox_id
  and killed_at is null;

-- name: ListActiveSandboxPoolEntries :many
-- Admin UI page: workspace-scoped sandbox-pool view. Claimed/bound
-- rows stay visible until a real kill so admins can see handoff.
select
  sandbox_id,
  template_id,
  case allocation_status::text
    when 'pooled' then case lifecycle_status::text when 'renewing' then 'renewing' else 'idle' end
    when 'bound' then 'claimed'
    else lifecycle_status::text
  end::text as status,
  created_at,
  coalesce(last_renewed_at, created_at)::timestamptz as last_renewed_at,
  killed_at,
  expires_at,
  timeout_seconds,
  auto_renew_threshold_seconds
from sandboxes
where workspace_id = @workspace_id::uuid
  and killed_at is null
  and allocation_status in ('pooled', 'bound')
order by created_at desc
limit @limit_n::int
offset @offset_n::int;

-- name: CountActiveSandboxPoolEntries :one
select count(*)::bigint
from sandboxes
where workspace_id = @workspace_id::uuid
  and killed_at is null
  and allocation_status in ('pooled', 'bound');

-- name: GetSandboxPoolEntry :one
select
  sandbox_id,
  template_id,
  case allocation_status::text
    when 'pooled' then case lifecycle_status::text when 'renewing' then 'renewing' else 'idle' end
    when 'bound' then 'claimed'
    else lifecycle_status::text
  end::text as status,
  created_at,
  coalesce(last_renewed_at, created_at)::timestamptz as last_renewed_at,
  killed_at,
  expires_at,
  timeout_seconds,
  auto_renew_threshold_seconds
from sandboxes
where sandbox_id = @sandbox_id
  and workspace_id = @workspace_id::uuid;

-- name: ListSandboxPoolEntriesDueForAutoRenew :many
select
  sandbox_id,
  template_id,
  expires_at,
  timeout_seconds,
  auto_renew_threshold_seconds
from sandboxes
where workspace_id = @workspace_id::uuid
  and killed_at is null
  and auto_renew_threshold_seconds > 0
order by expires_at asc;

-- name: SweepOrphanedSandboxPoolEntries :execrows
-- Server startup: every active pooled/bound pool row from the previous
-- lifetime has lost in-memory tracking. Mark them killed_orphaned.
update sandboxes
set lifecycle_status  = 'killed_orphaned',
    allocation_status = 'released',
    killed_at         = @now,
    last_renewed_at   = @now,
    last_active_at    = @now
where killed_at is null
  and allocation_status in ('pooled', 'bound')
  and lifecycle_status in ('running', 'renewing', 'spawning', 'killing');

-- ============================================================
-- 工作区主动申请加入(self-service join request)
--
-- 没有独立的 join_requests 表 —— pending / active / rejected 都是
-- workspace_members 行的不同 status,共享同一份 RBAC / 唯一性 / 软删
-- 语义。新查询只是围绕这套状态机 + visibility 的窗口。
-- ============================================================

-- name: GetWorkspaceMembershipForUser :one
-- 申请前置检查:看 (workspace_id, user_id) 在非 rejected 范围内是否已存在行
-- (跟唯一索引同语义)。返回 status 让 handler 决定是否阻止重复申请 / 提示
-- "已是成员" / "申请审核中"。
select id::text as id, role, status
from workspace_members
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and deleted_at is null
  and status <> 'rejected';

-- name: ListDiscoverableWorkspaces :many
-- 当前用户可以申请加入的 public 工作区:
--   - workspaces.visibility = 'public'
--   - 该用户不是此 workspace 的 active 成员(已经是成员就不该出现在发现列表)
--   - 但 pending(我已申请审核中) 的 workspace 仍要出现,前端通过
--     has_pending_request=true 显示"已申请,等待审批"状态。否则用户
--     一提交申请,workspace 就从下拉消失,体验上是"申请丢了"
--   - rejected 行不阻塞,允许再申请,与 uk_workspace_members_active 一致
-- private 工作区永远不出现在发现列表 —— 列举它们等于泄漏租户存在性。
--
-- 分页 + 搜索:
--   - @search_q 空时跳过模糊匹配;非空时 name ILIKE '%' || q || '%' (大小写不敏感)
--   - 切换器下拉用 limit=5 offset=0 拿首屏;Discover modal 用 limit=20 offset=N 翻页
--   - 总数走另一个 query CountDiscoverableWorkspaces (避免 window function 复杂度)
select
  w.id::text as id,
  w.name,
  w.slug,
  w.visibility,
  w.created_at,
  w.updated_at,
  (select count(*) from workspace_members wm2
     where wm2.workspace_id = w.id
       and wm2.deleted_at is null
       and wm2.status = 'active')::bigint as member_count,
  exists (
    select 1 from workspace_members m
    where m.workspace_id = w.id
      and m.user_id = @user_id::uuid
      and m.deleted_at is null
      and m.status = 'pending'
  ) as has_pending_request
from workspaces w
where w.deleted_at is null
  and w.visibility = 'public'
  and (@search_q::text = '' or w.name ilike '%' || @search_q::text || '%')
  and not exists (
    select 1 from workspace_members m
    where m.workspace_id = w.id
      and m.user_id = @user_id::uuid
      and m.deleted_at is null
      and m.status = 'active'
  )
order by w.created_at desc, w.id asc
limit @item_limit
offset @item_offset;

-- name: CountDiscoverableWorkspaces :one
-- 与 ListDiscoverableWorkspaces 同样的过滤条件,只取总数。前端用它驱动
-- "查看全部 (N)" 标签 + 分页器。注意:这里的"总数"是匹配 search_q 的
-- 总数, 不是平台 public 工作区总数 — 搜索后底部 pager 跟着结果走。
select count(*)::bigint as total
from workspaces w
where w.deleted_at is null
  and w.visibility = 'public'
  and (@search_q::text = '' or w.name ilike '%' || @search_q::text || '%')
  and not exists (
    select 1 from workspace_members m
    where m.workspace_id = w.id
      and m.user_id = @user_id::uuid
      and m.deleted_at is null
      and m.status = 'active'
  );

-- name: ListPendingJoinRequests :many
-- 某 workspace 的待审批申请,带申请人基本信息。
-- 调用方需有 owner / admin 权限(在 handler 层 RBAC 校验)。
select
  wm.id::text as id,
  wm.workspace_id::text as workspace_id,
  wm.user_id::text as user_id,
  coalesce(wm.request_reason, '')::text as request_reason,
  wm.created_at as requested_at,
  u.email as user_email,
  u.name as user_name
from workspace_members wm
join users u on u.id = wm.user_id
where wm.workspace_id = @workspace_id::uuid
  and wm.deleted_at is null
  and wm.status = 'pending'
  and u.deleted_at is null
order by wm.created_at asc, wm.id asc;

-- name: CountPendingJoinRequests :one
-- 审批 Tab 上的 badge:此 workspace 的待审批数量。
select count(*)::bigint as pending_count
from workspace_members
where workspace_id = @workspace_id::uuid
  and deleted_at is null
  and status = 'pending';

-- name: SoftDeleteRejectedJoinRequest :execrows
-- 申请前置:把同一 (workspace_id, user_id) 下残留的 rejected 行清掉,
-- 让接下来的 AddWorkspaceMember(status='pending') 不必应对 rejected
-- 行的复杂复活语义。rejected 行被 deleted_at 标记后仍可作为审计追溯。
update workspace_members
set deleted_at = @now, updated_at = @now
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and status = 'rejected'
  and deleted_at is null;

-- name: WithdrawOwnPendingJoinRequest :execrows
-- 申请人自助撤回自己提交的 pending 申请:把 pending 行 soft-delete
-- (与 SoftDeleteRejectedJoinRequest 同样的 deleted_at 模式)。
-- 守卫:
--   - workspace_id + user_id 必须匹配(handler 用 session user 锁定)
--   - 必须仍是 pending(active 行不能这样删 —— 那是退出工作区,走另一条路)
--   - affected rows = 0 时,handler 返回 404/409 让前端刷新
update workspace_members
set deleted_at = @now, updated_at = @now
where workspace_id = @workspace_id::uuid
  and user_id = @user_id::uuid
  and status = 'pending'
  and deleted_at is null;

-- name: ApproveJoinRequest :one
-- 同意:原子地把 pending → active,记审批人 + 时间。
-- WHERE status='pending' 防双 admin 竞态 —— 第二个 admin 的 UPDATE
-- 影响 0 行,handler 据此返回 409。同时校验 workspace_id 一致,避免
-- 跨 workspace 串号(URL 里的 workspace_id 与行里的 workspace_id 不
-- 一致时拒绝)。
update workspace_members
set status = 'active',
    reviewed_by = @reviewed_by::uuid,
    reviewed_at = @now,
    updated_at = @now
where id = @id::uuid
  and workspace_id = @workspace_id::uuid
  and status = 'pending'
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, status, created_at, updated_at;

-- name: RejectJoinRequest :one
-- 拒绝:pending → rejected。被拒后用户可再次发起申请(SoftDeleteRejectedJoinRequest
-- 会先清掉此行再 INSERT 新 pending)。reviewed_by/reviewed_at 留作审计。
update workspace_members
set status = 'rejected',
    reviewed_by = @reviewed_by::uuid,
    reviewed_at = @now,
    updated_at = @now
where id = @id::uuid
  and workspace_id = @workspace_id::uuid
  and status = 'pending'
  and deleted_at is null
returning id::text as id, workspace_id::text as workspace_id, user_id::text as user_id, role, status, created_at, updated_at;

-- name: GetDiscoverableWorkspaceForJoin :one
-- 申请入口:校验目标 workspace 既存在又是 public 的;非 public 时返回
-- 0 行,handler 据此返回 404(避免私有 workspace 存在性泄露)。
select
  id::text as id,
  name,
  slug,
  visibility
from workspaces
where id = @workspace_id::uuid
  and deleted_at is null
  and visibility = 'public';

-- name: SetProjectAgentRuntime :one
-- Bind (or rebind, or clear) the runtime a project_agent runs on.
--
-- runtime_id NULL clears the binding (turning the agent back into a
-- "needs configuration" state). The FK has ON DELETE SET NULL so the
-- column tolerates orphaned references — the dispatcher still
-- surfaces a friendly "no runtime bound" hint in that case.
--
-- The where-clause includes workspace_id as a tenant guard so an
-- attacker who guesses a project_agent_id from another workspace
-- can't repoint it.
update project_agents
set runtime_id = sqlc.narg('runtime_id')::uuid,
    updated_at = now()
where id = @project_agent_id::uuid
  and workspace_id = @workspace_id::uuid
  and deleted_at is null
returning
  id::text                     as project_agent_id,
  workspace_id::text           as workspace_id,
  project_id::text             as project_id,
  agent_id::text               as agent_id,
  coalesce(runtime_id::text, ''::text)::text as runtime_id,
  status,
  config;

-- name: GetProjectAgentRuntimeBinding :one
-- Read the explicit runtime_id binding for a project_agent. Used by
-- the agent settings page to render the picker's current value.
select
  pa.id::text                                    as project_agent_id,
  pa.workspace_id::text                          as workspace_id,
  coalesce(pa.runtime_id::text, ''::text)::text  as runtime_id
from project_agents pa
where pa.id = @project_agent_id::uuid
  and pa.workspace_id = @workspace_id::uuid
  and pa.deleted_at is null;

-- name: ResolveAgentNameForConversation :one
-- Returns the display name of the Agent bound to a conversation, used
-- as the per-card header title fallback when the caller has no
-- agent_run row in hand (e.g. NoticeCard sent from sendImmediateText
-- for /list / /help; CredentialFormRejected / PermissionResult patches
-- fired from the inbound callback path).
--
-- The Agent is keyed off conversations.metadata.primary_agent_id (set
-- at conversation-create time by CreateInboundIMMessage), which holds
-- a project_agents.id. We join through project_agents to agents to
-- pick up the display name. conversations has no selected_agent_id
-- column — that field lives on gateway_sessions, which only the
-- shared-bot /select flow writes to and is unrelated to "what Agent is
-- this conversation talking to".
--
-- Returns empty string when:
--   - the conversation doesn't exist (caller treats as "fall back to
--     brand title"),
--   - metadata.primary_agent_id is NULL / empty (system-initiated
--     conversation that never bound to an Agent),
--   - the project_agent / agent row was soft-deleted.
--
-- LEFT JOINs keep the row even on those degenerate cases so the
-- caller gets ('', nil) instead of pgx.ErrNoRows, simplifying the
-- "missing → fallback" branch at every call site.
select coalesce(a.name, '')::text as agent_name
from conversations c
left join project_agents pa
  on pa.id = nullif(c.metadata->>'primary_agent_id', '')::uuid
 and pa.deleted_at is null
left join agents a
  on a.id = pa.agent_id
 and a.deleted_at is null
where c.id = @conversation_id::uuid;

-- ============================================================
-- scheduled_tasks
-- ============================================================

-- name: CreateScheduledTask :one
insert into scheduled_tasks(
  id, project_agent_id, name, prompt, cron_expr, timezone,
  enabled, feishu_chat_id, feishu_chat_name, next_run_at, created_by, created_at, updated_at
)
values (@id::uuid, @project_agent_id::uuid, @name, @prompt, @cron_expr, @timezone,
        @enabled, @feishu_chat_id, @feishu_chat_name, @next_run_at, @created_by, @now, @now)
returning
  id::text                                  as id,
  project_agent_id::text                    as project_agent_id,
  coalesce(conversation_id::text, '')::text as conversation_id,
  name, prompt, cron_expr, timezone, enabled,
  coalesce(feishu_chat_id, '')::text        as feishu_chat_id,
  coalesce(feishu_chat_name, '')::text      as feishu_chat_name,
  next_run_at, last_run_at,
  coalesce(last_run_id::text, '')::text     as last_run_id,
  last_status, consecutive_failures,
  coalesce(created_by::text, '')::text      as created_by,
  created_at, updated_at;

-- name: GetScheduledTask :one
select
  t.id::text                                  as id,
  t.project_agent_id::text                    as project_agent_id,
  coalesce(t.conversation_id::text, '')::text as conversation_id,
  t.name, t.prompt, t.cron_expr, t.timezone, t.enabled,
  coalesce(t.feishu_chat_id, '')::text        as feishu_chat_id,
  coalesce(t.feishu_chat_name, '')::text      as feishu_chat_name,
  t.next_run_at, t.last_run_at,
  coalesce(t.last_run_id::text, '')::text     as last_run_id,
  -- Derive the display status from the linked run's live status so the
  -- list/detail never get stuck on the 'queued' dispatch stamp. Task-level
  -- states (skipped_overlap / auto_disabled) take precedence when set.
  coalesce(
    case when t.last_status in ('skipped_overlap', 'auto_disabled') then t.last_status
         else coalesce(r.status, t.last_status) end,
    ''
  )::text                                     as last_status,
  t.consecutive_failures,
  coalesce(t.created_by::text, '')::text      as created_by,
  t.created_at, t.updated_at
from scheduled_tasks t
left join agent_runs r on r.id = t.last_run_id
where t.id = @id::uuid and t.deleted_at is null;

-- name: ListScheduledTasksByProjectAgent :many
select
  t.id::text                                  as id,
  t.project_agent_id::text                    as project_agent_id,
  coalesce(t.conversation_id::text, '')::text as conversation_id,
  t.name, t.prompt, t.cron_expr, t.timezone, t.enabled,
  coalesce(t.feishu_chat_id, '')::text        as feishu_chat_id,
  coalesce(t.feishu_chat_name, '')::text      as feishu_chat_name,
  t.next_run_at, t.last_run_at,
  coalesce(t.last_run_id::text, '')::text     as last_run_id,
  coalesce(
    case when t.last_status in ('skipped_overlap', 'auto_disabled') then t.last_status
         else coalesce(r.status, t.last_status) end,
    ''
  )::text                                     as last_status,
  t.consecutive_failures,
  coalesce(t.created_by::text, '')::text      as created_by,
  t.created_at, t.updated_at
from scheduled_tasks t
left join agent_runs r on r.id = t.last_run_id
where t.project_agent_id = @project_agent_id::uuid and t.deleted_at is null
order by t.created_at desc;

-- name: ListScheduledTasksByProjectPage :many
-- Project-wide list (paginated): every scheduled task across all of the
-- project's agents, newest first. last_status is derived from the linked run
-- (see GetScheduledTask). The (created_at, id) tie-break keeps OFFSET paging
-- stable; pair with CountScheduledTasksByProject for the page count.
select
  t.id::text                                  as id,
  t.project_agent_id::text                    as project_agent_id,
  coalesce(t.conversation_id::text, '')::text as conversation_id,
  t.name, t.prompt, t.cron_expr, t.timezone, t.enabled,
  coalesce(t.feishu_chat_id, '')::text        as feishu_chat_id,
  coalesce(t.feishu_chat_name, '')::text      as feishu_chat_name,
  t.next_run_at, t.last_run_at,
  coalesce(t.last_run_id::text, '')::text     as last_run_id,
  coalesce(
    case when t.last_status in ('skipped_overlap', 'auto_disabled') then t.last_status
         else coalesce(r.status, t.last_status) end,
    ''
  )::text                                     as last_status,
  t.consecutive_failures,
  coalesce(t.created_by::text, '')::text      as created_by,
  t.created_at, t.updated_at
from scheduled_tasks t
join project_agents pa on pa.id = t.project_agent_id
left join agent_runs r on r.id = t.last_run_id
where pa.project_id = @project_id::uuid and t.deleted_at is null
order by t.created_at desc, t.id desc
limit @item_limit offset @item_offset;

-- name: CountScheduledTasksByProject :one
-- Companion to ListScheduledTasksByProjectPage: total rows under the same
-- filter so the pager can render "第 X-Y 条,共 N 条" and gate the Next button.
select count(*)::bigint as total
from scheduled_tasks t
join project_agents pa on pa.id = t.project_agent_id
where pa.project_id = @project_id::uuid and t.deleted_at is null;

-- name: GetScheduledTaskScope :one
-- Resolve workspace/project/project_agent for RBAC gating from a task id.
select
  t.id::text             as id,
  pa.id::text            as project_agent_id,
  pa.project_id::text    as project_id,
  pa.workspace_id::text  as workspace_id
from scheduled_tasks t
join project_agents pa on pa.id = t.project_agent_id
where t.id = @id::uuid and t.deleted_at is null;

-- name: UpdateScheduledTask :one
-- Re-enabling clears the failure state that tripped auto-disable. Without this,
-- a task auto-disabled at the failure threshold keeps consecutive_failures >=
-- threshold and last_status='auto_disabled'; the next cron fire would re-count
-- the prior failed run and re-disable before dispatching, so flipping enabled
-- back on via the UI would never actually run. Scoped to the disabled->enabled
-- transition (old enabled=false, new enabled=true) so editing an already-
-- enabled task doesn't wipe a meaningful in-flight failure count. last_run_id
-- is intentionally left intact so the self-overlap guard still sees an active
-- prior run.
update scheduled_tasks
set name = @name,
    prompt = @prompt,
    cron_expr = @cron_expr,
    timezone = @timezone,
    enabled = @enabled,
    next_run_at = @next_run_at,
    consecutive_failures = case
      when enabled = false and @enabled = true then 0
      else consecutive_failures
    end,
    last_status = case
      when enabled = false and @enabled = true then ''
      else last_status
    end,
    updated_at = @now::timestamptz
where id = @id::uuid and deleted_at is null
returning
  id::text                                  as id,
  project_agent_id::text                    as project_agent_id,
  coalesce(conversation_id::text, '')::text as conversation_id,
  name, prompt, cron_expr, timezone, enabled,
  coalesce(feishu_chat_id, '')::text        as feishu_chat_id,
  coalesce(feishu_chat_name, '')::text      as feishu_chat_name,
  next_run_at, last_run_at,
  coalesce(last_run_id::text, '')::text     as last_run_id,
  last_status, consecutive_failures,
  coalesce(created_by::text, '')::text      as created_by,
  created_at, updated_at;

-- name: SoftDeleteScheduledTask :exec
update scheduled_tasks
set deleted_at = @now::timestamptz, updated_at = @now::timestamptz
where id = @id::uuid and deleted_at is null;

-- name: ClaimDueScheduledTasks :many
-- Multi-pod-safe: FOR UPDATE OF t SKIP LOCKED so sibling pods get disjoint
-- batches; claim lease (claimed_at/claimed_by) recovers crashed claims.
-- Mirrors ClaimPendingQueuedFeishuRuns. Returns only what the scheduler
-- needs to compute next_run_at; FireScheduledTaskRun re-reads FOR UPDATE.
with picked as (
  select t.id
  from scheduled_tasks t
  where t.enabled = true
    and t.deleted_at is null
    and t.next_run_at is not null
    and t.next_run_at <= @now::timestamptz
    and (
      t.claimed_at is null
      or t.claimed_at < @stale_before::timestamptz
      or t.claimed_by = @claimed_by::text
    )
  order by t.next_run_at asc
  limit @item_limit
  for update of t skip locked
),
claimed as (
  update scheduled_tasks t
  set claimed_at = @now::timestamptz,
      claimed_by = @claimed_by::text,
      updated_at = @now::timestamptz
  from picked
  where t.id = picked.id
  returning t.id, t.cron_expr, t.timezone
)
select claimed.id::text   as id,
       claimed.cron_expr  as cron_expr,
       claimed.timezone   as timezone
from claimed;

-- name: GetScheduledTaskForUpdate :one
-- Row-lock the task and read the last run's terminal status for the
-- self-overlap + failure-accounting decision in FireScheduledTaskRun.
select
  t.id::text                                   as id,
  t.project_agent_id::text                     as project_agent_id,
  coalesce(t.conversation_id::text, '')::text  as conversation_id,
  t.name                                       as name,
  t.prompt                                     as prompt,
  t.timezone                                   as timezone,
  t.enabled                                    as enabled,
  t.consecutive_failures                       as consecutive_failures,
  coalesce(t.last_run_id::text, '')::text      as last_run_id,
  coalesce(r.status, '')::text                 as last_run_status,
  coalesce(t.created_by::text, '')::text       as created_by
from scheduled_tasks t
left join agent_runs r on r.id = t.last_run_id
where t.id = @id::uuid and t.deleted_at is null
for update of t;

-- name: CreateScheduledAgentRun :exec
-- 定时 run: trigger_source='scheduled_task', trigger_channel='cron',
-- trigger_ref_type='scheduled_task', trigger_ref_id=task.id。
-- 执行身份 = 创建者 (requested_by_type='user', requested_by_id=created_by)。
insert into agent_runs(
  id, workspace_id, project_id, conversation_id,
  trigger_message_id, trigger_source, trigger_channel, trigger_ref_type, trigger_ref_id,
  requested_by_type, requested_by_id,
  project_agent_id, connector_type, status, visibility, metadata,
  created_at, updated_at
)
values (@id::uuid, @workspace_id::uuid, @project_id::uuid, @conversation_id::uuid,
        @trigger_message_id::uuid, 'scheduled_task', 'cron', 'scheduled_task', @trigger_ref_id::uuid,
        'user', @requested_by_id,
        @project_agent_id::uuid, @connector_type, 'queued', 'project', @metadata::jsonb, @now, @now);

-- name: MarkScheduledTaskDispatched :exec
-- After a cron dispatch: stamp last run, set consecutive_failures to the
-- recomputed value, advance next_run_at, release claim.
update scheduled_tasks
set last_run_at = @now::timestamptz,
    last_run_id = @last_run_id::uuid,
    conversation_id = @conversation_id::uuid,
    last_status = 'queued',
    consecutive_failures = @consecutive_failures::int,
    next_run_at = @next_run_at,
    claimed_at = null,
    claimed_by = '',
    updated_at = @now::timestamptz
where id = @id::uuid;

-- name: AdvanceScheduledTaskAfterSkip :exec
-- Self-overlap skip: advance next_run_at, release claim, no run dispatched.
update scheduled_tasks
set next_run_at = @next_run_at,
    last_status = 'skipped_overlap',
    claimed_at = null,
    claimed_by = '',
    updated_at = @now::timestamptz
where id = @id::uuid;

-- name: DisableScheduledTaskForFailures :exec
-- Threshold reached: auto-disable, keep next_run_at advanced for re-enable.
update scheduled_tasks
set enabled = false,
    last_status = 'auto_disabled',
    consecutive_failures = @consecutive_failures::int,
    next_run_at = @next_run_at,
    claimed_at = null,
    claimed_by = '',
    updated_at = @now::timestamptz
where id = @id::uuid;

-- name: MarkScheduledTaskRunNow :exec
-- run-now is out-of-band: stamp last run only, DO NOT touch next_run_at
-- or consecutive_failures.
update scheduled_tasks
set last_run_at = @now::timestamptz,
    last_run_id = @last_run_id::uuid,
    conversation_id = @conversation_id::uuid,
    last_status = 'queued',
    updated_at = @now::timestamptz
where id = @id::uuid;

-- name: ListAgentRunsByScheduledTask :many
select
  id::text                                   as id,
  conversation_id::text                      as conversation_id,
  project_agent_id::text                     as project_agent_id,
  connector_type,
  status,
  failure_reason,
  trigger_source,
  trigger_channel,
  coalesce(trigger_ref_id::text, '')::text   as trigger_ref_id,
  created_at, started_at, finished_at, updated_at
from agent_runs
where trigger_ref_type = 'scheduled_task' and trigger_ref_id = @task_id::uuid
order by created_at desc
limit @item_limit;



-- ============================================================
-- workspace_im_connectors — workspace 维度 IM 连接器(feishu/slack/discord)
-- 见 migration 000002。凭据密文在 secrets(vault),本表 config 只存
-- *_ref 与非敏感字段。app_id 是 workspace-bot 的通用 join key。
-- ============================================================

-- name: UpsertWorkspaceIMConnector :one
-- 按 (workspace_id, platform) 唯一约束 upsert。冲突时更新 app_id /
-- enabled / config / updated_at,保留 id / created_by / created_at。
-- 若 (platform, app_id) 撞了别的 workspace,会触发 uk_wic_platform_appid
-- 唯一冲突并报错(由 store 层映射成 *_app_id_in_use)。
insert into workspace_im_connectors (
  id, workspace_id, platform, app_id, enabled, config, created_by, created_at, updated_at
) values (
  @id::uuid, @workspace_id::uuid, @platform::text, @app_id::text,
  @enabled::boolean, @config, nullif(@created_by::text, '')::uuid, @now, @now
)
on conflict (workspace_id, platform) where deleted_at is null
do update set
  app_id     = excluded.app_id,
  enabled    = excluded.enabled,
  config     = excluded.config,
  updated_at = excluded.updated_at
returning
  id::text, workspace_id::text, platform, app_id, enabled, config,
  created_at, updated_at;

-- name: GetWorkspaceIMConnectors :many
-- 拉取某 workspace 全部平台的有效连接器(前端面板初始化用)。
select
  id::text, workspace_id::text, platform, app_id, enabled, config,
  created_at, updated_at
from workspace_im_connectors
where workspace_id = @workspace_id::uuid
  and deleted_at is null
order by platform;

-- name: GetWorkspaceConnectorByAppID :one
-- 出站 resolver 按 (platform, app_id) 反查启用的连接器,取 config 里的
-- *_ref 解密 token。
select
  c.id::text, c.workspace_id::text, w.name as workspace_name,
  c.platform, c.app_id, c.enabled, c.config, c.created_at, c.updated_at
from workspace_im_connectors c
join workspaces w on w.id = c.workspace_id
where c.platform = @platform::text
  and c.app_id = @app_id::text
  and c.enabled = true
  and c.deleted_at is null
limit 1;

-- name: ListWorkspaceConnectorsByPlatform :many
-- 入站 reconciler 按平台扫描所有启用的连接器,为每条 (workspace_id,
-- app_id) 维持一条长连接。
select
  c.id::text, c.workspace_id::text, w.name as workspace_name,
  c.platform, c.app_id, c.enabled, c.config, c.created_at, c.updated_at
from workspace_im_connectors c
join workspaces w on w.id = c.workspace_id
where c.platform = @platform::text
  and c.enabled = true
  and c.app_id <> ''
  and c.deleted_at is null
order by c.workspace_id, c.app_id;
