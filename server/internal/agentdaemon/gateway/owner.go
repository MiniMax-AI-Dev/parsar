package gateway

import (
	"context"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const defaultOwnerLeaseTTL = 90 * time.Second

// DeviceOwnerStore is the DB-backed owner lease surface used by the
// gateway.
type DeviceOwnerStore interface {
	ClaimAgentDaemonDeviceOwner(ctx context.Context, input store.ClaimAgentDaemonDeviceOwnerInput) (store.AgentDaemonDeviceOwnerRead, error)
	RenewAgentDaemonDeviceOwner(ctx context.Context, input store.RenewAgentDaemonDeviceOwnerInput) (store.AgentDaemonDeviceOwnerRead, bool, error)
	ReleaseAgentDaemonDeviceOwner(ctx context.Context, input store.ReleaseAgentDaemonDeviceOwnerInput) (bool, error)
	GetAgentDaemonDeviceOwner(ctx context.Context, deviceID string) (store.AgentDaemonDeviceOwnerRead, bool, error)
}

type ownerLease struct {
	store      DeviceOwnerStore
	deviceID   string
	ownerPodID string
	ownerURL   string
	generation int64
	ttl        time.Duration
}

func normalizeOwnerTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return defaultOwnerLeaseTTL
	}
	return ttl
}
