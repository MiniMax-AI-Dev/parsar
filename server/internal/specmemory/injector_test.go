package specmemory

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeReader is the test stand-in for *store.Store. Pre-load rows/err
// per test case; call-log slices capture every Input the injector
// passes in so the test can assert request shape.
type fakeReader struct {
	specRows []store.SpecFragmentRead
	specErr  error
	specCall []store.ListWorkspaceSpecFragmentsInput

	userMemRows []store.MemoryRead
	userMemErr  error
	userMemCall []store.ListUserMemoriesInput

	projectMemRows []store.MemoryRead
	projectMemErr  error
	projectMemCall []store.ListProjectMemoriesInput

	userSinceRows []store.MemoryRead
	userSinceErr  error
	userSinceCall []store.ListUserMemoriesSinceInput

	projectSinceRows []store.MemoryRead
	projectSinceErr  error
	projectSinceCall []store.ListProjectMemoriesSinceInput
}

func (f *fakeReader) ListWorkspaceSpecFragments(_ context.Context, in store.ListWorkspaceSpecFragmentsInput) ([]store.SpecFragmentRead, error) {
	f.specCall = append(f.specCall, in)
	return f.specRows, f.specErr
}

func (f *fakeReader) ListUserMemories(_ context.Context, in store.ListUserMemoriesInput) ([]store.MemoryRead, error) {
	f.userMemCall = append(f.userMemCall, in)
	return f.userMemRows, f.userMemErr
}

func (f *fakeReader) ListProjectMemories(_ context.Context, in store.ListProjectMemoriesInput) ([]store.MemoryRead, error) {
	f.projectMemCall = append(f.projectMemCall, in)
	return f.projectMemRows, f.projectMemErr
}

func (f *fakeReader) ListUserMemoriesSince(_ context.Context, in store.ListUserMemoriesSinceInput) ([]store.MemoryRead, error) {
	f.userSinceCall = append(f.userSinceCall, in)
	return f.userSinceRows, f.userSinceErr
}

func (f *fakeReader) ListProjectMemoriesSince(_ context.Context, in store.ListProjectMemoriesSinceInput) ([]store.MemoryRead, error) {
	f.projectSinceCall = append(f.projectSinceCall, in)
	return f.projectSinceRows, f.projectSinceErr
}

func TestBuildSnapshotRequiresWorkspaceID(t *testing.T) {
	inj := NewInjector(&fakeReader{})
	_, err := inj.BuildSnapshot(context.Background(), SnapshotInput{UserID: "u"})
	if err == nil || !strings.Contains(err.Error(), "workspace_id") {
		t.Fatalf("expected workspace_id error, got %v", err)
	}
}

func TestBuildSnapshotRequiresUserID(t *testing.T) {
	inj := NewInjector(&fakeReader{})
	_, err := inj.BuildSnapshot(context.Background(), SnapshotInput{WorkspaceID: "w"})
	if err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("expected user_id error, got %v", err)
	}
}

func TestBuildSnapshotEmptyStoreReturnsGuideOnly(t *testing.T) {
	// MemoryWriteGuide must be emitted at SessionStart even when the
	// spec + memory blocks are empty.
	inj := NewInjector(&fakeReader{})
	got, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID:   "w",
		WorkspaceName: "acme",
		UserID:        "u",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SpecBlock != "" {
		t.Errorf("SpecBlock should be empty, got %q", got.SpecBlock)
	}
	if got.MemoryBlock != "" {
		t.Errorf("MemoryBlock should be empty, got %q", got.MemoryBlock)
	}
	if !strings.Contains(got.MemoryWriteGuide, "parsar memory add") {
		t.Errorf("MemoryWriteGuide should always be present, got %q", got.MemoryWriteGuide)
	}
	if got.IncrementalMemory != "" {
		t.Errorf("IncrementalMemory should be empty at SessionStart, got %q", got.IncrementalMemory)
	}
}

func TestBuildSnapshotFull(t *testing.T) {
	reader := &fakeReader{
		specRows: []store.SpecFragmentRead{
			{ID: "f1", Title: "Stack", Body: "Go + Postgres", Tags: []string{"backend"}, Source: "manual"},
		},
		userMemRows: []store.MemoryRead{
			{ID: "m1", Scope: "user", MemoryType: "user", Body: "senior backend dev", Source: "manual"},
		},
		projectMemRows: []store.MemoryRead{
			{ID: "m2", Scope: "project", MemoryType: "project", Body: "migrating to grpc", Why: "REST timeout SLOs", Source: "agent"},
		},
	}
	inj := NewInjector(reader)
	got, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID:   "w",
		WorkspaceName: "acme",
		UserID:        "u",
		ProjectID:     "p",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.SpecBlock, `<spec workspace="acme">`) {
		t.Errorf("SpecBlock missing workspace tag: %s", got.SpecBlock)
	}
	if !strings.Contains(got.SpecBlock, "### Stack") {
		t.Errorf("SpecBlock missing fragment title: %s", got.SpecBlock)
	}
	if !strings.Contains(got.MemoryBlock, "## user") || !strings.Contains(got.MemoryBlock, "senior backend dev") {
		t.Errorf("MemoryBlock missing user-type entry: %s", got.MemoryBlock)
	}
	if !strings.Contains(got.MemoryBlock, "## project") || !strings.Contains(got.MemoryBlock, "migrating to grpc") {
		t.Errorf("MemoryBlock missing project-type entry: %s", got.MemoryBlock)
	}
	if !strings.Contains(got.MemoryBlock, "(Why: REST timeout SLOs)") {
		t.Errorf("MemoryBlock should suffix Why for project memories: %s", got.MemoryBlock)
	}
}

func TestBuildSnapshotSkipsProjectWhenIDEmpty(t *testing.T) {
	reader := &fakeReader{}
	inj := NewInjector(reader)
	if _, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w",
		UserID:      "u",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reader.projectMemCall) != 0 {
		t.Errorf("ListProjectMemories should not have been called, got %d calls", len(reader.projectMemCall))
	}
	if len(reader.specCall) != 1 || len(reader.userMemCall) != 1 {
		t.Errorf("spec + user memory should still be queried, got spec=%d user=%d", len(reader.specCall), len(reader.userMemCall))
	}
}

func TestBuildSnapshotPropagatesLimits(t *testing.T) {
	reader := &fakeReader{}
	inj := NewInjector(reader)
	if _, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID:        "w",
		UserID:             "u",
		ProjectID:          "p",
		SpecLimit:          5,
		UserMemoryLimit:    7,
		ProjectMemoryLimit: 9,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reader.specCall[0].Limit != 5 {
		t.Errorf("SpecLimit not passed through, got %d", reader.specCall[0].Limit)
	}
	if reader.userMemCall[0].Limit != 7 {
		t.Errorf("UserMemoryLimit not passed through, got %d", reader.userMemCall[0].Limit)
	}
	if reader.projectMemCall[0].Limit != 9 {
		t.Errorf("ProjectMemoryLimit not passed through, got %d", reader.projectMemCall[0].Limit)
	}
}

func TestBuildSnapshotWrapsSpecError(t *testing.T) {
	sentinel := errors.New("boom")
	inj := NewInjector(&fakeReader{specErr: sentinel})
	_, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w",
		UserID:      "u",
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap sentinel, got %v", err)
	}
	if !strings.Contains(err.Error(), "snapshot spec list") {
		t.Errorf("error should label the failing step, got %v", err)
	}
}

func TestBuildSnapshotWrapsUserMemoryError(t *testing.T) {
	sentinel := errors.New("user-boom")
	inj := NewInjector(&fakeReader{userMemErr: sentinel})
	_, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w",
		UserID:      "u",
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap sentinel, got %v", err)
	}
}

func TestBuildSnapshotWrapsProjectMemoryError(t *testing.T) {
	sentinel := errors.New("proj-boom")
	inj := NewInjector(&fakeReader{projectMemErr: sentinel})
	_, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w",
		UserID:      "u",
		ProjectID:   "p",
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap sentinel, got %v", err)
	}
}

func TestBuildSnapshotDropsBadEnumRows(t *testing.T) {
	// Defense against a poisoned DB value sneaking past the un-CHECKed
	// text column.
	reader := &fakeReader{
		specRows: []store.SpecFragmentRead{
			{ID: "good", Title: "Real", Body: "real body", Source: "manual"},
			{ID: "bad", Title: "Poison", Body: "poison body", Source: "not-a-real-source"},
		},
		userMemRows: []store.MemoryRead{
			{ID: "ok", Scope: "user", MemoryType: "user", Body: "ok", Source: "manual"},
			{ID: "bad-scope", Scope: "weird", MemoryType: "user", Body: "drop me", Source: "manual"},
			{ID: "bad-type", Scope: "user", MemoryType: "weird", Body: "drop me too", Source: "manual"},
			{ID: "bad-src", Scope: "user", MemoryType: "user", Body: "drop me three", Source: "weird"},
		},
	}
	inj := NewInjector(reader)
	got, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w", UserID: "u", WorkspaceName: "acme",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.SpecBlock, "Real") {
		t.Errorf("good fragment dropped: %s", got.SpecBlock)
	}
	if strings.Contains(got.SpecBlock, "Poison") || strings.Contains(got.SpecBlock, "poison body") {
		t.Errorf("bad-source fragment leaked: %s", got.SpecBlock)
	}
	if !strings.Contains(got.MemoryBlock, "- ok") {
		t.Errorf("good memory dropped: %s", got.MemoryBlock)
	}
	for _, bad := range []string{"drop me", "drop me too", "drop me three"} {
		if strings.Contains(got.MemoryBlock, bad) {
			t.Errorf("bad-enum memory leaked (%q): %s", bad, got.MemoryBlock)
		}
	}
}

func TestBuildSnapshotMergesUserAndProjectMemories(t *testing.T) {
	reader := &fakeReader{
		userMemRows: []store.MemoryRead{
			{ID: "u1", Scope: "user", MemoryType: "user", Body: "from user", Source: "manual"},
		},
		projectMemRows: []store.MemoryRead{
			{ID: "p1", Scope: "project", MemoryType: "user", Body: "from project", Source: "manual"},
		},
	}
	inj := NewInjector(reader)
	got, err := inj.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "w", UserID: "u", ProjectID: "p",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.MemoryBlock, "from user") {
		t.Errorf("user memory missing: %s", got.MemoryBlock)
	}
	if !strings.Contains(got.MemoryBlock, "from project") {
		t.Errorf("project memory missing: %s", got.MemoryBlock)
	}
}

func TestBuildIncrementalRequiresUserID(t *testing.T) {
	inj := NewInjector(&fakeReader{})
	_, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		Since: time.Now(),
	})
	if err == nil || !strings.Contains(err.Error(), "user_id") {
		t.Fatalf("expected user_id error, got %v", err)
	}
}

func TestBuildIncrementalRequiresSince(t *testing.T) {
	inj := NewInjector(&fakeReader{})
	_, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID: "u",
	})
	if err == nil || !strings.Contains(err.Error(), "since cursor") {
		t.Fatalf("expected since cursor error, got %v", err)
	}
}

func TestBuildIncrementalEmptyReturnsEmptyBundle(t *testing.T) {
	inj := NewInjector(&fakeReader{})
	got, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID: "u",
		Since:  time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.IncrementalMemory != "" {
		t.Errorf("IncrementalMemory should be empty, got %q", got.IncrementalMemory)
	}
	if got.SpecBlock != "" || got.MemoryBlock != "" || got.MemoryWriteGuide != "" {
		t.Errorf("BuildIncremental must not populate snapshot fields, got %+v", got)
	}
}

func TestBuildIncrementalSkipsProjectWhenIDEmpty(t *testing.T) {
	reader := &fakeReader{}
	inj := NewInjector(reader)
	if _, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID: "u",
		Since:  time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reader.projectSinceCall) != 0 {
		t.Errorf("ListProjectMemoriesSince should not have been called, got %d calls", len(reader.projectSinceCall))
	}
	if len(reader.userSinceCall) != 1 {
		t.Errorf("user-since should still run, got %d calls", len(reader.userSinceCall))
	}
}

func TestBuildIncrementalRendersDeltaInIncrementalTag(t *testing.T) {
	reader := &fakeReader{
		userSinceRows: []store.MemoryRead{
			{ID: "u1", Scope: "user", MemoryType: "user", Body: "just learned", Source: "agent"},
		},
		projectSinceRows: []store.MemoryRead{
			{ID: "p1", Scope: "project", MemoryType: "project", Body: "scope cut", Why: "deadline", Source: "agent"},
		},
	}
	inj := NewInjector(reader)
	got, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID:    "u",
		ProjectID: "p",
		Since:     time.Now().Add(-time.Hour),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.IncrementalMemory, "<memory-incremental>") {
		t.Errorf("expected <memory-incremental> wrapper, got %q", got.IncrementalMemory)
	}
	if !strings.Contains(got.IncrementalMemory, "just learned") {
		t.Errorf("user delta missing: %q", got.IncrementalMemory)
	}
	if !strings.Contains(got.IncrementalMemory, "scope cut") {
		t.Errorf("project delta missing: %q", got.IncrementalMemory)
	}
	if !strings.Contains(got.IncrementalMemory, "(Why: deadline)") {
		t.Errorf("Why suffix missing on project delta: %q", got.IncrementalMemory)
	}
}

func TestBuildIncrementalPropagatesLimitAndSince(t *testing.T) {
	reader := &fakeReader{}
	inj := NewInjector(reader)
	cursor := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID:    "u",
		ProjectID: "p",
		Since:     cursor,
		Limit:     42,
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reader.userSinceCall[0].Since.Equal(cursor) {
		t.Errorf("user since not propagated, got %v want %v", reader.userSinceCall[0].Since, cursor)
	}
	if reader.userSinceCall[0].Limit != 42 {
		t.Errorf("user limit not propagated, got %d", reader.userSinceCall[0].Limit)
	}
	if !reader.projectSinceCall[0].Since.Equal(cursor) {
		t.Errorf("project since not propagated, got %v", reader.projectSinceCall[0].Since)
	}
	if reader.projectSinceCall[0].Limit != 42 {
		t.Errorf("project limit not propagated, got %d", reader.projectSinceCall[0].Limit)
	}
}

func TestBuildIncrementalWrapsUserError(t *testing.T) {
	sentinel := errors.New("user-since-boom")
	inj := NewInjector(&fakeReader{userSinceErr: sentinel})
	_, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID: "u",
		Since:  time.Now().Add(-time.Hour),
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap sentinel, got %v", err)
	}
}

func TestBuildIncrementalWrapsProjectError(t *testing.T) {
	sentinel := errors.New("project-since-boom")
	inj := NewInjector(&fakeReader{projectSinceErr: sentinel})
	_, err := inj.BuildIncremental(context.Background(), IncrementalInput{
		UserID:    "u",
		ProjectID: "p",
		Since:     time.Now().Add(-time.Hour),
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("error chain should wrap sentinel, got %v", err)
	}
}

func TestConvertMemoriesDeduplicatesByID(t *testing.T) {
	// Cross-scope duplicate (same ID surfacing in both queries) must
	// render exactly once. Defensive — each row has a single scope today.
	got := convertMemories([]store.MemoryRead{
		{ID: "a", Scope: "user", MemoryType: "user", Body: "first", Source: "manual"},
		{ID: "a", Scope: "user", MemoryType: "user", Body: "dup", Source: "manual"},
		{ID: "b", Scope: "project", MemoryType: "user", Body: "other", Source: "manual"},
	})
	if len(got) != 2 {
		t.Fatalf("expected 2 unique memories, got %d", len(got))
	}
	if got[0].ID != "a" || got[1].ID != "b" {
		t.Errorf("dedupe should keep first occurrence, got %+v", got)
	}
	if got[0].Body != "first" {
		t.Errorf("dedupe should preserve first body, got %q", got[0].Body)
	}
}

func TestFragmentFromStoreRowRejectsUnknownSource(t *testing.T) {
	_, ok := FragmentFromStoreRow(store.SpecFragmentRead{ID: "x", Source: "bogus"})
	if ok {
		t.Error("FragmentFromStoreRow should reject unknown Source")
	}
}

func TestMemoryFromStoreRowRejectsAnyBadEnum(t *testing.T) {
	cases := []struct {
		name string
		row  store.MemoryRead
	}{
		{"bad scope", store.MemoryRead{Scope: "bogus", MemoryType: "user", Source: "manual"}},
		{"bad type", store.MemoryRead{Scope: "user", MemoryType: "bogus", Source: "manual"}},
		{"bad source", store.MemoryRead{Scope: "user", MemoryType: "user", Source: "bogus"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, ok := MemoryFromStoreRow(c.row); ok {
				t.Errorf("MemoryFromStoreRow should reject %s row", c.name)
			}
		})
	}
}

func TestMemoryFromStoreRowAcceptsValid(t *testing.T) {
	row := store.MemoryRead{
		ID:         "m",
		Scope:      "user",
		UserID:     "u",
		MemoryType: "feedback",
		Body:       "do not mock the db",
		Why:        "had a prod regression last quarter",
		Source:     "agent",
		AgentActor: "claude:proj1",
	}
	got, ok := MemoryFromStoreRow(row)
	if !ok {
		t.Fatal("MemoryFromStoreRow should accept valid row")
	}
	if got.Scope != ScopeUser {
		t.Errorf("scope conv: got %q want %q", got.Scope, ScopeUser)
	}
	if got.MemoryType != MemoryTypeFeedback {
		t.Errorf("type conv: got %q want %q", got.MemoryType, MemoryTypeFeedback)
	}
	if got.Source != SourceAgent {
		t.Errorf("source conv: got %q want %q", got.Source, SourceAgent)
	}
	if got.Why != row.Why {
		t.Errorf("why passthrough lost data, got %q", got.Why)
	}
}
