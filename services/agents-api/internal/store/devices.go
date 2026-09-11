package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/gateway"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

var ErrDeviceBindingConflict = errors.New("session is already bound to a different device")

// ExecutionDevice contains safe identity only, never a device credential.
type ExecutionDevice struct {
	ID   string
	Name string
}

// CreateDevice is operator provisioning, not a tenant-facing registration API.
func (s *Store) CreateDevice(ctx context.Context, tenantID, name, credentialHash string) (ExecutionDevice, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return ExecutionDevice{}, err
	}
	name = strings.TrimSpace(name)
	digest, err := hex.DecodeString(credentialHash)
	if err != nil || len(digest) != 32 || name == "" || len(name) > 256 {
		return ExecutionDevice{}, fmt.Errorf("%w: device name and SHA-256 credential digest required", ErrInvalidInput)
	}
	id, err := s.queries.CreateDevice(ctx, sqlc.CreateDeviceParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
		Name: name, CredentialHash: hex.EncodeToString(digest),
	})
	if err != nil {
		return ExecutionDevice{}, fmt.Errorf("create execution device: %w", err)
	}
	return ExecutionDevice{ID: uuid.UUID(id.Bytes).String(), Name: name}, nil
}

// GetDeviceCredential is used only by the shared gateway's credential verifier.
// The standalone service does not assign a product WorkspaceID.
func (s *Store) GetDeviceCredential(ctx context.Context, deviceID string) (device.Credential, bool, error) {
	id, err := parseID(deviceID)
	if err != nil {
		return device.Credential{}, false, nil
	}
	row, err := s.queries.GetDeviceCredential(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return device.Credential{}, false, nil
	}
	if err != nil {
		return device.Credential{}, false, err
	}
	return device.Credential{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name,
		Type: gateway.RuntimeTypeAgentDaemon, CredentialHash: row.CredentialHash}, true, nil
}

func (s *Store) RevokeDevice(ctx context.Context, tenantID, deviceID string) error {
	params, err := deviceLookup(tenantID, deviceID)
	if err != nil {
		return err
	}
	n, err := s.queries.RevokeDevice(ctx, sqlc.RevokeDeviceParams(params))
	if err == nil && n == 0 {
		return ErrNotFound
	}
	return err
}

// BindSessionDevice keeps retries stable and refuses silent filesystem moves.
// The dispatcher must obtain its device through GetSessionDevice before delivery.
func (s *Store) BindSessionDevice(ctx context.Context, tenantID, sessionID, deviceID string) error {
	params, err := deviceLookup(tenantID, deviceID)
	if err != nil {
		return err
	}
	return s.withSession(ctx, tenantID, sessionID, func(q *sqlc.Queries, session pgtype.UUID) error {
		if _, err := q.GetDevice(ctx, params); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		_, err := q.BindSessionDevice(ctx, sqlc.BindSessionDeviceParams{TenantID: params.TenantID, ID: session, ID_2: params.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDeviceBindingConflict
		}
		return err
	})
}

func (s *Store) GetSessionDevice(ctx context.Context, tenantID, sessionID string) (ExecutionDevice, error) {
	params, err := deviceLookup(tenantID, sessionID)
	if err != nil {
		return ExecutionDevice{}, err
	}
	row, err := s.queries.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams(params))
	if errors.Is(err, pgx.ErrNoRows) {
		return ExecutionDevice{}, ErrNotFound
	}
	if err != nil {
		return ExecutionDevice{}, err
	}
	return ExecutionDevice{ID: uuid.UUID(row.ID.Bytes).String(), Name: row.Name}, nil
}

func deviceLookup(tenantID, id string) (sqlc.GetDeviceParams, error) {
	var p sqlc.GetDeviceParams
	var err error
	if p.TenantID, err = parseID(tenantID); err != nil {
		return p, err
	}
	p.ID, err = parseID(id)
	return p, err
}

func (s *Store) TouchRuntimeHeartbeat(ctx context.Context, deviceID string) (device.HeartbeatStatus, error) {
	id, err := parseID(deviceID)
	if err != nil {
		return device.HeartbeatStatus{}, err
	}
	n, err := s.queries.TouchDevice(ctx, id)
	return device.HeartbeatStatus{Liveness: "online", Deleted: n == 0}, err
}

func (s *Store) TouchAgentDaemonHeartbeat(ctx context.Context, heartbeat device.Heartbeat) (device.HeartbeatStatus, error) {
	return s.TouchRuntimeHeartbeat(ctx, heartbeat.RuntimeID)
}

// Live connectivity belongs to the gateway Registry. Only last-seen time is
// persisted, so a stale socket closing cannot overwrite a newer connection.
func (s *Store) MarkRuntimeOffline(context.Context, string) error { return nil }
