package specmemory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// fakeService bundles fakeReader (read surface used by Injector) with
// a writer-side fake — together they satisfy specmemory.Store.
type fakeService struct {
	fakeReader

	insertedSpec    []store.InsertSpecFragmentInput
	insertSpecRow   store.SpecFragmentRead
	insertSpecErr   error
	getSpecRow      store.SpecFragmentRead
	getSpecFound    bool
	getSpecErr      error
	updateSpecIn    []store.UpdateSpecFragmentInput
	updateSpecRow   store.SpecFragmentRead
	updateSpecFound bool
	updateSpecErr   error
	deleteSpecCalls []string
	deleteSpecErr   error

	insertedMemory    []store.InsertMemoryInput
	insertMemoryRow   store.MemoryRead
	insertMemoryErr   error
	getMemoryRow      store.MemoryRead
	getMemoryFound    bool
	getMemoryErr      error
	updateMemoryIn    []store.UpdateMemoryInput
	updateMemoryRow   store.MemoryRead
	updateMemoryFound bool
	updateMemoryErr   error
	deleteMemoryCalls []string
	deleteMemoryErr   error
}

func (f *fakeService) GetSpecFragment(_ context.Context, id string) (store.SpecFragmentRead, bool, error) {
	return f.getSpecRow, f.getSpecFound, f.getSpecErr
}

func (f *fakeService) InsertSpecFragment(_ context.Context, in store.InsertSpecFragmentInput) (store.SpecFragmentRead, error) {
	f.insertedSpec = append(f.insertedSpec, in)
	row := f.insertSpecRow
	// Mirror the input back into the row when no row was preloaded.
	if row.ID == "" {
		row = store.SpecFragmentRead{
			ID:          in.ID,
			WorkspaceID: in.WorkspaceID,
			Title:       in.Title,
			Body:        in.Body,
			Tags:        in.Tags,
			Source:      in.Source,
			CreatedBy:   in.CreatedBy,
			AgentActor:  in.AgentActor,
			CreatedAt:   in.Now,
			UpdatedAt:   in.Now,
		}
	}
	return row, f.insertSpecErr
}

func (f *fakeService) UpdateSpecFragment(_ context.Context, in store.UpdateSpecFragmentInput) (store.SpecFragmentRead, bool, error) {
	f.updateSpecIn = append(f.updateSpecIn, in)
	return f.updateSpecRow, f.updateSpecFound, f.updateSpecErr
}

func (f *fakeService) SoftDeleteSpecFragment(_ context.Context, id string, _ time.Time) error {
	f.deleteSpecCalls = append(f.deleteSpecCalls, id)
	return f.deleteSpecErr
}

func (f *fakeService) GetMemory(_ context.Context, id string) (store.MemoryRead, bool, error) {
	return f.getMemoryRow, f.getMemoryFound, f.getMemoryErr
}

func (f *fakeService) InsertMemory(_ context.Context, in store.InsertMemoryInput) (store.MemoryRead, error) {
	f.insertedMemory = append(f.insertedMemory, in)
	row := f.insertMemoryRow
	if row.ID == "" {
		row = store.MemoryRead{
			ID:             in.ID,
			Scope:          in.Scope,
			UserID:         in.UserID,
			ProjectID:      in.ProjectID,
			MemoryType:     in.MemoryType,
			Title:          in.Title,
			Body:           in.Body,
			Why:            in.Why,
			Tags:           in.Tags,
			Source:         in.Source,
			AgentActor:     in.AgentActor,
			ConversationID: in.ConversationID,
			CreatedAt:      in.Now,
			UpdatedAt:      in.Now,
		}
	}
	return row, f.insertMemoryErr
}

func (f *fakeService) UpdateMemory(_ context.Context, in store.UpdateMemoryInput) (store.MemoryRead, bool, error) {
	f.updateMemoryIn = append(f.updateMemoryIn, in)
	return f.updateMemoryRow, f.updateMemoryFound, f.updateMemoryErr
}

func (f *fakeService) SoftDeleteMemory(_ context.Context, id string, _ time.Time) error {
	f.deleteMemoryCalls = append(f.deleteMemoryCalls, id)
	return f.deleteMemoryErr
}

type recordingAudit struct {
	mu     sync.Mutex
	events []audit.Event
	err    error
}

func (r *recordingAudit) Emit(ev audit.Event) error {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
	return r.err
}

func newTestService(t *testing.T, fake *fakeService, aud AuditIngester) (*Service, time.Time) {
	t.Helper()
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC)
	idSeq := 0
	svc := NewService(fake, aud, Options{
		Now:   func() time.Time { return now },
		NewID: func() string { idSeq++; return uuidish(idSeq) },
	})
	return svc, now
}

func uuidish(i int) string {
	return "id-" + string(rune('0'+i))
}

// ----- Actor validation -----------------------------------------------------

func TestActorValidate(t *testing.T) {
	cases := []struct {
		name string
		a    Actor
		ok   bool
	}{
		{"valid user", Actor{Type: audit.ActorTypeUser, UserID: "u"}, true},
		{"valid agent", Actor{Type: audit.ActorTypeAgent, AgentActor: "claude:p1"}, true},
		{"empty type", Actor{UserID: "u"}, false},
		{"user missing id", Actor{Type: audit.ActorTypeUser}, false},
		{"agent missing actor", Actor{Type: audit.ActorTypeAgent}, false},
		{"unknown type", Actor{Type: "alien", UserID: "u"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.a.Validate()
			if (err == nil) != c.ok {
				t.Errorf("Validate ok=%v, got err=%v", c.ok, err)
			}
		})
	}
}

func TestActorAuditSource(t *testing.T) {
	if got := (Actor{Type: audit.ActorTypeUser}).auditSource(); got != audit.SourceAdmin {
		t.Errorf("user actor source = %q, want %q", got, audit.SourceAdmin)
	}
	if got := (Actor{Type: audit.ActorTypeAgent}).auditSource(); got != audit.SourceRuntime {
		t.Errorf("agent actor source = %q, want %q", got, audit.SourceRuntime)
	}
}

// ----- Spec fragment CRUD ---------------------------------------------------

func TestCreateSpecFragmentHappyPathUser(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, now := newTestService(t, fake, aud)

	frag, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws-1",
		Title:       "Stack",
		Body:        "Go + Postgres",
		Tags:        []string{"backend"},
		Source:      SourceManual,
		Actor:       Actor{Type: audit.ActorTypeUser, UserID: "user-1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if frag.ID == "" || frag.Title != "Stack" {
		t.Errorf("unexpected fragment: %+v", frag)
	}

	if len(fake.insertedSpec) != 1 {
		t.Fatalf("expected 1 InsertSpecFragment call, got %d", len(fake.insertedSpec))
	}
	got := fake.insertedSpec[0]
	if got.WorkspaceID != "ws-1" || got.Title != "Stack" || got.Source != "manual" {
		t.Errorf("insert input mismatch: %+v", got)
	}
	if got.CreatedBy != "user-1" {
		t.Errorf("user-actor write should set CreatedBy, got %q", got.CreatedBy)
	}
	if got.AgentActor != "" {
		t.Errorf("user-actor write should leave AgentActor empty, got %q", got.AgentActor)
	}
	if !got.Now.Equal(now) {
		t.Errorf("Now not stamped from service clock, got %v want %v", got.Now, now)
	}

	if len(aud.events) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(aud.events))
	}
	ev := aud.events[0]
	if ev.EventType != "spec_fragment.created" || ev.Source != audit.SourceAdmin {
		t.Errorf("audit event wrong type/source: %+v", ev)
	}
	if ev.ActorType != audit.ActorTypeUser || ev.ActorID != "user-1" {
		t.Errorf("audit actor mismatch: %+v", ev)
	}
	if ev.TargetID != frag.ID || ev.WorkspaceID != "ws-1" {
		t.Errorf("audit target mismatch: %+v", ev)
	}
}

func TestCreateSpecFragmentAgentActor(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	_, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws-1",
		Title:       "Convention",
		Body:        "use ctx as first arg",
		Source:      SourceAgent,
		Actor:       Actor{Type: audit.ActorTypeAgent, AgentActor: "claude:p1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := fake.insertedSpec[0]
	if got.CreatedBy != "" {
		t.Errorf("agent-actor write should leave CreatedBy empty, got %q", got.CreatedBy)
	}
	if got.AgentActor != "claude:p1" {
		t.Errorf("AgentActor not propagated, got %q", got.AgentActor)
	}
	if aud.events[0].Source != audit.SourceRuntime {
		t.Errorf("agent-write audit source = %q, want %q", aud.events[0].Source, audit.SourceRuntime)
	}
}

func TestCreateSpecFragmentRejectsBadSource(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws", Title: "t", Body: "b",
		Source: Source("bogus"),
		Actor:  Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Errorf("expected unknown source error, got %v", err)
	}
	if len(fake.insertedSpec) != 0 {
		t.Error("store should not be called on validation failure")
	}
}

func TestCreateSpecFragmentRequiresActor(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws", Title: "t", Body: "b", Source: SourceManual,
	})
	if err == nil {
		t.Error("expected actor validation error")
	}
}

func TestCreateSpecFragmentWrapsStoreError(t *testing.T) {
	sentinel := errors.New("db-down")
	fake := &fakeService{insertSpecErr: sentinel}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws", Title: "t", Body: "b", Source: SourceManual,
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected wrapped sentinel, got %v", err)
	}
}

func TestUpdateSpecFragmentEmitsAudit(t *testing.T) {
	fake := &fakeService{
		updateSpecFound: true,
		updateSpecRow: store.SpecFragmentRead{
			ID: "f1", WorkspaceID: "ws-1", Title: "Stack v2", Body: "body",
			Source: "manual",
		},
	}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	frag, ok, err := svc.UpdateSpecFragment(context.Background(), UpdateSpecFragmentInput{
		ID: "f1", Title: "Stack v2", Body: "body",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil || !ok {
		t.Fatalf("update: err=%v ok=%v", err, ok)
	}
	if frag.Title != "Stack v2" {
		t.Errorf("returned fragment wrong: %+v", frag)
	}
	if len(aud.events) != 1 || aud.events[0].EventType != "spec_fragment.updated" {
		t.Errorf("expected one updated event, got %+v", aud.events)
	}
}

func TestUpdateSpecFragmentMissingReturnsFalse(t *testing.T) {
	fake := &fakeService{updateSpecFound: false}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	_, ok, err := svc.UpdateSpecFragment(context.Background(), UpdateSpecFragmentInput{
		ID:    "missing",
		Title: "t", Body: "b",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false for missing row")
	}
	if len(aud.events) != 0 {
		t.Errorf("no audit should fire on missing row, got %+v", aud.events)
	}
}

func TestDeleteSpecFragmentEmitsAudit(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	err := svc.DeleteSpecFragment(context.Background(), DeleteSpecFragmentInput{
		ID:    "f1",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteSpecCalls) != 1 || fake.deleteSpecCalls[0] != "f1" {
		t.Errorf("expected one delete call for f1, got %+v", fake.deleteSpecCalls)
	}
	if len(aud.events) != 1 || aud.events[0].EventType != "spec_fragment.deleted" {
		t.Errorf("expected deleted event, got %+v", aud.events)
	}
}

func TestListWorkspaceSpecFragmentsValidatesFilter(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.ListWorkspaceSpecFragments(context.Background(), ListWorkspaceSpecFragmentsInput{
		WorkspaceID:  "ws",
		SourceFilter: Source("bogus"),
	})
	if err == nil {
		t.Error("expected unknown source filter error")
	}
}

// ----- Memory CRUD ----------------------------------------------------------

func TestCreateMemoryUserScopeHappyPath(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	mem, err := svc.CreateMemory(context.Background(), CreateMemoryInput{
		Scope:      ScopeUser,
		UserID:     "u",
		MemoryType: MemoryTypeFeedback,
		Body:       "no defer in hot loop",
		Why:        "30% regression last year",
		Source:     SourceAgent,
		Actor:      Actor{Type: audit.ActorTypeAgent, AgentActor: "claude:p1"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mem.Scope != ScopeUser || mem.MemoryType != MemoryTypeFeedback {
		t.Errorf("returned memory wrong: %+v", mem)
	}
	got := fake.insertedMemory[0]
	if got.ProjectID != "" {
		t.Errorf("user-scope memory should have empty ProjectID, got %q", got.ProjectID)
	}
	if got.AgentActor != "claude:p1" {
		t.Errorf("AgentActor not propagated, got %q", got.AgentActor)
	}
	if aud.events[0].EventType != "memory.created" {
		t.Errorf("expected memory.created event, got %q", aud.events[0].EventType)
	}
	payload := aud.events[0].Payload
	if payload["why_present"] != true {
		t.Errorf("why_present should be true when Why set, got %+v", payload)
	}
}

func TestCreateMemoryProjectScopeRequiresProjectID(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.CreateMemory(context.Background(), CreateMemoryInput{
		Scope:      ScopeProject,
		UserID:     "u",
		MemoryType: MemoryTypeProject,
		Body:       "b",
		Source:     SourceManual,
		Actor:      Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err == nil || !strings.Contains(err.Error(), "project_id is required") {
		t.Errorf("expected project_id required error, got %v", err)
	}
}

func TestCreateMemoryUserScopeRejectsProjectID(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	_, err := svc.CreateMemory(context.Background(), CreateMemoryInput{
		Scope:      ScopeUser,
		UserID:     "u",
		ProjectID:  "p",
		MemoryType: MemoryTypeUser,
		Body:       "b",
		Source:     SourceManual,
		Actor:      Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err == nil || !strings.Contains(err.Error(), "must be empty") {
		t.Errorf("expected project_id must be empty error, got %v", err)
	}
}

func TestCreateMemoryRejectsBadEnums(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	cases := []struct {
		name  string
		input CreateMemoryInput
		want  string
	}{
		{"bad scope", CreateMemoryInput{
			Scope: Scope("bogus"), UserID: "u", MemoryType: MemoryTypeUser, Body: "b", Source: SourceManual,
			Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
		}, "unknown scope"},
		{"bad type", CreateMemoryInput{
			Scope: ScopeUser, UserID: "u", MemoryType: MemoryType("bogus"), Body: "b", Source: SourceManual,
			Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
		}, "unknown memory type"},
		{"bad source", CreateMemoryInput{
			Scope: ScopeUser, UserID: "u", MemoryType: MemoryTypeUser, Body: "b", Source: Source("bogus"),
			Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
		}, "unknown source"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.CreateMemory(context.Background(), c.input)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("expected %q error, got %v", c.want, err)
			}
		})
	}
}

func TestUpdateMemoryEmitsAudit(t *testing.T) {
	fake := &fakeService{
		updateMemoryFound: true,
		updateMemoryRow: store.MemoryRead{
			ID: "m1", Scope: "user", UserID: "u", MemoryType: "user",
			Body: "updated body", Source: "manual",
		},
	}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	mem, ok, err := svc.UpdateMemory(context.Background(), UpdateMemoryInput{
		ID: "m1", Title: "", Body: "updated body",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil || !ok {
		t.Fatalf("update: err=%v ok=%v", err, ok)
	}
	if mem.Body != "updated body" {
		t.Errorf("returned memory wrong: %+v", mem)
	}
	if aud.events[0].EventType != "memory.updated" {
		t.Errorf("expected memory.updated, got %q", aud.events[0].EventType)
	}
}

func TestDeleteMemoryEmitsAudit(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)
	if err := svc.DeleteMemory(context.Background(), DeleteMemoryInput{
		ID:    "m1",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fake.deleteMemoryCalls) != 1 || fake.deleteMemoryCalls[0] != "m1" {
		t.Errorf("expected single delete for m1, got %+v", fake.deleteMemoryCalls)
	}
	if len(aud.events) != 1 || aud.events[0].EventType != "memory.deleted" {
		t.Errorf("expected one delete event, got %+v", aud.events)
	}
}

// ----- Import ---------------------------------------------------------------

func TestConfirmImportCreatesAllFragments(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)

	got, err := svc.ConfirmImport(context.Background(), ConfirmImportInput{
		WorkspaceID: "ws-1",
		Text:        "## A\nbody A\n\n## B\nbody B\n",
		Actor:       Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 fragments, got %d", len(got))
	}
	if len(fake.insertedSpec) != 2 {
		t.Errorf("expected 2 store inserts, got %d", len(fake.insertedSpec))
	}
	if fake.insertedSpec[0].Source != "import" {
		t.Errorf("ConfirmImport should stamp SourceImport, got %q", fake.insertedSpec[0].Source)
	}
	if len(aud.events) != 2 {
		t.Errorf("expected 2 created events, got %d", len(aud.events))
	}
}

func TestConfirmImportReturnsPartialOnFailure(t *testing.T) {
	// 2nd insert fails — the first fragment is already persisted, so
	// ConfirmImport returns the partial slice + an error labeled with
	// the failing fragment's index/title.
	calls := 0
	fake := &fakeService{}
	aud := &recordingAudit{}
	svc, _ := newTestService(t, fake, aud)
	origInsert := fake.insertSpecErr
	wrapped := &countingInsertFake{fakeService: fake, failAt: 2, calls: &calls}
	svc.store = wrapped
	_ = origInsert

	got, err := svc.ConfirmImport(context.Background(), ConfirmImportInput{
		WorkspaceID: "ws-1",
		Text:        "## A\nbody A\n\n## B\nbody B\n",
		Actor:       Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err == nil || !strings.Contains(err.Error(), "fragment 1") {
		t.Errorf("expected fragment-1 error, got %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected partial slice of 1, got %d", len(got))
	}
}

// countingInsertFake wraps fakeService to fail on a specific call
// index — supports the partial-failure import test.
type countingInsertFake struct {
	*fakeService
	failAt int
	calls  *int
}

func (c *countingInsertFake) InsertSpecFragment(ctx context.Context, in store.InsertSpecFragmentInput) (store.SpecFragmentRead, error) {
	*c.calls++
	if *c.calls == c.failAt {
		return store.SpecFragmentRead{}, errors.New("synthetic insert failure")
	}
	return c.fakeService.InsertSpecFragment(ctx, in)
}

func TestConfirmImportEmptyTextReturnsNil(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	got, err := svc.ConfirmImport(context.Background(), ConfirmImportInput{
		WorkspaceID: "ws", Text: "   \n",
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result for empty text, got %+v", got)
	}
	if len(fake.insertedSpec) != 0 {
		t.Errorf("store should not be touched for empty text")
	}
}

func TestPreviewImportPassthrough(t *testing.T) {
	svc, _ := newTestService(t, &fakeService{}, nil)
	got := svc.PreviewImport("## A\nbody\n")
	if len(got) != 1 || got[0].Title != "A" {
		t.Errorf("PreviewImport passthrough broken: %+v", got)
	}
}

// ----- Injection passthrough + ingester error handling ----------------------

func TestBuildSnapshotDelegatesToInjector(t *testing.T) {
	fake := &fakeService{}
	fake.specRows = []store.SpecFragmentRead{{ID: "f1", Title: "T", Body: "B", Source: "manual"}}
	svc, _ := newTestService(t, fake, nil)
	got, err := svc.BuildSnapshot(context.Background(), SnapshotInput{
		WorkspaceID: "ws", WorkspaceName: "acme", UserID: "u",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.SpecBlock, "### T") {
		t.Errorf("injector passthrough lost data: %q", got.SpecBlock)
	}
}

func TestServiceNilAuditNoPanic(t *testing.T) {
	fake := &fakeService{}
	svc, _ := newTestService(t, fake, nil)
	if _, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws", Title: "t", Body: "b", Source: SourceManual,
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	}); err != nil {
		t.Fatalf("nil audit should not fail call, got %v", err)
	}
}

func TestServiceAuditErrorDoesNotBubble(t *testing.T) {
	fake := &fakeService{}
	aud := &recordingAudit{err: errors.New("audit down")}
	svc, _ := newTestService(t, fake, aud)
	if _, err := svc.CreateSpecFragment(context.Background(), CreateSpecFragmentInput{
		WorkspaceID: "ws", Title: "t", Body: "b", Source: SourceManual,
		Actor: Actor{Type: audit.ActorTypeUser, UserID: "u"},
	}); err != nil {
		t.Fatalf("audit failure must not fail the call, got %v", err)
	}
	if len(aud.events) != 1 {
		t.Errorf("audit Emit was still called, expected 1, got %d", len(aud.events))
	}
}

func TestNewServicePanicsOnNilStore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil store")
		}
	}()
	NewService(nil, nil, Options{})
}
