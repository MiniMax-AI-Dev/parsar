package store

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func artifactArchive(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	for name, body := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func artifactTurn(t *testing.T, s *Store, kind string) (tenant, session, environment, turn string) {
	t.Helper()
	tenant = uuid.NewString()
	created, err := s.CreateSession(t.Context(), tenant, environmentInput("artifact", kind, "/workspace"))
	if err != nil {
		t.Fatal(err)
	}
	env, err := s.GetSessionEnvironment(t.Context(), tenant, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := submitMessage(t, s, tenant, created.ID, "artifact-turn")
	transition(t, s, tenant, created.ID, input.TurnID, TurnQueued, TurnInProgress)
	return tenant, created.ID, env.ID, input.TurnID
}

func TestSessionArtifactsPublishVersionScopeAndLifetime(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
	before := sourceObjectCount(t, pool)
	data := bytes.Repeat([]byte("immutable\x00"), 100000)
	archive := artifactArchive(t, map[string][]byte{"outputs/a.bin": data, "outputs/nested/empty": {}})
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(archive)); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || len(page.Artifacts) != 0 {
		t.Fatalf("private capture visible: %+v %v", page, err)
	}
	completed := transition(t, s, tenant, session, turn, TurnInProgress, TurnCompleted)
	page, err = s.ListSessionArtifacts(t.Context(), tenant, session, environment, "", 100, true)
	if err != nil || len(page.Artifacts) != 2 {
		t.Fatalf("published capture: %+v %v", page, err)
	}
	for _, a := range page.Artifacts {
		if a.SessionID != session || a.TurnID != turn || a.EnvironmentID != environment || !a.CreatedAt.Equal(completed.CompletedAt) {
			t.Fatalf("publication metadata: %+v", a)
		}
		foreign := uuid.NewString()
		if _, err := s.GetSessionArtifact(t.Context(), foreign, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign metadata: %v", err)
		}
		if _, err := s.GetSessionArtifact(t.Context(), tenant, uuid.NewString(), a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("wrong session metadata: %v", err)
		}
		if err := s.DeleteSessionArtifact(t.Context(), foreign, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign delete: %v", err)
		}
		if err := s.ReadSessionArtifact(t.Context(), foreign, session, a.ID, func(SessionArtifact, io.Reader) error {
			t.Error("foreign read reached content")
			return nil
		}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign read: %v", err)
		}
	}
	if _, err := s.ListSessionArtifacts(t.Context(), uuid.NewString(), session, "", "", 100, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign list: %v", err)
	}
	if empty, err := s.ListSessionArtifacts(t.Context(), tenant, session, uuid.NewString(), "", 100, false); err != nil || len(empty.Artifacts) != 0 {
		t.Fatalf("environment filter: %+v %v", empty, err)
	}
	// A later completed Turn publishes another immutable version of the same path.
	next := submitMessage(t, s, tenant, session, "version-two")
	transition(t, s, tenant, session, next.TurnID, TurnQueued, TurnInProgress)
	if err := s.StageTurnArtifacts(t.Context(), tenant, session, next.TurnID, environment, bytes.NewReader(artifactArchive(t, map[string][]byte{"outputs/a.bin": []byte("new")}))); err != nil {
		t.Fatal(err)
	}
	transition(t, s, tenant, session, next.TurnID, TurnInProgress, TurnCompleted)
	all, err := s.ListSessionArtifacts(t.Context(), tenant, session, "", "", 100, true)
	if err != nil || len(all.Artifacts) != 3 {
		t.Fatal(all, err)
	}
	for _, asc := range []bool{true, false} {
		var got []SessionArtifact
		cursor := ""
		for {
			part, err := s.ListSessionArtifacts(t.Context(), tenant, session, environment, cursor, 1, asc)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, part.Artifacts...)
			if part.NextCursor == "" {
				break
			}
			if len(got) > 3 {
				t.Fatal("pagination repeated artifacts")
			}
			cursor = part.NextCursor
		}
		want := append([]SessionArtifact(nil), all.Artifacts...)
		if !asc {
			want[0], want[2] = want[2], want[0]
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ordered pages differ: %+v %+v", got, want)
		}
	}
	// Expiration is a controlled fixture; stored reads must not touch Runtime.
	if _, err := pool.Exec(t.Context(), "UPDATE environments SET status='expired' WHERE id=$1", environment); err != nil {
		t.Fatal(err)
	}
	for _, a := range page.Artifacts {
		if err := New(pool).ReadSessionArtifact(t.Context(), tenant, session, a.ID, func(meta SessionArtifact, r io.Reader) error {
			if err := s.DeleteSessionArtifact(t.Context(), tenant, session, a.ID); err != nil {
				return err
			}
			body, err := io.ReadAll(r)
			want := data
			if a.Path == "/workspace/outputs/nested/empty" {
				want = nil
			}
			if meta != a || !bytes.Equal(body, want) {
				t.Error("expired/deleted artifact damaged admitted snapshot")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetSessionArtifact(t.Context(), tenant, session, a.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted metadata retained: %v", err)
		}
	}
	if err := s.DeleteSession(t.Context(), tenant, session); err != nil {
		t.Fatal(err)
	}
	if count := sourceObjectCount(t, pool); count != before {
		t.Fatalf("objects leaked: %d -> %d", before, count)
	}
}

type artifactReadError struct{}

func (artifactReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestSessionArtifactsRejectIncompleteAndUnownedCapture(t *testing.T) {
	s, pool := testStore(t)
	tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
	before := sourceObjectCount(t, pool)
	valid := artifactArchive(t, map[string][]byte{"outputs/a": []byte("data")})
	for name, body := range map[string]io.Reader{
		"transport-failure-after-valid-tar": io.MultiReader(bytes.NewReader(valid), artifactReadError{}),
		"truncated-body":                    bytes.NewReader(valid[:513]),
		"trailing-data":                     io.MultiReader(bytes.NewReader(valid), bytes.NewReader([]byte("not archive padding"))),
		"traversal":                         bytes.NewReader(artifactArchive(t, map[string][]byte{"outputs/../secret": []byte("no")})),
		"private-root":                      bytes.NewReader(artifactArchive(t, map[string][]byte{"secrets/key": []byte("no")})),
	} {
		t.Run(name, func(t *testing.T) {
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, body); err == nil {
				t.Fatal("invalid capture accepted")
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("rollback leaked objects: %d -> %d", before, count)
			}
		})
	}
	for _, ids := range [][4]string{{uuid.NewString(), session, turn, environment}, {tenant, session, turn, uuid.NewString()}} {
		if err := s.StageTurnArtifacts(t.Context(), ids[0], ids[1], ids[2], ids[3], artifactReadError{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unauthorized capture reached reader: %v", err)
		}
	}
	st, ss, se, sr := artifactTurn(t, s, "self_hosted")
	if err := s.StageTurnArtifacts(t.Context(), st, ss, sr, se, artifactReadError{}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("self_hosted publication allowed: %v", err)
	}
}

func TestSessionArtifactsDiscardTerminalPrivateCapture(t *testing.T) {
	for _, status := range []string{TurnFailed, TurnCancelled} {
		t.Run(status, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
			before := sourceObjectCount(t, pool)
			body := artifactArchive(t, map[string][]byte{"outputs/a": []byte("private")})
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(body)); err != nil {
				t.Fatal(err)
			}
			transition(t, s, tenant, session, turn, TurnInProgress, status)
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("terminal capture leaked objects: %d -> %d", before, count)
			}
			if err := s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, bytes.NewReader(body)); !errors.Is(err, ErrTurnConflict) {
				t.Fatalf("late capture accepted: %v", err)
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("late capture leaked objects: %d -> %d", before, count)
			}
		})
	}
}

func TestSessionArtifactTransferDoesNotBlockDeletionOrCancellation(t *testing.T) {
	for _, operation := range []string{"delete", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			s, pool := testStore(t)
			tenant, session, environment, turn := artifactTurn(t, s, "openai_hosted")
			before := sourceObjectCount(t, pool)
			reader, writer := io.Pipe()
			defer reader.Close()
			defer writer.Close()
			result := make(chan error, 1)
			go func() { result <- s.StageTurnArtifacts(t.Context(), tenant, session, turn, environment, reader) }()
			// A complete TAR arrives, but transport has not acknowledged success yet.
			if _, err := writer.Write(artifactArchive(t, map[string][]byte{"outputs/a": []byte("partial")})); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			want := ErrNotFound
			if operation == "delete" {
				if err := s.DeleteSession(ctx, tenant, session); err != nil {
					t.Fatalf("transfer blocked deletion: %v", err)
				}
			} else {
				if _, err := s.RequestCancel(ctx, tenant, session, "cancel-capture"); err != nil {
					t.Fatalf("transfer blocked cancellation: %v", err)
				}
				want = ErrTurnConflict
			}
			writer.Close()
			if err := <-result; !errors.Is(err, want) {
				t.Fatalf("late publication after %s: %v", operation, err)
			}
			if count := sourceObjectCount(t, pool); count != before {
				t.Fatalf("late capture leaked objects: %d -> %d", before, count)
			}
		})
	}
}
