-- name: PutSubagentIdentity :one
INSERT INTO subagent_identities (
    id, session_id, device_id, engine, native_id, parent_native_id, native_created_at,
    first_turn_id, first_event_ordinal
)
SELECT sqlc.arg(id), s.id, b.device_id, s.engine, sqlc.arg(native_id),
    sqlc.arg(parent_native_id), sqlc.arg(native_created_at), sqlc.arg(first_turn_id), sqlc.arg(first_event_ordinal)
FROM sessions s JOIN session_devices b ON b.session_id = s.id
WHERE s.id = sqlc.arg(session_id)
  AND (b.native_session_id = '' OR b.native_session_id = sqlc.arg(parent_native_id))
  AND NOT EXISTS (
      SELECT 1 FROM subagent_identities old
      WHERE old.session_id = s.id AND old.parent_native_id <> sqlc.arg(parent_native_id)
  )
ON CONFLICT (device_id, engine, native_id) DO UPDATE SET id = subagent_identities.id
WHERE subagent_identities.session_id = EXCLUDED.session_id
  AND subagent_identities.parent_native_id = EXCLUDED.parent_native_id
  AND subagent_identities.native_created_at = EXCLUDED.native_created_at
RETURNING id;

-- name: GetSubagentIdentity :one
SELECT i.*, e.created_at AS first_observed_at FROM subagent_identities i
JOIN sessions s ON s.id = i.session_id
JOIN turn_events e ON e.turn_id = i.first_turn_id AND e.ordinal = i.first_event_ordinal
WHERE s.tenant_id = sqlc.arg(tenant_id) AND s.id = sqlc.arg(session_id)
  AND s.deleted_at IS NULL AND i.native_id = sqlc.arg(native_id);
