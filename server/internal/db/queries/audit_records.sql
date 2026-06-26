-- Audit records queries.
-- Unified ingest + listing for admin / runtime / approval / identity / data
-- audit events. Single-table design with jsonb payload (see migration
-- 000005_audit_records.sql for rationale).

-- name: InsertAuditRecord :exec
insert into audit_records (
  occurred_at, source, event_type, actor_type, actor_id,
  target_type, target_id, workspace_id, project_id, payload
)
values (
  @occurred_at, @source, @event_type, @actor_type, @actor_id::uuid,
  @target_type, @target_id::uuid, @workspace_id::uuid, @project_id::uuid, @payload::jsonb
);

-- name: ListAuditRecords :many
-- Generic filter list. Pass empty/null for any filter you want to skip.
-- workspace_id / project_id / actor_id / target_id are pgtype.UUID — pass
-- {Valid:false} to skip. source / event_type / target_type are text — pass
-- empty string to skip. since / until are pgtype.Timestamptz — pass
-- {Valid:false} to skip.
select id, occurred_at, source, event_type, actor_type, actor_id,
       target_type, target_id, workspace_id, project_id, payload
from audit_records
where (@workspace_id::uuid is null or workspace_id = @workspace_id::uuid)
  and (@project_id::uuid is null or project_id = @project_id::uuid)
  and (@source::text = '' or source = @source::text)
  and (@event_type::text = '' or event_type = @event_type::text)
  and (@actor_id::uuid is null or actor_id = @actor_id::uuid)
  and (@target_type::text = '' or target_type = @target_type::text)
  and (@target_id::uuid is null or target_id = @target_id::uuid)
  and (@since::timestamptz is null or occurred_at >= @since::timestamptz)
  and (@until::timestamptz is null or occurred_at <= @until::timestamptz)
order by occurred_at desc, id desc
limit @item_limit::int;

-- name: GetAuditRecord :one
select id, occurred_at, source, event_type, actor_type, actor_id,
       target_type, target_id, workspace_id, project_id, payload
from audit_records
where id = @id::bigint;
