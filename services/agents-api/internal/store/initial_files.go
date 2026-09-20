package store

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const MaxInitialFileBytes = 50 << 20

// InitialFile keeps confidential input separate from ordinary Session configuration.
type InitialFile struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	FileID string `json:"file_id,omitempty"`
	Data   []byte `json:"data,omitempty"`
}

type InitialFileMetadata struct {
	ID        string `json:"id,omitempty"`
	Type      string `json:"type"`
	Path      string `json:"path"`
	FileID    string `json:"file_id,omitempty"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
}

func ValidateInitialFiles(files []InitialFile) error {
	if len(files) > 50 {
		return ErrInvalidInput
	}
	total := 0
	seen := map[string]bool{}
	for _, f := range files {
		if !utf8.ValidString(f.Path) || strings.ContainsAny(f.Path, "\\\x00\r\n") || len(f.Path) > 4096 || !strings.HasPrefix(f.Path, "/workspace/") || path.Clean(f.Path) != f.Path || seen[f.Path] {
			return ErrInvalidInput
		}
		seen[f.Path] = true
		switch f.Type {
		case "inline":
			if f.FileID != "" || len(f.Data) > 5<<20 {
				return ErrInvalidInput
			}
			total += len(f.Data)
		case "file_id":
			if f.FileID == "" || len(f.Data) != 0 {
				return ErrInvalidInput
			}
		default:
			return ErrInvalidInput
		}
	}
	if total > 10<<20 {
		return ErrInvalidInput
	}
	return nil
}

func initialFileMetadata(files []InitialFile) []InitialFileMetadata {
	result := make([]InitialFileMetadata, 0, len(files))
	for _, f := range files {
		m := InitialFileMetadata{Type: f.Type, Path: f.Path, FileID: f.FileID}
		if f.Type == "inline" {
			size := int64(len(f.Data))
			m.SizeBytes = &size
		}
		result = append(result, m)
	}
	return result
}

func (s *Store) sealTemplateFiles(tenant, id string, files []InitialFile) ([]byte, []byte, error) {
	if err := ValidateInitialFiles(files); err != nil {
		return nil, nil, err
	}
	metadata, err := json.Marshal(initialFileMetadata(files))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return metadata, nil, nil
	}
	input, err := json.Marshal(files)
	if err != nil {
		return nil, nil, err
	}
	encrypted, err := s.credentialCipher.SealEnvironmentFile(input, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "environment_template", OwnerID: id, FileID: "files"})
	return metadata, encrypted, err
}

// ResolveEnvironmentTemplate reads one atomic snapshot; public reads need no decryption key.
func (s *Store) ResolveEnvironmentTemplate(ctx context.Context, tenant, id string) (EnvironmentTemplate, []InitialFile, error) {
	lookup, err := deviceLookup(tenant, id)
	if err != nil {
		return EnvironmentTemplate{}, nil, ErrNotFound
	}
	row, err := s.queries.ResolveEnvironmentTemplate(ctx, sqlc.ResolveEnvironmentTemplateParams{TenantID: lookup.TenantID, ID: lookup.ID})
	value, err := templateFromRow(templateMetadataRow{ID: row.ID, TenantID: row.TenantID, Name: row.Name, NetworkAccess: row.NetworkAccess, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Files: row.Files}, err)
	if err != nil {
		return value, nil, err
	}
	if len(row.FileContents) == 0 {
		if len(value.Files) > 0 {
			return value, nil, ErrInvalidInput
		}
		return value, nil, nil
	}
	plain, err := s.credentialCipher.OpenEnvironmentFile(row.FileContents, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "environment_template", OwnerID: id, FileID: "files"})
	if err != nil {
		return value, nil, err
	}
	var files []InitialFile
	if json.Unmarshal(plain, &files) != nil || ValidateInitialFiles(files) != nil {
		return value, nil, ErrInvalidInput
	}
	return value, files, nil
}

func (s *Store) saveInitialFiles(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, tenant string, session pgtype.UUID, files []InitialFile) ([]byte, error) {
	if err := ValidateInitialFiles(files); err != nil {
		return nil, err
	}
	metadata := initialFileMetadata(files)
	for i, f := range files {
		body := f.Data
		if f.Type == "file_id" {
			sourceTenant, sourceID, err := sourceFileIDs(tenant, f.FileID)
			if err != nil {
				return nil, err
			}
			source, err := q.LockInitialSourceFile(ctx, sqlc.LockInitialSourceFileParams{TenantID: sourceTenant, ID: sourceID})
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrNotFound
			}
			if err != nil {
				return nil, err
			}
			err = consumeSourceFile(ctx, tx, source, func(source SourceFile, reader io.Reader) error {
				if source.SizeBytes > MaxInitialFileBytes {
					return ErrInvalidInput
				}
				var err error
				body, err = io.ReadAll(io.LimitReader(reader, MaxInitialFileBytes+1))
				if err == nil && (len(body) > MaxInitialFileBytes || int64(len(body)) != source.SizeBytes) {
					return ErrInvalidInput
				}
				return err
			})
			if err != nil {
				return nil, err
			}
		}
		id := uuid.NewString()
		size := int64(len(body))
		metadata[i].ID = id
		metadata[i].SizeBytes = &size
		encrypted, err := s.credentialCipher.SealEnvironmentFile(body, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "session", OwnerID: uuid.UUID(session.Bytes).String(), FileID: id})
		if err != nil {
			return nil, err
		}
		fileID, _ := parseID(id)
		if err := q.CreateInitialEnvironmentFile(ctx, sqlc.CreateInitialEnvironmentFileParams{ID: fileID, SessionID: session, Position: int32(i), Path: f.Path, SizeBytes: size, Contents: encrypted}); err != nil {
			return nil, err
		}
	}
	return json.Marshal(metadata)
}

// ReadInitialEnvironmentFile decrypts only the next frozen file, bounding memory per installation.
func (s *Store) ReadInitialEnvironmentFile(ctx context.Context, tenant, session string, position int) (InitialFileMetadata, []byte, error) {
	lookup, err := deviceLookup(tenant, session)
	if err != nil {
		return InitialFileMetadata{}, nil, err
	}
	row, err := s.queries.GetInitialEnvironmentFile(ctx, sqlc.GetInitialEnvironmentFileParams{TenantID: lookup.TenantID, SessionID: lookup.ID, Position: int32(position)})
	if err != nil {
		return InitialFileMetadata{}, nil, err
	}
	id := uuid.UUID(row.ID.Bytes).String()
	body, err := s.credentialCipher.OpenEnvironmentFile(row.Contents, credentialcrypto.EnvironmentFileBinding{TenantID: tenant, Resource: "session", OwnerID: session, FileID: id})
	if err == nil && int64(len(body)) != row.SizeBytes {
		err = ErrInvalidInput
	}
	return InitialFileMetadata{ID: id, Path: row.Path, SizeBytes: &row.SizeBytes}, body, err
}
