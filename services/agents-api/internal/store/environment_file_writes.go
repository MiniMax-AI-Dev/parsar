package store

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

// FileWriteIdentity binds a private mutation to one dedicated local Runtime. RequestSHA256 covers the canonical destination, byte count and data digest.
// The caller must qualify that binding and validate native receipts independently;
// persistence alone is neither placement authority nor permission to send bytes.
type FileWriteIdentity struct {
	ID, DeviceID, RequestSHA256 string
}

type EnvironmentFileWrite struct {
	Identity                 FileWriteIdentity
	EnvironmentID, SessionID string
	State                    string
	CreatedAt                time.Time
	SettledAt                *time.Time
	Replayed                 bool
}

func (k FileWriteIdentity) valid() bool {
	for _, value := range []string{k.ID, k.DeviceID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return false
		}
	}
	digest, err := hex.DecodeString(k.RequestSHA256)
	return err == nil && len(digest) == 32 && hex.EncodeToString(digest) == k.RequestSHA256
}

// ReserveEnvironmentFileWrite persists intent before external dispatch. A retry
// observes the earlier operation and never authorizes resending an unknown write.
func (s *Store) ReserveEnvironmentFileWrite(ctx context.Context, tenant, environment string, key FileWriteIdentity) (EnvironmentFileWrite, error) {
	if s.executionLease == nil || !key.valid() {
		return EnvironmentFileWrite{}, ErrInvalidInput
	}
	owned, err := s.GetEnvironment(ctx, tenant, environment)
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	lookup, err := fileWriteLookup(tenant, environment, key.ID)
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	var result EnvironmentFileWrite
	err = s.withPublicSession(ctx, tenant, owned.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		previous, err := q.GetEnvironmentFileWrite(ctx, lookup)
		if err == nil {
			result = fileWriteFromRow(previous.EnvironmentFileWrite, session)
			result.Replayed = true
			if result.Identity != key {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		current, err := q.GetSessionEnvironment(ctx, sqlc.GetSessionEnvironmentParams{TenantID: lookup.TenantID, ID: session})
		if err != nil {
			return err
		}
		if current.Environment.ID != lookup.EnvironmentID || current.Environment.Status == "failed" || current.Environment.Status == "expired" {
			return ErrInvalidInput
		}
		device, err := q.GetSessionDevice(ctx, sqlc.GetSessionDeviceParams{TenantID: lookup.TenantID, ID: session})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if uuid.UUID(device.ID.Bytes).String() != key.DeviceID || device.EnvironmentID != lookup.EnvironmentID {
			return ErrDeviceBindingConflict
		}
		if err := environmentInputMayStart(ctx, q, session); err != nil {
			return err
		}
		pending, err := q.EnvironmentFileWriteHasPendingInput(ctx, session)
		if err != nil {
			return err
		}
		if pending {
			return ErrTurnConflict
		}
		deviceID, _ := parseID(key.DeviceID)
		row, err := q.CreateEnvironmentFileWrite(ctx, sqlc.CreateEnvironmentFileWriteParams{
			ID: lookup.ID, EnvironmentID: lookup.EnvironmentID, DeviceID: deviceID, RequestSha256: key.RequestSHA256,
		})
		if err == nil {
			result = fileWriteFromRow(row, session)
		}
		return err
	})
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	return result, nil
}

// GetEnvironmentFileWrite also retains deleted-Session intents for internal
// cleanup. It is not a public resource query and never authorizes dispatch.
func (s *Store) GetEnvironmentFileWrite(ctx context.Context, tenant, environment, id string) (EnvironmentFileWrite, error) {
	lookup, err := fileWriteLookup(tenant, environment, id)
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	row, err := s.queries.GetEnvironmentFileWrite(ctx, lookup)
	if errors.Is(err, pgx.ErrNoRows) {
		return EnvironmentFileWrite{}, ErrNotFound
	}
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	result := fileWriteFromRow(row.EnvironmentFileWrite, row.SessionID)
	result.Replayed = true
	return result, nil
}

// SettleEnvironmentFileWrite requires an independently validated exact receipt.
// A missing receipt, cancellation or owner retirement is not a rejected upload.
func (s *Store) SettleEnvironmentFileWrite(ctx context.Context, tenant, environment string, key FileWriteIdentity, state string) (EnvironmentFileWrite, error) {
	if s.executionLease == nil || !key.valid() || (state != "committed" && state != "rejected") {
		return EnvironmentFileWrite{}, ErrInvalidInput
	}
	previous, err := s.GetEnvironmentFileWrite(ctx, tenant, environment, key.ID)
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	lookup, _ := fileWriteLookup(tenant, environment, key.ID)
	var result EnvironmentFileWrite
	err = s.withSession(ctx, tenant, previous.SessionID, func(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
		current, err := q.GetEnvironmentFileWrite(ctx, lookup)
		if err != nil {
			return err
		}
		result = fileWriteFromRow(current.EnvironmentFileWrite, session)
		if result.Identity != key || (result.State != "pending" && result.State != state) {
			return ErrIdempotencyConflict
		}
		if result.State == state {
			result.Replayed = true
			return nil
		}
		row, err := q.SettleEnvironmentFileWrite(ctx, sqlc.SettleEnvironmentFileWriteParams{EnvironmentID: lookup.EnvironmentID, ID: lookup.ID, State: state})
		if err == nil {
			result = fileWriteFromRow(row, session)
		}
		return err
	})
	if err != nil {
		return EnvironmentFileWrite{}, err
	}
	return result, nil
}

func fileWriteLookup(tenant, environment, id string) (sqlc.GetEnvironmentFileWriteParams, error) {
	var result sqlc.GetEnvironmentFileWriteParams
	var err error
	result.TenantID, err = parseID(tenant)
	if err == nil {
		result.EnvironmentID, err = parseID(environment)
	}
	if err == nil {
		result.ID, err = parseID(id)
	}
	return result, err
}

func fileWriteFromRow(row sqlc.EnvironmentFileWrite, session pgtype.UUID) EnvironmentFileWrite {
	result := EnvironmentFileWrite{Identity: FileWriteIdentity{ID: uuid.UUID(row.ID.Bytes).String(), DeviceID: uuid.UUID(row.DeviceID.Bytes).String(), RequestSHA256: row.RequestSha256},
		EnvironmentID: uuid.UUID(row.EnvironmentID.Bytes).String(), SessionID: uuid.UUID(session.Bytes).String(), State: row.State, CreatedAt: row.CreatedAt.Time}
	if row.SettledAt.Valid {
		result.SettledAt = &row.SettledAt.Time
	}
	return result
}

func checkEnvironmentFileWriteGate(ctx context.Context, q *sqlc.Queries, session pgtype.UUID) error {
	blocked, err := q.EnvironmentFileWriteBlocksSession(ctx, session)
	if err == nil && blocked {
		return ErrTurnConflict
	}
	return err
}
