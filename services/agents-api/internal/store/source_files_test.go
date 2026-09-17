package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sourceObjectCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_largeobject_metadata").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func uploadSource(data []byte) func(io.Writer) (SourceFileUpload, error) {
	return func(w io.Writer) (SourceFileUpload, error) {
		_, err := w.Write(data)
		return SourceFileUpload{Filename: "source.bin", Purpose: "user_data"}, err
	}
}

func TestSourceFilesPersistScopeAndDelete(t *testing.T) {
	s, pool := testStore(t)
	tenant, foreign := uuid.NewString(), uuid.NewString()
	before := sourceObjectCount(t, pool)
	for _, data := range [][]byte{{}, {0, 1, 255}, bytes.Repeat([]byte("binary\x00"), 300000)} {
		file, err := s.CreateSourceFile(t.Context(), tenant, uploadSource(data))
		if err != nil || file.SizeBytes != int64(len(data)) || file.Filename != "source.bin" || !strings.HasPrefix(file.ID, "file-") || file.CreatedAt.IsZero() {
			t.Fatalf("create: %+v %v", file, err)
		}
		if _, err := s.GetSourceFile(t.Context(), foreign, file.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign metadata: %v", err)
		}
		if err := s.ReadSourceFile(t.Context(), foreign, file.ID, func(SourceFile, io.Reader) error {
			t.Fatal("foreign content callback reached")
			return nil
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign content: %v", err)
		}
		if err := s.DeleteSourceFile(t.Context(), foreign, file.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
		reopened := New(pool)
		if got, err := reopened.GetSourceFile(t.Context(), tenant, file.ID); err != nil || got != file {
			t.Fatalf("metadata: %+v %v", got, err)
		}
		if err := reopened.ReadSourceFile(t.Context(), tenant, file.ID, func(meta SourceFile, r io.Reader) error {
			got, err := io.ReadAll(r)
			if meta != file || !bytes.Equal(got, data) {
				t.Error("persisted contents differ")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteSourceFile(t.Context(), tenant, file.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSourceFile(t.Context(), tenant, file.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted metadata: %v", err)
		}
		if err := s.DeleteSourceFile(t.Context(), tenant, file.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("repeated delete: %v", err)
		}
	}
	if got := sourceObjectCount(t, pool); got != before {
		t.Fatalf("orphaned objects: %d -> %d", before, got)
	}
}

func TestSourceFilesRollbackInvalidOrInterruptedUpload(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	before := sourceObjectCount(t, pool)
	for _, reason := range []string{"body", "purpose", "filename", "cancel", "ignored-write-error"} {
		t.Run(reason, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			_, err := s.CreateSourceFile(ctx, tenant, func(w io.Writer) (SourceFileUpload, error) {
				if _, err := w.Write([]byte("not committed")); err != nil {
					return SourceFileUpload{}, err
				}
				input := SourceFileUpload{Filename: "source.bin", Purpose: "user_data"}
				switch reason {
				case "body":
					return input, io.ErrUnexpectedEOF
				case "purpose":
					input.Purpose = "not-supported"
				case "filename":
					input.Filename = "bad\x00name"
				case "cancel":
					cancel()
				case "ignored-write-error":
					bounded := w.(*sourceFileWriter)
					bounded.size = MaxSourceFileBytes
					_, _ = w.Write([]byte("over limit"))
				}
				return input, nil
			})
			if err == nil {
				t.Fatal("invalid upload committed")
			}
			if got := sourceObjectCount(t, pool); got != before {
				t.Fatalf("rollback orphan: %d -> %d", before, got)
			}
		})
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM source_files WHERE tenant_id=$1", tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed upload left metadata: %d %v", count, err)
	}
}

func TestSourceFileReadAdmittedBeforeDeletionCompletes(t *testing.T) {
	s, pool := testStore(t)
	tenant := uuid.NewString()
	data := bytes.Repeat([]byte("immutable\x00"), 10000)
	file, err := s.CreateSourceFile(t.Context(), tenant, uploadSource(data))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReadSourceFile(t.Context(), tenant, file.ID, func(_ SourceFile, r io.Reader) error {
		prefix := make([]byte, 1)
		if _, err := io.ReadFull(r, prefix); err != nil {
			return err
		}
		if err := New(pool).DeleteSourceFile(t.Context(), tenant, file.ID); err != nil {
			return err
		}
		if _, err := s.GetSourceFile(t.Context(), tenant, file.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("new read after delete: %v", err)
		}
		rest, err := io.ReadAll(r)
		if !bytes.Equal(append(prefix, rest...), data) {
			t.Error("deletion damaged admitted read")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSourceFileLargeStream(t *testing.T) {
	if os.Getenv("PARSAR_TEST_SOURCE_FILE_LARGE") != "1" {
		t.Skip("opt-in 512 MiB source storage acceptance")
	}
	s, _ := testStore(t)
	tenant := uuid.NewString()
	chunk := bytes.Repeat([]byte("source\x00binary"), 20000)
	want := sha256.New()
	file, err := s.CreateSourceFile(t.Context(), tenant, func(w io.Writer) (SourceFileUpload, error) {
		for left := MaxSourceFileBytes; left > 0; {
			b := chunk[:min(int64(len(chunk)), left)]
			if _, err := w.Write(b); err != nil {
				return SourceFileUpload{}, err
			}
			want.Write(b)
			left -= int64(len(b))
		}
		return SourceFileUpload{Filename: "large.bin", Purpose: "user_data"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.DeleteSourceFile(context.Background(), tenant, file.ID); err != nil {
			t.Error(err)
		}
	})
	got := sha256.New()
	if err := s.ReadSourceFile(t.Context(), tenant, file.ID, func(meta SourceFile, r io.Reader) error {
		n, err := io.CopyBuffer(got, r, chunk)
		if n != MaxSourceFileBytes || meta.SizeBytes != n {
			t.Errorf("size: %d metadata: %d", n, meta.SizeBytes)
		}
		return err
	}); err != nil || !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
		t.Fatalf("large stream mismatch: %v", err)
	}
}
