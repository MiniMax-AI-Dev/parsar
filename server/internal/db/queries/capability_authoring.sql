-- name: LockAuthoringSkill :one
select id::text
from capability
where id = @id::uuid and workspace_id = @workspace_id::uuid
  and type = 'skill' and visibility = 'workspace' and deleted_at is null
for update;
