-- runtimes.sql — runtime lifecycle and Agent Daemon pairing queries.
--
-- Scope: SQL backing the runtimes table: admin lifecycle, daemon
-- pairing, runtime credential heartbeat, and agent_daemon heartbeat
-- capability persistence. Conventions follow the existing store.sql
-- (uuid cast to text on the way out, jsonb cast for write-side params,
-- @now/@id parameter naming).

-- name: CreateRuntimePairing :one
-- Admin UI calls this to register a new Agent Daemon runtime in
-- pending_pairing state. The pairing token is generated server-side,
-- hashed (sha256 hex) and stored here; the plaintext is returned to
-- the caller exactly once and never persisted. Pairing window is
-- short (typically 15min) so unused rows can be reaped without
-- breaking active pairings.
insert into runtimes(
  id, workspace_id, type, name, liveness, provider,
  owner_user_id, version, hostname,
  last_heartbeat_at,
  pairing_token_hash, pairing_token_expires_at,
  config, created_at, updated_at, deleted_at
)
values (
  @id::uuid, @workspace_id::uuid, @type, @name, 'pending_pairing', @provider,
  sqlc.narg('owner_user_id')::uuid, '', '',
  null,
  @pairing_token_hash, @pairing_token_expires_at,
  @config::jsonb, @now, @now, null
)
returning
  id::text                       as id,
  workspace_id::text             as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  version,
  hostname,
  last_heartbeat_at,
  pairing_token_expires_at,
  config,
  created_at,
  updated_at;

-- name: ConsumePairingToken :one
-- Daemon-side: presented token hash matches and not expired ->
-- promote pending_pairing -> offline (will go online on first
-- heartbeat). Atomically clears the pairing token columns and
-- writes the daemon-supplied hostname/version + X25519 public key
-- (config jsonb). Returns the activated runtime so the daemon can
-- learn its runtime_id + remember its credential.
--
-- config jsonb is merged via concat (||) so admin-set keys set at
-- create-pairing time survive the pair handshake — matches the
-- SetRuntimeRunnerCredentialHash pattern in this file (round-1
-- review F2).
update runtimes
set liveness                 = 'offline',
    hostname                 = @hostname,
    version                  = @version,
    config                   = coalesce(config, '{}'::jsonb) || @config::jsonb,
    pairing_token_hash       = null,
    pairing_token_expires_at = null,
    updated_at               = @now
where pairing_token_hash         = @pairing_token_hash
  and liveness                   = 'pending_pairing'
  and pairing_token_expires_at   > @now
  and deleted_at                 is null
returning
  id::text          as id,
  workspace_id::text as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  hostname,
  version,
  config,
  created_at,
  updated_at;

-- name: TouchRuntimeHeartbeat :one
-- Bumps last_heartbeat_at and transitions the runtime to 'online'
-- if it was offline/error. Soft-deleted runtimes are not promoted.
-- Returns the new liveness.
update runtimes
set last_heartbeat_at = @now,
    liveness          = 'online',
    updated_at        = @now
where id          = @id::uuid
  and deleted_at  is null
  and liveness    in ('offline', 'online', 'error')
returning
  id::text       as id,
  liveness,
  last_heartbeat_at;

-- name: TouchAgentDaemonHeartbeat :one
-- WebSocket agent_daemon heartbeat: bump liveness and persist the
-- daemon-advertised agent_kind capability snapshot. This keeps the
-- Runtime page and DevicePicker on the same source of truth without a
-- new table/migration; agent_daemon is already the runtime type that
-- owns these config keys.
update runtimes
set last_heartbeat_at = @now,
    liveness          = 'online',
    version           = case when @daemon_version::text = '' then version else @daemon_version end,
    config            = coalesce(config, '{}'::jsonb) || jsonb_build_object(
      'supported_agent_kinds', @supported_agent_kinds::jsonb,
      'supported_agent_kind_names', @supported_agent_kind_names::jsonb,
      'daemon_capabilities', @daemon_capabilities::jsonb,
      'agent_daemon_active_requests', @active_requests::int,
      'agent_daemon_heartbeat_ts', @heartbeat_ts::bigint
    ),
    updated_at        = @now
where id          = @id::uuid
  and deleted_at  is null
  and type        = 'agent_daemon'
  and liveness    in ('offline', 'online', 'error')
returning
  id::text       as id,
  liveness,
  last_heartbeat_at;

-- name: GetRuntime :one
-- Single runtime fetch (admin detail page + runner self-query).
-- Includes deleted=false guard so soft-deleted runtimes are
-- invisible to API surface; bypass via internal callers if ever
-- needed (not in MVP).
select
  id::text          as id,
  workspace_id::text as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  version,
  hostname,
  last_heartbeat_at,
  pairing_token_expires_at,
  config,
  created_at,
  updated_at
from runtimes
where id         = @id::uuid
  and deleted_at is null;

-- name: GetRuntimeByCredentialHash :one
-- Bearer-only lookup for the runner_credential middleware. Resolves
-- a long-lived runtime credential (sha256 hex) to its owning runtime
-- without a URL/header-supplied runtime id — the daemon CLI's only
-- credential dimension is the bearer.
--
-- Filters mirror the existing bearer auth (deleted=false) so a leaked
-- credential cannot resurrect a removed sandbox. Sequential scan today
-- (no functional index on config->>'runner_credential_hash'); MVP scale
-- of low-hundreds of active runtimes per workspace makes it cheap.
-- Promote to a CREATE INDEX … ((config->>'runner_credential_hash'))
-- when call volume warrants it.
select
  id::text          as id,
  workspace_id::text as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  version,
  hostname,
  last_heartbeat_at,
  pairing_token_expires_at,
  config,
  created_at,
  updated_at
from runtimes
where config->>'runner_credential_hash' = @credential_hash::text
  and deleted_at                         is null
limit 1;

-- name: ListRuntimesByWorkspace :many
-- Admin UI Runtime page list. Filters by optional type so the
-- per-tab views (Agent Daemon / Sandbox / External) reuse one query.
-- Empty string in @type means "all types".
select
  id::text          as id,
  workspace_id::text as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  version,
  hostname,
  last_heartbeat_at,
  pairing_token_expires_at,
  config,
  created_at,
  updated_at
from runtimes
where workspace_id = @workspace_id::uuid
  and deleted_at   is null
  and (@type = '' or type::text = @type)
order by created_at desc
limit @limit_n::int;

-- name: PatchRuntime :one
-- Admin PATCH: rename. Empty string means "do not change" (preserves
-- the current value).
update runtimes
set name            = case when @new_name::text          = '' then name            else @new_name end,
    updated_at      = @now
where id         = @id::uuid
  and deleted_at is null
returning
  id::text          as id,
  workspace_id::text as workspace_id,
  type,
  name,
  liveness,
  provider,
  owner_user_id,
  version,
  hostname,
  last_heartbeat_at,
  pairing_token_expires_at,
  config,
  created_at,
  updated_at;

-- name: SoftDeleteRuntime :exec
-- Admin "remove". Soft-delete only; agent_runs.runtime_id stays
-- as the historical FK (the FK is ON DELETE SET NULL, but soft
-- delete leaves it intact so Run Detail can still display "ran
-- on alice-laptop (deleted)").
update runtimes
set deleted_at  = @now,
    updated_at  = @now
where id         = @id::uuid
  and deleted_at is null;

-- name: SoftDeleteRuntimeByWorkspaceName :exec
-- Retire any active runtime with the given workspace+name so a
-- replacement can be created without hitting the
-- uk_runtimes_workspace_name_active unique constraint.
update runtimes
set deleted_at  = @now,
    updated_at  = @now
where workspace_id = @workspace_id::uuid
  and name         = @name
  and deleted_at   is null;

-- name: SweepStaleRuntimesToOffline :execrows
-- Background sweeper: runtimes whose last_heartbeat_at is older
-- than the cutoff get demoted online -> offline. Operator picks
-- the cutoff (typically 3-5x the runner heartbeat interval, e.g.
-- runner ticks every 10s => cutoff 30-60s). Returns row count
-- for sweep telemetry.
update runtimes
set liveness   = 'offline',
    updated_at = @now
where deleted_at        is null
  and liveness          = 'online'
  and last_heartbeat_at < @cutoff;

-- name: MarkRuntimeOffline :exec
-- Instant offline marking on WebSocket session close. Idempotent: only
-- touches rows that are currently online.
UPDATE runtimes
SET liveness   = 'offline',
    updated_at = @now
WHERE id         = @id::uuid
  AND liveness   = 'online'
  AND deleted_at IS NULL;

-- name: SetRuntimeRunnerCredentialHash :exec
-- Pair handshake follow-up: after ConsumePairingToken promotes the
-- row to offline + writes runner_public_key, we mint a long-lived
-- runtime bearer credential and stash its hash under
-- config.runner_credential_hash. jsonb-concat preserves runner_public_key
-- + any other admin-set keys; written as one round trip so the daemon
-- never sees a partially-paired row.
update runtimes
set config     = coalesce(config, '{}'::jsonb) || jsonb_build_object('runner_credential_hash', @hash::text),
    updated_at = @now
where id         = @id::uuid
  and deleted_at is null;
