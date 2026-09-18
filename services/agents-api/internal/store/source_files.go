package store

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
)

const MaxSourceFileBytes int64 = 512 << 20

var ErrSourceFileTooLarge = errors.New("source file exceeds storage limit")

type SourceFile struct {
	ID        string
	Filename  string
	Purpose   string
	SizeBytes int64
	CreatedAt time.Time
}

type SourceFileUpload struct {
	Filename string
	Purpose  string
}

type SourceFilePage struct {
	Files      []SourceFile
	NextCursor string
}

// CreateSourceFile commits only after the complete upload envelope has validated.
func (s *Store) CreateSourceFile(ctx context.Context, tenantID string, upload func(io.Writer) (SourceFileUpload, error)) (SourceFile, error) {
	tenant, err := parseID(tenantID)
	if err != nil || upload == nil {
		return SourceFile{}, ErrInvalidInput
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SourceFile{}, err
	}
	defer tx.Rollback(context.Background())
	objects := tx.LargeObjects()
	oid, err := objects.Create(ctx, 0)
	if err != nil {
		return SourceFile{}, err
	}
	body, err := objects.Open(ctx, oid, pgx.LargeObjectModeWrite)
	if err != nil {
		return SourceFile{}, err
	}
	writer := newSourceFileWriter(body)
	input, err := upload(writer)
	if writer.err != nil {
		return SourceFile{}, writer.err
	}
	if err != nil {
		return SourceFile{}, err
	}
	if !validSourceFilename(input.Filename) || input.Purpose != "user_data" {
		return SourceFile{}, ErrInvalidInput
	}
	if err := body.Close(); err != nil {
		return SourceFile{}, err
	}
	row, err := s.queries.WithTx(tx).CreateSourceFile(ctx, sqlc.CreateSourceFileParams{
		ID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, TenantID: tenant,
		Filename: input.Filename, Purpose: input.Purpose, BodyOid: pgtype.Uint32{Uint32: oid, Valid: true},
		SizeBytes: writer.size, Sha256: hex.EncodeToString(writer.hash.Sum(nil)),
	})
	if err != nil {
		return SourceFile{}, fmt.Errorf("create source file: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SourceFile{}, err
	}
	return sourceFileFromRow(row), nil
}

func (s *Store) GetSourceFile(ctx context.Context, tenantID, fileID string) (SourceFile, error) {
	tenant, id, err := sourceFileIDs(tenantID, fileID)
	if err != nil {
		return SourceFile{}, err
	}
	row, err := s.queries.GetSourceFile(ctx, sqlc.GetSourceFileParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return SourceFile{}, ErrNotFound
	}
	if err != nil {
		return SourceFile{}, err
	}
	return sourceFileFromRow(row), nil
}

func (s *Store) ListSourceFiles(ctx context.Context, tenantID, cursor string, limit int, ascending bool, purpose *string) (SourceFilePage, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return SourceFilePage{}, err
	}
	if limit < 1 || limit > 10000 {
		return SourceFilePage{}, fmt.Errorf("%w: internal page size must be 1..10000", ErrInvalidInput)
	}
	params := sqlc.ListSourceFilesParams{
		TenantID: tenant, PageLimit: int32(limit + 1), Ascending: ascending,
		AfterID: pgtype.UUID{Valid: true},
	}
	if purpose != nil {
		if !utf8.ValidString(*purpose) || strings.ContainsRune(*purpose, '\x00') {
			return SourceFilePage{}, ErrInvalidInput
		}
		params.Purpose = pgtype.Text{String: *purpose, Valid: true}
	}
	if cursor != "" {
		after, err := s.GetSourceFile(ctx, tenantID, cursor)
		if err != nil {
			return SourceFilePage{}, err
		}
		params.AfterCreated = pgtype.Timestamptz{Time: after.CreatedAt, Valid: true}
		_, params.AfterID, _ = sourceFileIDs(tenantID, after.ID)
	}
	rows, err := s.queries.ListSourceFiles(ctx, params)
	if err != nil {
		return SourceFilePage{}, fmt.Errorf("list source files: %w", err)
	}
	page := SourceFilePage{Files: make([]SourceFile, 0, min(limit, len(rows)))}
	if len(rows) > limit {
		page.NextCursor = sourceFileFromRow(rows[limit-1]).ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		page.Files = append(page.Files, sourceFileFromRow(row))
	}
	return page, nil
}

// ReadSourceFile retains an authorized immutable snapshot during concurrent deletion.
func (s *Store) ReadSourceFile(ctx context.Context, tenantID, fileID string, consume func(SourceFile, io.Reader) error) error {
	tenant, id, err := sourceFileIDs(tenantID, fileID)
	if err != nil {
		return err
	}
	if consume == nil {
		return ErrInvalidInput
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	row, err := s.queries.WithTx(tx).GetSourceFile(ctx, sqlc.GetSourceFileParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	objects := tx.LargeObjects()
	body, err := objects.Open(ctx, row.BodyOid.Uint32, pgx.LargeObjectModeRead)
	if err != nil {
		return err
	}
	if err := consume(sourceFileFromRow(row), body); err != nil {
		return err
	}
	if err := body.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DeleteSourceFile(ctx context.Context, tenantID, fileID string) error {
	tenant, id, err := sourceFileIDs(tenantID, fileID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	oid, err := s.queries.WithTx(tx).DeleteSourceFile(ctx, sqlc.DeleteSourceFileParams{TenantID: tenant, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	objects := tx.LargeObjects()
	if err := objects.Unlink(ctx, oid.Uint32); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func sourceFileIDs(tenantID, fileID string) (pgtype.UUID, pgtype.UUID, error) {
	tenant, err := parseID(tenantID)
	if err != nil {
		return tenant, pgtype.UUID{}, err
	}
	id, err := uuid.Parse(strings.TrimPrefix(fileID, "file-"))
	if err != nil || id == uuid.Nil || fileID != "file-"+id.String() {
		return tenant, pgtype.UUID{}, ErrNotFound
	}
	return tenant, pgtype.UUID{Bytes: id, Valid: true}, nil
}

func validSourceFilename(name string) bool {
	return len(name) >= 1 && len(name) <= 1024 && utf8.ValidString(name) && !strings.ContainsRune(name, '\x00')
}

func sourceFileFromRow(row sqlc.SourceFile) SourceFile {
	return SourceFile{ID: "file-" + uuid.UUID(row.ID.Bytes).String(), Filename: row.Filename,
		Purpose: row.Purpose, SizeBytes: row.SizeBytes, CreatedAt: row.CreatedAt.Time}
}
