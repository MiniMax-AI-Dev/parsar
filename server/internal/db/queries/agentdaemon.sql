-- agentdaemon.sql — multi-pod WebSocket owner leases for agent_daemon.

-- name: ClaimAgentDaemonDeviceOwner :one
-- Latest successful WebSocket dial-in wins and receives a fresh generation.
-- Older sessions can still exist briefly, but renew/release operations are
-- fenced by generation so they cannot affect the new owner row.
insert into agent_daemon_device_owners (
  device_id, workspace_id, owner_pod_id, owner_url, generation,
  status, connected_at, last_seen_at, lease_expires_at, updated_at
)
values (
  @device_id::uuid, @workspace_id::uuid, @owner_pod_id::text, @owner_url::text, 1,
  'connected', @now, @now, @lease_expires_at, @now
)
on conflict (device_id) do update
set workspace_id = excluded.workspace_id,
    owner_pod_id = excluded.owner_pod_id,
    owner_url = excluded.owner_url,
    generation = agent_daemon_device_owners.generation + 1,
    status = 'connected',
    connected_at = excluded.connected_at,
    last_seen_at = excluded.last_seen_at,
    lease_expires_at = excluded.lease_expires_at,
    updated_at = excluded.updated_at
returning device_id::text, workspace_id::text, owner_pod_id, owner_url,
  generation, status, connected_at, last_seen_at, lease_expires_at, updated_at;

-- name: GetAgentDaemonDeviceOwner :one
select device_id::text, workspace_id::text, owner_pod_id, owner_url,
  generation, status, connected_at, last_seen_at, lease_expires_at, updated_at
from agent_daemon_device_owners
where device_id = @device_id::uuid;

-- name: RenewAgentDaemonDeviceOwner :one
update agent_daemon_device_owners
set last_seen_at = @now,
    lease_expires_at = @lease_expires_at,
    status = 'connected',
    updated_at = @now
where device_id = @device_id::uuid
  and owner_pod_id = @owner_pod_id::text
  and generation = @generation::bigint
returning device_id::text, workspace_id::text, owner_pod_id, owner_url,
  generation, status, connected_at, last_seen_at, lease_expires_at, updated_at;

-- name: ReleaseAgentDaemonDeviceOwner :execrows
delete from agent_daemon_device_owners
where device_id = @device_id::uuid
  and owner_pod_id = @owner_pod_id::text
  and generation = @generation::bigint;

-- name: ExpireAgentDaemonDeviceOwners :execrows
update agent_daemon_device_owners
set status = 'expired',
    updated_at = @now
where lease_expires_at < @now
  and status <> 'expired';
