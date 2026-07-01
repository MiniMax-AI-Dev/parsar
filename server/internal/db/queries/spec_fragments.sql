-- spec_fragments.sql — workspace-scoped spec fragments backing the
-- spec & memory injection feature (see docs/spec-memory-module.md).
--
-- Conventions follow audit_records.sql / runtimes.sql in this dir:
--   * application-layer @id::uuid + @now::timestamptz (no DB defaults
--     for id/created_at/updated_at per migration 000004)
--   * @param::type casts on every parameter for sqlc pgx/v5 codegen
--   * empty-string / empty-array sentinels mean "skip this filter"
--     so list endpoints can reuse one query for unfiltered + filtered
--     views without proliferating query variants
--   * source / created_by / agent_actor write-source triple is
--     validated in the Go service layer (specmemory.Source enum);
--     SQL just stores whatever the caller passes
--
-- Soft delete is the only delete: idx_spec_fragments_workspace_active
-- is partial WHERE deleted_at IS NULL so reads stay fast as the table
-- accumulates tombstones. List queries always filter `deleted_at is null`.

-- name: InsertSpecFragment :one
-- Single insert path for both UI ("manual"/"import") and agent
-- ("agent") writes. Caller is responsible for setting created_by
-- (UI: signed-in user; agent: pgtype.UUID{Valid:false}) and
-- agent_actor (UI: ''; agent: 'connector:agentID').
insert into spec_fragments (
  id, workspace_id, title, body, tags,
  source, created_by, agent_actor,
  created_at, updated_at
)
values (
  @id::uuid, @workspace_id::uuid, @title, @body, @tags::text[],
  @source, sqlc.narg('created_by')::uuid, @agent_actor,
  @now, @now
)
returning
  id::text           as id,
  workspace_id::text as workspace_id,
  title,
  body,
  tags,
  source,
  created_by,
  agent_actor,
  created_at,
  updated_at;

-- name: GetSpecFragment :one
-- Single fragment fetch (UI detail page + audit replay). Filters
-- soft-deleted rows; admin/forensic reads should use a separate
-- query that drops the deleted_at guard if ever needed.
select
  id::text           as id,
  workspace_id::text as workspace_id,
  title,
  body,
  tags,
  source,
  created_by,
  agent_actor,
  created_at,
  updated_at
from spec_fragments
where id         = @id::uuid
  and deleted_at is null;

-- name: ListWorkspaceSpecFragments :many
-- Primary list query for both UI (Spec tab) and SessionStart
-- injection snapshot. Filters:
--   * @source::text — empty string skips; otherwise exact match
--     (e.g. only "agent"-authored fragments)
--   * @tag_filter::text[] — empty array skips; otherwise overlap
--     (tags && filter), backed by idx_spec_fragments_tags_active
-- Order by updated_at desc so the freshest fragments dominate the
-- injection budget when callers cap with @item_limit.
select
  id::text           as id,
  workspace_id::text as workspace_id,
  title,
  body,
  tags,
  source,
  created_by,
  agent_actor,
  created_at,
  updated_at
from spec_fragments
where workspace_id = @workspace_id::uuid
  and deleted_at   is null
  and (@source::text = '' or source = @source::text)
  and (cardinality(@tag_filter::text[]) = 0
       or tags && @tag_filter::text[])
order by updated_at desc, id desc
limit @item_limit::int;

-- name: ListWorkspaceSpecFragmentsSince :many
-- Per-turn incremental injection cursor. Returns fragments whose
-- updated_at is strictly greater than the cursor — covers both new
-- inserts and edits to existing rows. Soft-deleted rows are excluded
-- here; deletions are surfaced via a separate query if the per-turn
-- hook ever needs to retract a previously-injected fragment.
select
  id::text           as id,
  workspace_id::text as workspace_id,
  title,
  body,
  tags,
  source,
  created_by,
  agent_actor,
  created_at,
  updated_at
from spec_fragments
where workspace_id = @workspace_id::uuid
  and deleted_at   is null
  and updated_at   > @since::timestamptz
order by updated_at asc, id asc
limit @item_limit::int;

-- name: UpdateSpecFragment :one
-- Full-replace update (UI edits + agent rewrites). Caller does
-- read-modify-write; this query is a dumb writer that bumps
-- updated_at. Soft-deleted rows are immutable (deleted_at guard).
-- We deliberately do NOT let updates change source/created_by/
-- agent_actor — provenance is fixed at insert time; subsequent
-- edits inherit the original triple.
update spec_fragments
set title      = @title,
    body       = @body,
    tags       = @tags::text[],
    updated_at = @now
where id         = @id::uuid
  and deleted_at is null
returning
  id::text           as id,
  workspace_id::text as workspace_id,
  title,
  body,
  tags,
  source,
  created_by,
  agent_actor,
  created_at,
  updated_at;

-- name: SoftDeleteSpecFragment :exec
-- Tombstone the fragment. Idempotent on already-deleted rows (no
-- match -> no update). Hard delete is intentionally absent: audit
-- replay needs the body around to render historical injections.
update spec_fragments
set deleted_at = @now,
    updated_at = @now
where id         = @id::uuid
  and deleted_at is null;
