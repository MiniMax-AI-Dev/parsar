-- name: CreateDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetDevice :one
SELECT id, name FROM devices WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;

-- name: GetDeviceCredential :one
SELECT id, name, credential_hash FROM devices WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeDevice :execrows
UPDATE devices SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE tenant_id = $1 AND id = $2;

-- name: TouchDevice :execrows
UPDATE devices SET last_seen_at = clock_timestamp()
WHERE id = $1 AND revoked_at IS NULL;

-- name: BindSessionDevice :one
INSERT INTO session_devices (session_id, device_id)
SELECT s.id, d.id FROM sessions s JOIN devices d ON d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.id = $3 AND d.revoked_at IS NULL
ON CONFLICT (session_id) DO UPDATE SET device_id = session_devices.device_id
WHERE session_devices.device_id = EXCLUDED.device_id
RETURNING device_id;

-- name: GetSessionDevice :one
SELECT d.id, d.name FROM session_devices b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.device_id AND d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL;
