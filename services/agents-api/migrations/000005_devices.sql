-- +goose Up
CREATE TABLE devices (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    name text NOT NULL,
    credential_hash text NOT NULL CHECK (credential_hash ~ '^[0-9a-f]{64}$'),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    last_seen_at timestamptz,
    revoked_at timestamptz
);
CREATE INDEX devices_tenant_idx ON devices (tenant_id);

CREATE TABLE session_devices (
    session_id uuid PRIMARY KEY REFERENCES sessions(id),
    device_id uuid NOT NULL REFERENCES devices(id)
);

-- +goose Down
DROP TABLE session_devices;
DROP TABLE devices;
