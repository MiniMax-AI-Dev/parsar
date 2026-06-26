-- memories.sql — user- and project-scoped memory rows backing the
-- spec & memory injection feature (see docs/spec-memory-module.md).
--
-- Conventions match spec_fragments.sql in this dir:
--   * application-layer @id::uuid + @now::timestamptz
--   * @param::type casts on every parameter
--   * empty-string / empty-array sentinels skip the corresponding
--     filter so one list query covers unfiltered + filtered views
--   * source / scope / memory_type strings are validated by the Go
--     service layer (specmemory.{Source,Scope,MemoryType} enums);
--     SQL only enforces the scope↔project_id structural CHECK from
--     migration 000004
--
-- Two-rail design: user-scope rows live alongside this user only
-- (idx_memories_user_scope_active); project-scope rows are shared
-- across all users on the project (idx_memories_project_active).
-- Per-turn incremental injection therefore uses two separate
-- ListXxxSince queries so callers can't accidentally bleed one
-- user's user-scope memory into another user's session.

-- name: InsertMemory :one
-- Single insert path for both UI ("user") and agent ("agent") writes.
-- Caller is responsible for:
--   * setting project_id (pgtype.UUID{Valid:false} when scope='user';
--     concrete UUID when scope='project') — the table CHECK enforces
--     the pairing
--   * agent_actor ('' for human; 'connector:projectAgentID' for agent)
--   * conversation_id (pgtype.UUID{Valid:false} unless the write
--     originated inside an agent turn)
insert into memories (
  id, scope, user_id, project_id, memory_type,
  title, body, why, tags,
  source, agent_actor, conversation_id,
  created_at, updated_at
)
values (
  @id::uuid, @scope, @user_id::uuid, sqlc.narg('project_id')::uuid, @memory_type,
  @title, @body, @why, @tags::text[],
  @source, @agent_actor, sqlc.narg('conversation_id')::uuid,
  @now, @now
)
returning
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at;

-- name: GetMemory :one
-- Single memory fetch (UI detail page + audit replay). Filters
-- soft-deleted rows.
select
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at
from memories
where id         = @id::uuid
  and deleted_at is null;

-- name: ListUserMemories :many
-- Per-user listing for the user-scope Memory tab in UI and for
-- SessionStart snapshot injection. Filters:
--   * @memory_type::text — empty string skips; otherwise exact
--     match (e.g. only 'feedback' rows)
--   * @tag_filter::text[] — empty array skips; otherwise overlap
-- Order updated_at desc so freshest memories dominate when callers
-- cap with @item_limit.
select
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at
from memories
where user_id     = @user_id::uuid
  and scope       = 'user'
  and deleted_at  is null
  and (@memory_type::text = '' or memory_type = @memory_type::text)
  and (cardinality(@tag_filter::text[]) = 0
       or tags && @tag_filter::text[])
order by updated_at desc, id desc
limit @item_limit::int;

-- name: ListProjectMemories :many
-- Per-project listing (shared across all users on the project) for
-- the project-scope Memory tab and SessionStart snapshot injection.
-- Same filter sentinels as ListUserMemories.
select
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at
from memories
where project_id  = @project_id::uuid
  and scope       = 'project'
  and deleted_at  is null
  and (@memory_type::text = '' or memory_type = @memory_type::text)
  and (cardinality(@tag_filter::text[]) = 0
       or tags && @tag_filter::text[])
order by updated_at desc, id desc
limit @item_limit::int;

-- name: ListUserMemoriesSince :many
-- Per-turn incremental cursor for user-scope memories. Returns
-- rows whose updated_at is strictly greater than the cursor so
-- both new inserts and edits surface. Soft-deleted rows are
-- excluded; per-turn deletion retraction is a separate query if
-- needed later.
select
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at
from memories
where user_id     = @user_id::uuid
  and scope       = 'user'
  and deleted_at  is null
  and updated_at  > @since::timestamptz
order by updated_at asc, id asc
limit @item_limit::int;

-- name: ListProjectMemoriesSince :many
-- Per-turn incremental cursor for project-scope memories.
-- Mirrors ListUserMemoriesSince but scoped by project_id so a
-- session bound to project X cannot pick up memories from project Y.
select
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at
from memories
where project_id  = @project_id::uuid
  and scope       = 'project'
  and deleted_at  is null
  and updated_at  > @since::timestamptz
order by updated_at asc, id asc
limit @item_limit::int;

-- name: UpdateMemory :one
-- Full-replace update of the editable fields. Provenance fields
-- (scope/user_id/project_id/memory_type/source/agent_actor/
-- conversation_id) are fixed at insert time and intentionally not
-- editable — a "moved" memory should be deleted + re-inserted so
-- audit history stays intact. memory_type is excluded because a
-- type change usually means the user meant a different memory and
-- should re-author it.
update memories
set title      = @title,
    body       = @body,
    why        = @why,
    tags       = @tags::text[],
    updated_at = @now
where id         = @id::uuid
  and deleted_at is null
returning
  id::text           as id,
  scope,
  user_id::text      as user_id,
  project_id,
  memory_type,
  title,
  body,
  why,
  tags,
  source,
  agent_actor,
  conversation_id,
  created_at,
  updated_at;

-- name: SoftDeleteMemory :exec
-- Tombstone the memory. Idempotent on already-deleted rows.
-- Hard delete is intentionally absent: audit replay needs the body
-- to render historical injections.
update memories
set deleted_at = @now,
    updated_at = @now
where id         = @id::uuid
  and deleted_at is null;
