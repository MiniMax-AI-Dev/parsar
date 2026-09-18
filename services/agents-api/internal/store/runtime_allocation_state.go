package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// ObserveRuntimeRunning requires verified original compute identity. It does not
// mark the Environment connected or qualify native preparation.
func (s *Store) ObserveRuntimeRunning(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, true, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		return q.ObserveRuntimeRunning(ctx, row.ID)
	})
}

// KeepRuntimeAllocation follows an authenticated connection and successful
// provider observation. A keepalive cannot revive cleanup or an expired lease.
func (s *Store) KeepRuntimeAllocation(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, true, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		return q.KeepRuntimeAllocation(ctx, row.ID)
	})
}

// SettleRuntimeCreation records evidence that the original Create can no longer
// mutate resources. A timeout, missing container or lost lease is not evidence.
func (s *Store) SettleRuntimeCreation(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, false, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		if row.State == "released" {
			return row, nil
		}
		return q.SettleRuntimeCreation(ctx, row.ID)
	})
}

// RequestRuntimeCleanup revokes future authority before external reclamation.
// Cancellation requests do not prove existing native work has stopped.
func (s *Store) RequestRuntimeCleanup(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, false, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		if row.State == "released" {
			return row, nil
		}
		tenant, _ := parseID(owner.TenantID)
		if _, err := q.RevokeDevice(ctx, sqlc.RevokeDeviceParams{TenantID: tenant, ID: row.DeviceID}); err != nil {
			return sqlc.RuntimeAllocation{}, err
		}
		current, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: tenant, EnvironmentID: row.EnvironmentID})
		if err != nil {
			return sqlc.RuntimeAllocation{}, err
		}
		cancel := func() error { return cancelSessionWork(ctx, q, current.SessionID) }
		if current.DeletedAt.Valid {
			err = cancel()
		} else {
			err = withEnvironmentInputActivity(ctx, q, current.SessionID, cancel)
		}
		if err != nil {
			return sqlc.RuntimeAllocation{}, err
		}

		return q.RequestRuntimeCleanup(ctx, row.ID)
	})
}

// ReleaseRuntimeAllocation follows successful owned container/volume cleanup.
// Unknown Create outcomes keep their cleanup record even when compute is absent.
func (s *Store) ReleaseRuntimeAllocation(ctx context.Context, owner RuntimeAllocation) (RuntimeAllocation, error) {
	return s.mutateRuntimeAllocation(ctx, owner, false, func(ctx context.Context, q *sqlc.Queries, row sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error) {
		if row.State == "released" {
			return row, nil
		}
		return q.ReleaseRuntimeAllocation(ctx, row.ID)
	})
}

func (s *Store) mutateRuntimeAllocation(ctx context.Context, owner RuntimeAllocation, live bool, apply func(context.Context, *sqlc.Queries, sqlc.RuntimeAllocation) (sqlc.RuntimeAllocation, error)) (RuntimeAllocation, error) {
	if s.executionLease == nil {
		return RuntimeAllocation{}, ErrInvalidInput
	}
	previous, err := s.GetRuntimeAllocation(ctx, owner.TenantID, owner.EnvironmentID)
	if err != nil {
		return RuntimeAllocation{}, err
	}
	if previous.ID != owner.ID || previous.DeviceID != owner.DeviceID || previous.ProviderKey != owner.ProviderKey {
		return RuntimeAllocation{}, ErrIdempotencyConflict
	}
	lookup, _ := deviceLookup(owner.TenantID, owner.EnvironmentID)
	var result RuntimeAllocation
	err = s.withSession(ctx, owner.TenantID, previous.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetRuntimeAllocation(ctx, sqlc.GetRuntimeAllocationParams{TenantID: lookup.TenantID, EnvironmentID: lookup.ID})
		if err != nil {
			return err
		}
		if live {
			if current.DeletedAt.Valid {
				return ErrNotFound
			}
			device, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: lookup.TenantID, ID: session})
			if err != nil {
				return err
			}
			if device.ID != current.RuntimeAllocation.DeviceID || device.EnvironmentID != lookup.ID {
				return ErrDeviceBindingConflict
			}
		}
		row, err := apply(ctx, q, current.RuntimeAllocation)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTurnConflict
		}
		if err == nil {
			result = runtimeAllocationFromRow(row, session, lookup.TenantID, current.DeletedAt, current.Expired)
		}
		return err
	})
	if err != nil {
		return RuntimeAllocation{}, err
	}
	return result, nil
}
