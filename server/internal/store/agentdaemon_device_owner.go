package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

const AgentDaemonOwnerStatusConnected = "connected"
const AgentDaemonOwnerStatusDraining = "draining"
const AgentDaemonOwnerStatusExpired = "expired"

// AgentDaemonDeviceOwnerRead is the store-level view of the current
// WebSocket owner for one agent_daemon device. Generation is a fencing
// token: renewal/release paths must carry it so stale pods can't act.
type AgentDaemonDeviceOwnerRead struct {
	DeviceID       string
	WorkspaceID    string
	OwnerPodID     string
	OwnerURL       string
	Generation     int64
	Status         string
	ConnectedAt    time.Time
	LastSeenAt     time.Time
	LeaseExpiresAt time.Time
	UpdatedAt      time.Time
}

type ClaimAgentDaemonDeviceOwnerInput struct {
	DeviceID       string
	WorkspaceID    string
	OwnerPodID     string
	OwnerURL       string
	LeaseExpiresAt time.Time
	Now            time.Time
}

type RenewAgentDaemonDeviceOwnerInput struct {
	DeviceID       string
	OwnerPodID     string
	Generation     int64
	LeaseExpiresAt time.Time
	Now            time.Time
}

type ReleaseAgentDaemonDeviceOwnerInput struct {
	DeviceID   string
	OwnerPodID string
	Generation int64
}

// ClaimAgentDaemonDeviceOwner records this pod as the live WebSocket
// owner for a device. Latest connection wins and increments generation;
// stale sessions are fenced by subsequent Renew/Release checks.
func (s *Store) ClaimAgentDaemonDeviceOwner(ctx context.Context, input ClaimAgentDaemonDeviceOwnerInput) (AgentDaemonDeviceOwnerRead, error) {
	deviceID, err := uuid(input.DeviceID)
	if err != nil {
		return AgentDaemonDeviceOwnerRead{}, fmt.Errorf("agentdaemon owner: device_id: %w", err)
	}
	workspaceID, err := uuid(input.WorkspaceID)
	if err != nil {
		return AgentDaemonDeviceOwnerRead{}, fmt.Errorf("agentdaemon owner: workspace_id: %w", err)
	}
	ownerPodID := strings.TrimSpace(input.OwnerPodID)
	if ownerPodID == "" {
		return AgentDaemonDeviceOwnerRead{}, errors.New("agentdaemon owner: owner_pod_id is required")
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseExpiresAt := input.LeaseExpiresAt.UTC()
	if leaseExpiresAt.IsZero() || !leaseExpiresAt.After(now) {
		return AgentDaemonDeviceOwnerRead{}, errors.New("agentdaemon owner: lease_expires_at must be in the future")
	}
	row, err := sqlc.New(s.db).ClaimAgentDaemonDeviceOwner(ctx, sqlc.ClaimAgentDaemonDeviceOwnerParams{
		DeviceID:       deviceID,
		WorkspaceID:    workspaceID,
		OwnerPodID:     ownerPodID,
		OwnerUrl:       strings.TrimSpace(input.OwnerURL),
		Now:            timestamptz(now),
		LeaseExpiresAt: timestamptz(leaseExpiresAt),
	})
	if err != nil {
		return AgentDaemonDeviceOwnerRead{}, fmt.Errorf("agentdaemon owner: claim: %w", err)
	}
	return agentDaemonOwnerFromClaimRow(row), nil
}

func (s *Store) GetAgentDaemonDeviceOwner(ctx context.Context, deviceIDRaw string) (AgentDaemonDeviceOwnerRead, bool, error) {
	deviceID, err := uuid(deviceIDRaw)
	if err != nil {
		return AgentDaemonDeviceOwnerRead{}, false, fmt.Errorf("agentdaemon owner: device_id: %w", err)
	}
	row, err := sqlc.New(s.db).GetAgentDaemonDeviceOwner(ctx, deviceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentDaemonDeviceOwnerRead{}, false, nil
		}
		return AgentDaemonDeviceOwnerRead{}, false, fmt.Errorf("agentdaemon owner: get: %w", err)
	}
	return agentDaemonOwnerFromGetRow(row), true, nil
}

// RenewAgentDaemonDeviceOwner extends the lease only if the caller
// still matches owner_pod_id and generation. A false return means
// another pod has claimed a newer generation or the row vanished.
func (s *Store) RenewAgentDaemonDeviceOwner(ctx context.Context, input RenewAgentDaemonDeviceOwnerInput) (AgentDaemonDeviceOwnerRead, bool, error) {
	deviceID, err := uuid(input.DeviceID)
	if err != nil {
		return AgentDaemonDeviceOwnerRead{}, false, fmt.Errorf("agentdaemon owner: device_id: %w", err)
	}
	ownerPodID := strings.TrimSpace(input.OwnerPodID)
	if ownerPodID == "" || input.Generation <= 0 {
		return AgentDaemonDeviceOwnerRead{}, false, errors.New("agentdaemon owner: owner_pod_id and generation are required")
	}
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	leaseExpiresAt := input.LeaseExpiresAt.UTC()
	if leaseExpiresAt.IsZero() || !leaseExpiresAt.After(now) {
		return AgentDaemonDeviceOwnerRead{}, false, errors.New("agentdaemon owner: lease_expires_at must be in the future")
	}
	row, err := sqlc.New(s.db).RenewAgentDaemonDeviceOwner(ctx, sqlc.RenewAgentDaemonDeviceOwnerParams{
		Now:            timestamptz(now),
		LeaseExpiresAt: timestamptz(leaseExpiresAt),
		DeviceID:       deviceID,
		OwnerPodID:     ownerPodID,
		Generation:     input.Generation,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AgentDaemonDeviceOwnerRead{}, false, nil
		}
		return AgentDaemonDeviceOwnerRead{}, false, fmt.Errorf("agentdaemon owner: renew: %w", err)
	}
	return agentDaemonOwnerFromRenewRow(row), true, nil
}

// ReleaseAgentDaemonDeviceOwner drops the owner row only when the caller
// still owns the matching generation. Stale releases are harmless no-ops.
func (s *Store) ReleaseAgentDaemonDeviceOwner(ctx context.Context, input ReleaseAgentDaemonDeviceOwnerInput) (bool, error) {
	deviceID, err := uuid(input.DeviceID)
	if err != nil {
		return false, fmt.Errorf("agentdaemon owner: device_id: %w", err)
	}
	ownerPodID := strings.TrimSpace(input.OwnerPodID)
	if ownerPodID == "" || input.Generation <= 0 {
		return false, errors.New("agentdaemon owner: owner_pod_id and generation are required")
	}
	n, err := sqlc.New(s.db).ReleaseAgentDaemonDeviceOwner(ctx, sqlc.ReleaseAgentDaemonDeviceOwnerParams{
		DeviceID:   deviceID,
		OwnerPodID: ownerPodID,
		Generation: input.Generation,
	})
	if err != nil {
		return false, fmt.Errorf("agentdaemon owner: release: %w", err)
	}
	return n > 0, nil
}

func (s *Store) ExpireAgentDaemonDeviceOwners(ctx context.Context, now time.Time) (int64, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	n, err := sqlc.New(s.db).ExpireAgentDaemonDeviceOwners(ctx, timestamptz(now.UTC()))
	if err != nil {
		return 0, fmt.Errorf("agentdaemon owner: expire: %w", err)
	}
	return n, nil
}

func agentDaemonOwnerFromClaimRow(r sqlc.ClaimAgentDaemonDeviceOwnerRow) AgentDaemonDeviceOwnerRead {
	return agentDaemonOwnerFromRow(agentDaemonOwnerRow(r))
}

func agentDaemonOwnerFromGetRow(r sqlc.GetAgentDaemonDeviceOwnerRow) AgentDaemonDeviceOwnerRead {
	return agentDaemonOwnerFromRow(agentDaemonOwnerRow(r))
}

func agentDaemonOwnerFromRenewRow(r sqlc.RenewAgentDaemonDeviceOwnerRow) AgentDaemonDeviceOwnerRead {
	return agentDaemonOwnerFromRow(agentDaemonOwnerRow(r))
}

type agentDaemonOwnerRow sqlc.ClaimAgentDaemonDeviceOwnerRow

func agentDaemonOwnerFromRow(r agentDaemonOwnerRow) AgentDaemonDeviceOwnerRead {
	return AgentDaemonDeviceOwnerRead{
		DeviceID:       r.DeviceID,
		WorkspaceID:    r.WorkspaceID,
		OwnerPodID:     r.OwnerPodID,
		OwnerURL:       r.OwnerUrl,
		Generation:     r.Generation,
		Status:         r.Status,
		ConnectedAt:    pgTime(r.ConnectedAt),
		LastSeenAt:     pgTime(r.LastSeenAt),
		LeaseExpiresAt: pgTime(r.LeaseExpiresAt),
		UpdatedAt:      pgTime(r.UpdatedAt),
	}
}
