-- name: GetAgentRunRetrySource :one
select r.workspace_id::text as workspace_id, r.conversation_id::text as conversation_id,
       r.agent_id::text as agent_id, r.trigger_message_id, r.visibility,
       a.connector_type, coalesce(r.metadata->>'retry_run_id', '')::text as retry_run_id
from agent_runs r
join agents a on a.id = r.agent_id and a.workspace_id = r.workspace_id
join conversations c on c.id = r.conversation_id and c.workspace_id = r.workspace_id
join workspaces w on w.id = r.workspace_id
join messages m on m.id = r.trigger_message_id
  and m.workspace_id = r.workspace_id and m.conversation_id = r.conversation_id
where r.id = @id::uuid
  and r.status in ('failed', 'cancelled', 'interrupted')
  and a.status = 'active' and a.deleted_at is null
  and c.status = 'active' and c.deleted_at is null
  and w.deleted_at is null and m.deleted_at is null
for update of r;

-- name: CreateAgentRunRetry :exec
insert into agent_runs (
  id, workspace_id, conversation_id, agent_id, connector_type,
  trigger_message_id, trigger_source, trigger_channel, trigger_ref_type, trigger_ref_id,
  requested_by_type, requested_by_id, status, visibility, metadata, created_at, updated_at
) values (
  @id::uuid, @workspace_id::uuid, @conversation_id::uuid, @agent_id::uuid, @connector_type,
  @trigger_message_id::uuid, 'manual', 'web', 'agent_run', @retry_of_run_id::uuid,
  'user', @requested_by_id::uuid, 'queued', @visibility, @metadata::jsonb, @now, @now
);
