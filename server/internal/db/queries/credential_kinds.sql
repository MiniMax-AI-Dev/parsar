-- Queries for credential_kinds (see migrations/000003_capability_canonical.sql
-- and migrations/000004_drop_credential_kind_ui_columns.sql).
--
-- Built-in kinds (built_in=TRUE) are seeded by the migration and immutable —
-- the import UI should hide delete/edit affordances for them. User-created
-- kinds (built_in=FALSE) carry `created_by` and can be soft-deleted.
--
-- created_by is wrapped in coalesce so seed rows (where created_by IS NULL)
-- scan into a non-nullable Go string; callers treat "" as "system-owned".

-- name: ListCredentialKinds :many
select id::text as id,
       code,
       display_name,
       description,
       value_schema,
       built_in,
       source,
       coalesce(created_by::text, '') as created_by,
       created_at,
       updated_at
from credential_kinds
where deleted_at is null
order by built_in desc, code asc;

-- name: GetCredentialKindByCode :one
select id::text as id,
       code,
       display_name,
       description,
       value_schema,
       built_in,
       source,
       coalesce(created_by::text, '') as created_by,
       created_at,
       updated_at
from credential_kinds
where code = @code
  and deleted_at is null;

-- name: CreateCredentialKind :one
insert into credential_kinds(
  id, code, display_name, description,
  value_schema, built_in, source, created_by, created_at, updated_at
)
values (
  @id::uuid, @code, @display_name, @description,
  @value_schema::jsonb, false, @source, @created_by::uuid, @now, @now
)
returning id::text as id,
          code,
          display_name,
          description,
          value_schema,
          built_in,
          source,
          coalesce(created_by::text, '') as created_by,
          created_at,
          updated_at;
