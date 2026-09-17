-- name: CreateDevice :one
INSERT INTO devices (id, tenant_id, name, credential_hash)
VALUES ($1, $2, $3, $4) RETURNING id;

-- name: GetDevice :one
SELECT id, name FROM devices WHERE tenant_id = $1 AND id = $2 AND revoked_at IS NULL;

-- name: GetDeviceCredential :one
SELECT d.id, d.name, d.credential_hash FROM devices d
WHERE d.id = $1 AND d.revoked_at IS NULL AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e JOIN sessions s ON s.id = e.session_id
    WHERE e.id = d.environment_id AND s.tenant_id = d.tenant_id AND s.deleted_at IS NULL
));

-- name: RevokeDevice :execrows
UPDATE devices SET revoked_at = COALESCE(revoked_at, clock_timestamp())
WHERE tenant_id = $1 AND id = $2;

-- name: TouchDevice :execrows
UPDATE devices SET last_seen_at = clock_timestamp()
WHERE devices.id = $1 AND devices.revoked_at IS NULL AND (devices.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e JOIN sessions s ON s.id = e.session_id
    WHERE e.id = devices.environment_id AND s.tenant_id = devices.tenant_id AND s.deleted_at IS NULL
));

-- name: BindSessionDevice :one
INSERT INTO session_devices (session_id, device_id)
SELECT s.id, d.id FROM sessions s JOIN devices d ON d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.id = $3 AND d.revoked_at IS NULL
AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e WHERE e.id = d.environment_id AND e.session_id = s.id
))
ON CONFLICT (session_id) DO UPDATE SET device_id = session_devices.device_id
WHERE session_devices.device_id = EXCLUDED.device_id
RETURNING device_id;

-- name: GetSessionDevice :one
SELECT d.id, d.name, b.native_session_id, d.environment_id FROM session_devices b
JOIN sessions s ON s.id = b.session_id
JOIN devices d ON d.id = b.device_id AND d.tenant_id = s.tenant_id
WHERE s.tenant_id = $1 AND s.id = $2 AND d.revoked_at IS NULL
AND (d.environment_id IS NULL OR EXISTS (
    SELECT 1 FROM environments e WHERE e.id = d.environment_id AND e.session_id = s.id
));

-- name: RememberNativeSession :execrows
UPDATE session_devices SET native_session_id = $2 WHERE session_id = $1;
