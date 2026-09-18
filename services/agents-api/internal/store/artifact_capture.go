package store

import (
	"archive/tar"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxArtifactBytes int64 = 200 << 20
const MaxArtifactBatchBytes int64 = 500 << 20

// StageTurnArtifacts stores a complete export privately without locking admission during transfer.
func (s *Store) StageTurnArtifacts(ctx context.Context, tenantID, sessionID, turnID, environmentID string, input io.Reader) error {
	lookup, err := turnLookup(tenantID, sessionID, turnID)
	if err != nil {
		return err
	}
	environment, err := parseID(environmentID)
	if err != nil || input == nil {
		return ErrInvalidInput
	}
	// Authorize before reading caller-controlled bytes or allocating storage.
	owned, err := s.GetSessionEnvironment(ctx, tenantID, sessionID)
	if err != nil {
		return err
	}
	if owned.ID != environmentID {
		return ErrNotFound
	}
	var configuration struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(owned.Configuration, &configuration); err != nil {
		return err
	}
	if configuration.Type != "openai_hosted" {
		return ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	rows, err := captureArtifactArchive(ctx, tx, input)
	if err != nil {
		return err
	}
	q := s.queries.WithTx(tx)
	locked, err := q.LockSession(ctx, sqlc.LockSessionParams{TenantID: lookup.TenantID, ID: lookup.SessionID})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if locked.DeletedAt.Valid {
		return ErrNotFound
	}
	turn, err := q.GetTurn(ctx, lookup)
	if err != nil {
		return err
	}
	if turn.Status != TurnInProgress || turn.CancelRequestedAt.Valid {
		return ErrTurnConflict
	}
	for _, row := range rows {
		row.SessionID, row.TurnID, row.EnvironmentID = lookup.SessionID, lookup.ID, environment
		if err := q.StageSessionArtifact(ctx, row); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func captureArtifactArchive(ctx context.Context, tx pgx.Tx, input io.Reader) ([]sqlc.StageSessionArtifactParams, error) {
	archive := tar.NewReader(input)
	objects := tx.LargeObjects()
	rows := make([]sqlc.StageSessionArtifactParams, 0)
	seen := make(map[string]bool)
	var total int64
	for {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag != tar.TypeReg || !strings.HasPrefix(header.Name, "outputs/") || !fs.ValidPath(header.Name) || strings.ContainsAny(header.Name, "\\\x00\r\n") || len(header.Name) > 4096 || header.Size < 0 || header.Size > MaxArtifactBytes || header.Size > MaxArtifactBatchBytes-total || len(rows) >= 4096 || seen[header.Name] {
			return nil, ErrInvalidInput
		}
		seen[header.Name] = true
		total += header.Size
		oid, err := objects.Create(ctx, 0)
		if err != nil {
			return nil, err
		}
		body, err := objects.Open(ctx, oid, pgx.LargeObjectModeWrite)
		if err != nil {
			return nil, err
		}
		writer := newSourceFileWriter(body)
		if _, err := io.CopyN(writer, archive, header.Size); err != nil {
			return nil, err
		}
		if err := body.Close(); err != nil {
			return nil, err
		}
		rows = append(rows, sqlc.StageSessionArtifactParams{ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Path: "/workspace/" + header.Name, SizeBytes: writer.size, BodyOid: pgtype.Uint32{Uint32: oid, Valid: true}, Sha256: hex.EncodeToString(writer.hash.Sum(nil))})
	}
	// Require transport EOF after the archive trailer, including confirmed helper exit.
	padding, err := io.ReadAll(io.LimitReader(input, 32769))
	if err != nil {
		return nil, err
	}
	if len(padding) > 32768 {
		return nil, ErrInvalidInput
	}
	for _, b := range padding {
		if b != 0 {
			return nil, ErrInvalidInput
		}
	}
	return rows, nil
}
