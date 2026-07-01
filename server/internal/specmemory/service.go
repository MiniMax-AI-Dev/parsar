package specmemory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"strings"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"

	guuid "github.com/google/uuid"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/audit"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// AuditIngester is the narrow audit-emit surface specmemory uses.
// Audit failures are observability-only and must not fail the
// business operation (matches audit.Ingester.Emit contract).
type AuditIngester interface {
	Emit(ev audit.Event) error
}

// Store is the read+write surface the Service depends on.
type Store interface {
	InjectionReader

	GetSpecFragment(ctx context.Context, id string) (store.SpecFragmentRead, bool, error)
	InsertSpecFragment(ctx context.Context, input store.InsertSpecFragmentInput) (store.SpecFragmentRead, error)
	UpdateSpecFragment(ctx context.Context, input store.UpdateSpecFragmentInput) (store.SpecFragmentRead, bool, error)
	SoftDeleteSpecFragment(ctx context.Context, id string, now time.Time) error

	GetMemory(ctx context.Context, id string) (store.MemoryRead, bool, error)
	InsertMemory(ctx context.Context, input store.InsertMemoryInput) (store.MemoryRead, error)
	UpdateMemory(ctx context.Context, input store.UpdateMemoryInput) (store.MemoryRead, bool, error)
	SoftDeleteMemory(ctx context.Context, id string, now time.Time) error
}

// Actor identifies whoever is invoking the service.
//   - UI user → ActorTypeUser, UserID populated
//   - agent CLI → ActorTypeAgent, AgentActor populated
//     (format "connector:agent_id", set by the parsar CLI from
//     PARSAR_CONNECTOR + PARSAR_AGENT_ID env vars)
//
// Source maps to audit.Source*: user actions get SourceAdmin, agent
// actions get SourceRuntime.
type Actor struct {
	Type       string // audit.ActorTypeUser | ActorTypeAgent
	UserID     string // required when Type==ActorTypeUser
	AgentActor string // required when Type==ActorTypeAgent
}

// Validate enforces the user/agent dichotomy.
func (a Actor) Validate() error {
	switch a.Type {
	case audit.ActorTypeUser:
		if strings.TrimSpace(a.UserID) == "" {
			return errors.New("specmemory: actor: user_id required for user actor")
		}
		return nil
	case audit.ActorTypeAgent:
		if strings.TrimSpace(a.AgentActor) == "" {
			return errors.New("specmemory: actor: agent_actor required for agent actor")
		}
		return nil
	case "":
		return errors.New("specmemory: actor: type is required")
	default:
		return fmt.Errorf("specmemory: actor: unknown type %q", a.Type)
	}
}

// auditID returns what goes into audit.Event.ActorID for this actor.
func (a Actor) auditID() string {
	if a.Type == audit.ActorTypeUser {
		return a.UserID
	}
	return a.AgentActor
}

// auditSource maps the actor to an audit.Source* category.
func (a Actor) auditSource() string {
	if a.Type == audit.ActorTypeAgent {
		return audit.SourceRuntime
	}
	return audit.SourceAdmin
}

// Options configures a Service. Zero values default to production;
// tests override Now and NewID for determinism.
type Options struct {
	Now    func() time.Time
	NewID  func() string
	Logger *slog.Logger
}

// Service is the top-level facade for HTTP handlers and the
// agent-runtime CLI bridge. Stateless beyond its dependencies.
type Service struct {
	store    Store
	audit    AuditIngester
	injector *Injector
	now      func() time.Time
	newID    func() string
	logger   *slog.Logger
}

// NewService binds a Service to its dependencies. The audit ingester
// may be nil — emit is then a silent no-op.
func NewService(s Store, a AuditIngester, opts Options) *Service {
	if s == nil {
		panic("specmemory.NewService: store is nil")
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	newID := opts.NewID
	if newID == nil {
		newID = guuid.NewString
	}
	logger := opts.Logger
	if logger == nil {
		logger = obslog.Bg()
	}
	return &Service{
		store:    s,
		audit:    a,
		injector: NewInjector(s),
		now:      now,
		newID:    newID,
		logger:   logger,
	}
}

// emitAudit hands an event to the ingester. Per audit.Ingester
// contract, Emit failures must not fail the caller's operation.
func (s *Service) emitAudit(ev audit.Event) {
	if s.audit == nil {
		return
	}
	if err := s.audit.Emit(ev); err != nil {
		s.logger.Debug("specmemory: audit emit dropped",
			"event_type", ev.EventType,
			"target_id", ev.TargetID,
			"error", err,
		)
	}
}

// ----- Spec fragment CRUD ---------------------------------------------------

// CreateSpecFragmentInput is the UI / agent-runtime write payload.
// Source must be a known specmemory.Source.
type CreateSpecFragmentInput struct {
	WorkspaceID string
	Title       string
	Body        string
	Tags        []string
	Source      Source
	Actor       Actor
}

// CreateSpecFragment validates input, generates the row ID, persists
// via the store, and emits a "spec_fragment.created" audit event.
func (s *Service) CreateSpecFragment(ctx context.Context, input CreateSpecFragmentInput) (Fragment, error) {
	if err := input.Actor.Validate(); err != nil {
		return Fragment{}, err
	}
	if input.WorkspaceID == "" {
		return Fragment{}, errors.New("specmemory: workspace_id is required")
	}
	if !input.Source.Valid() {
		return Fragment{}, fmt.Errorf("specmemory: unknown source %q", input.Source)
	}
	id := s.newID()
	now := s.now()

	row, err := s.store.InsertSpecFragment(ctx, store.InsertSpecFragmentInput{
		ID:          id,
		WorkspaceID: input.WorkspaceID,
		Title:       input.Title,
		Body:        input.Body,
		Tags:        input.Tags,
		Source:      input.Source.String(),
		CreatedBy:   userIDForActor(input.Actor),
		AgentActor:  agentActorForActor(input.Actor),
		Now:         now,
	})
	if err != nil {
		return Fragment{}, fmt.Errorf("specmemory: create spec fragment: %w", err)
	}
	frag, ok := FragmentFromStoreRow(row)
	if !ok {
		return Fragment{}, fmt.Errorf("specmemory: created fragment failed enum re-parse: %+v", row)
	}
	s.emitAudit(audit.Event{
		OccurredAt:  now,
		Source:      input.Actor.auditSource(),
		EventType:   "spec_fragment.created",
		ActorType:   input.Actor.Type,
		ActorID:     input.Actor.auditID(),
		TargetType:  "spec_fragment",
		TargetID:    frag.ID,
		WorkspaceID: frag.WorkspaceID,
		Payload: map[string]any{
			"source":   frag.Source.String(),
			"title":    frag.Title,
			"body_len": len(frag.Body),
			"tags":     frag.Tags,
		},
	})
	return frag, nil
}

// GetSpecFragment is a read-through; reads are not audited.
func (s *Service) GetSpecFragment(ctx context.Context, id string) (Fragment, bool, error) {
	row, ok, err := s.store.GetSpecFragment(ctx, id)
	if err != nil {
		return Fragment{}, false, fmt.Errorf("specmemory: get spec fragment: %w", err)
	}
	if !ok {
		return Fragment{}, false, nil
	}
	frag, parsed := FragmentFromStoreRow(row)
	if !parsed {
		return Fragment{}, false, fmt.Errorf("specmemory: spec fragment %s has unknown enum values", id)
	}
	return frag, true, nil
}

// ListWorkspaceSpecFragmentsInput drives the UI Spec tab.
type ListWorkspaceSpecFragmentsInput struct {
	WorkspaceID  string
	SourceFilter Source   // zero ("") = all sources
	TagFilter    []string // empty = all tags
	Limit        int32
}

// ListWorkspaceSpecFragments returns active fragments for a workspace
// ordered by updated_at desc.
func (s *Service) ListWorkspaceSpecFragments(ctx context.Context, input ListWorkspaceSpecFragmentsInput) ([]Fragment, error) {
	if input.WorkspaceID == "" {
		return nil, errors.New("specmemory: workspace_id is required")
	}
	if input.SourceFilter != "" && !input.SourceFilter.Valid() {
		return nil, fmt.Errorf("specmemory: unknown source filter %q", input.SourceFilter)
	}
	rows, err := s.store.ListWorkspaceSpecFragments(ctx, store.ListWorkspaceSpecFragmentsInput{
		WorkspaceID:  input.WorkspaceID,
		SourceFilter: string(input.SourceFilter),
		TagFilter:    input.TagFilter,
		Limit:        input.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("specmemory: list workspace spec fragments: %w", err)
	}
	return convertFragments(rows), nil
}

// UpdateSpecFragmentInput is the full-replace payload. Source /
// CreatedBy / AgentActor are locked at insert.
type UpdateSpecFragmentInput struct {
	ID    string
	Title string
	Body  string
	Tags  []string
	Actor Actor
}

// UpdateSpecFragment applies the edit and emits a "spec_fragment.updated"
// audit event. Returns (zero, false, nil) when the row is missing or
// already soft-deleted.
func (s *Service) UpdateSpecFragment(ctx context.Context, input UpdateSpecFragmentInput) (Fragment, bool, error) {
	if err := input.Actor.Validate(); err != nil {
		return Fragment{}, false, err
	}
	if input.ID == "" {
		return Fragment{}, false, errors.New("specmemory: id is required")
	}
	now := s.now()
	row, ok, err := s.store.UpdateSpecFragment(ctx, store.UpdateSpecFragmentInput{
		ID:    input.ID,
		Title: input.Title,
		Body:  input.Body,
		Tags:  input.Tags,
		Now:   now,
	})
	if err != nil {
		return Fragment{}, false, fmt.Errorf("specmemory: update spec fragment: %w", err)
	}
	if !ok {
		return Fragment{}, false, nil
	}
	frag, parsed := FragmentFromStoreRow(row)
	if !parsed {
		return Fragment{}, false, fmt.Errorf("specmemory: updated fragment %s has unknown enum values", input.ID)
	}
	s.emitAudit(audit.Event{
		OccurredAt:  now,
		Source:      input.Actor.auditSource(),
		EventType:   "spec_fragment.updated",
		ActorType:   input.Actor.Type,
		ActorID:     input.Actor.auditID(),
		TargetType:  "spec_fragment",
		TargetID:    frag.ID,
		WorkspaceID: frag.WorkspaceID,
		Payload: map[string]any{
			"title":    frag.Title,
			"body_len": len(frag.Body),
			"tags":     frag.Tags,
		},
	})
	return frag, true, nil
}

// DeleteSpecFragmentInput identifies the row to tombstone.
type DeleteSpecFragmentInput struct {
	ID    string
	Actor Actor
}

// DeleteSpecFragment soft-deletes the row and emits an audit event.
// Idempotent — repeated deletes succeed with one audit event per call.
func (s *Service) DeleteSpecFragment(ctx context.Context, input DeleteSpecFragmentInput) error {
	if err := input.Actor.Validate(); err != nil {
		return err
	}
	if input.ID == "" {
		return errors.New("specmemory: id is required")
	}
	now := s.now()
	if err := s.store.SoftDeleteSpecFragment(ctx, input.ID, now); err != nil {
		return fmt.Errorf("specmemory: delete spec fragment: %w", err)
	}
	s.emitAudit(audit.Event{
		OccurredAt: now,
		Source:     input.Actor.auditSource(),
		EventType:  "spec_fragment.deleted",
		ActorType:  input.Actor.Type,
		ActorID:    input.Actor.auditID(),
		TargetType: "spec_fragment",
		TargetID:   input.ID,
	})
	return nil
}

// ----- Memory CRUD ----------------------------------------------------------

// CreateMemoryInput is the UI / agent-runtime memory write payload.
// WorkspaceID is required when Scope==ScopeWorkspace (enforced both here
// and by memories_scope_workspace_id_match_check at the DB level).
type CreateMemoryInput struct {
	Scope          Scope
	UserID         string
	WorkspaceID    string // required when Scope==ScopeWorkspace
	MemoryType     MemoryType
	Title          string
	Body           string
	Why            string
	Tags           []string
	Source         Source
	ConversationID string // optional, set when the agent wrote mid-turn
	Actor          Actor
}

// CreateMemory validates input, persists, and emits "memory.created".
func (s *Service) CreateMemory(ctx context.Context, input CreateMemoryInput) (Memory, error) {
	if err := input.Actor.Validate(); err != nil {
		return Memory{}, err
	}
	if !input.Scope.Valid() {
		return Memory{}, fmt.Errorf("specmemory: unknown scope %q", input.Scope)
	}
	if !input.MemoryType.Valid() {
		return Memory{}, fmt.Errorf("specmemory: unknown memory type %q", input.MemoryType)
	}
	if !input.Source.Valid() {
		return Memory{}, fmt.Errorf("specmemory: unknown source %q", input.Source)
	}
	if input.UserID == "" {
		return Memory{}, errors.New("specmemory: user_id is required")
	}
	if input.Scope == ScopeWorkspace && input.WorkspaceID == "" {
		return Memory{}, errors.New("specmemory: workspace_id is required for workspace-scope memories")
	}
	if input.Scope == ScopeUser && input.WorkspaceID != "" {
		return Memory{}, errors.New("specmemory: workspace_id must be empty for user-scope memories")
	}
	id := s.newID()
	now := s.now()
	row, err := s.store.InsertMemory(ctx, store.InsertMemoryInput{
		ID:             id,
		Scope:          input.Scope.String(),
		UserID:         input.UserID,
		WorkspaceID:    input.WorkspaceID,
		MemoryType:     input.MemoryType.String(),
		Title:          input.Title,
		Body:           input.Body,
		Why:            input.Why,
		Tags:           input.Tags,
		Source:         input.Source.String(),
		AgentActor:     agentActorForActor(input.Actor),
		ConversationID: input.ConversationID,
		Now:            now,
	})
	if err != nil {
		return Memory{}, fmt.Errorf("specmemory: create memory: %w", err)
	}
	mem, parsed := MemoryFromStoreRow(row)
	if !parsed {
		return Memory{}, fmt.Errorf("specmemory: created memory failed enum re-parse: %+v", row)
	}
	s.emitAudit(audit.Event{
		OccurredAt: now,
		Source:     input.Actor.auditSource(),
		EventType:  "memory.created",
		ActorType:  input.Actor.Type,
		ActorID:    input.Actor.auditID(),
		TargetType: "memory",
		TargetID:   mem.ID,
		WorkspaceID: mem.WorkspaceID,
		Payload: map[string]any{
			"scope":           mem.Scope.String(),
			"memory_type":     mem.MemoryType.String(),
			"source":          mem.Source.String(),
			"title":           mem.Title,
			"body_len":        len(mem.Body),
			"why_present":     strings.TrimSpace(mem.Why) != "",
			"tags":            mem.Tags,
			"conversation_id": mem.ConversationID,
		},
	})
	return mem, nil
}

// GetMemory is a read-through; no audit.
func (s *Service) GetMemory(ctx context.Context, id string) (Memory, bool, error) {
	row, ok, err := s.store.GetMemory(ctx, id)
	if err != nil {
		return Memory{}, false, fmt.Errorf("specmemory: get memory: %w", err)
	}
	if !ok {
		return Memory{}, false, nil
	}
	mem, parsed := MemoryFromStoreRow(row)
	if !parsed {
		return Memory{}, false, fmt.Errorf("specmemory: memory %s has unknown enum values", id)
	}
	return mem, true, nil
}

// ListUserMemoriesInput drives the user-scope tab + agent-runtime
// snapshot.
type ListUserMemoriesInput struct {
	UserID           string
	MemoryTypeFilter MemoryType
	TagFilter        []string
	Limit            int32
}

func (s *Service) ListUserMemories(ctx context.Context, input ListUserMemoriesInput) ([]Memory, error) {
	if input.UserID == "" {
		return nil, errors.New("specmemory: user_id is required")
	}
	if input.MemoryTypeFilter != "" && !input.MemoryTypeFilter.Valid() {
		return nil, fmt.Errorf("specmemory: unknown memory type filter %q", input.MemoryTypeFilter)
	}
	rows, err := s.store.ListUserMemories(ctx, store.ListUserMemoriesInput{
		UserID:           input.UserID,
		MemoryTypeFilter: string(input.MemoryTypeFilter),
		TagFilter:        input.TagFilter,
		Limit:            input.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("specmemory: list user memories: %w", err)
	}
	return convertMemories(rows), nil
}

// ListWorkspaceMemoriesInput drives the workspace-scope tab.
type ListWorkspaceMemoriesInput struct {
	WorkspaceID      string
	MemoryTypeFilter MemoryType
	TagFilter        []string
	Limit            int32
}

func (s *Service) ListWorkspaceMemories(ctx context.Context, input ListWorkspaceMemoriesInput) ([]Memory, error) {
	if input.WorkspaceID == "" {
		return nil, errors.New("specmemory: workspace_id is required")
	}
	if input.MemoryTypeFilter != "" && !input.MemoryTypeFilter.Valid() {
		return nil, fmt.Errorf("specmemory: unknown memory type filter %q", input.MemoryTypeFilter)
	}
	rows, err := s.store.ListWorkspaceMemories(ctx, store.ListWorkspaceMemoriesInput{
		WorkspaceID:      input.WorkspaceID,
		MemoryTypeFilter: string(input.MemoryTypeFilter),
		TagFilter:        input.TagFilter,
		Limit:            input.Limit,
	})
	if err != nil {
		return nil, fmt.Errorf("specmemory: list workspace memories: %w", err)
	}
	return convertMemories(rows), nil
}

// UpdateMemoryInput is the full-replace edit payload. Scope / type /
// source / actor identity are locked at insert.
type UpdateMemoryInput struct {
	ID    string
	Title string
	Body  string
	Why   string
	Tags  []string
	Actor Actor
}

func (s *Service) UpdateMemory(ctx context.Context, input UpdateMemoryInput) (Memory, bool, error) {
	if err := input.Actor.Validate(); err != nil {
		return Memory{}, false, err
	}
	if input.ID == "" {
		return Memory{}, false, errors.New("specmemory: id is required")
	}
	now := s.now()
	row, ok, err := s.store.UpdateMemory(ctx, store.UpdateMemoryInput{
		ID:    input.ID,
		Title: input.Title,
		Body:  input.Body,
		Why:   input.Why,
		Tags:  input.Tags,
		Now:   now,
	})
	if err != nil {
		return Memory{}, false, fmt.Errorf("specmemory: update memory: %w", err)
	}
	if !ok {
		return Memory{}, false, nil
	}
	mem, parsed := MemoryFromStoreRow(row)
	if !parsed {
		return Memory{}, false, fmt.Errorf("specmemory: updated memory %s has unknown enum values", input.ID)
	}
	s.emitAudit(audit.Event{
		OccurredAt: now,
		Source:     input.Actor.auditSource(),
		EventType:  "memory.updated",
		ActorType:  input.Actor.Type,
		ActorID:    input.Actor.auditID(),
		TargetType: "memory",
		TargetID:   mem.ID,
		WorkspaceID: mem.WorkspaceID,
		Payload: map[string]any{
			"title":       mem.Title,
			"body_len":    len(mem.Body),
			"why_present": strings.TrimSpace(mem.Why) != "",
			"tags":        mem.Tags,
		},
	})
	return mem, true, nil
}

type DeleteMemoryInput struct {
	ID    string
	Actor Actor
}

func (s *Service) DeleteMemory(ctx context.Context, input DeleteMemoryInput) error {
	if err := input.Actor.Validate(); err != nil {
		return err
	}
	if input.ID == "" {
		return errors.New("specmemory: id is required")
	}
	now := s.now()
	if err := s.store.SoftDeleteMemory(ctx, input.ID, now); err != nil {
		return fmt.Errorf("specmemory: delete memory: %w", err)
	}
	s.emitAudit(audit.Event{
		OccurredAt: now,
		Source:     input.Actor.auditSource(),
		EventType:  "memory.deleted",
		ActorType:  input.Actor.Type,
		ActorID:    input.Actor.auditID(),
		TargetType: "memory",
		TargetID:   input.ID,
	})
	return nil
}

// ----- Import ---------------------------------------------------------------

// PreviewImport is a pure passthrough to the markdown importer.
func (s *Service) PreviewImport(text string) []ImportedFragment {
	return ImportFragmentsFromMarkdown(text)
}

// ConfirmImportInput carries the import body and the workspace +
// actor that will own the resulting fragments.
type ConfirmImportInput struct {
	WorkspaceID string
	Text        string
	Actor       Actor
}

// ConfirmImport slices the text, persists each fragment with
// SourceImport, and returns the created Fragment slice. Not
// transactional — a mid-loop store failure leaves previously-written
// fragments in place; callers should re-fetch on error.
func (s *Service) ConfirmImport(ctx context.Context, input ConfirmImportInput) ([]Fragment, error) {
	if err := input.Actor.Validate(); err != nil {
		return nil, err
	}
	if input.WorkspaceID == "" {
		return nil, errors.New("specmemory: workspace_id is required")
	}
	pieces := ImportFragmentsFromMarkdown(input.Text)
	if len(pieces) == 0 {
		return nil, nil
	}
	out := make([]Fragment, 0, len(pieces))
	for i, p := range pieces {
		frag, err := s.CreateSpecFragment(ctx, CreateSpecFragmentInput{
			WorkspaceID: input.WorkspaceID,
			Title:       p.Title,
			Body:        p.Body,
			Source:      SourceImport,
			Actor:       input.Actor,
		})
		if err != nil {
			return out, fmt.Errorf("specmemory: confirm import: fragment %d (%q): %w", i, p.Title, err)
		}
		out = append(out, frag)
	}
	return out, nil
}

// ----- Injection passthrough ------------------------------------------------

// BuildSnapshot delegates to the embedded Injector.
func (s *Service) BuildSnapshot(ctx context.Context, input SnapshotInput) (Injection, error) {
	return s.injector.BuildSnapshot(ctx, input)
}

// BuildIncremental delegates to the embedded Injector.
func (s *Service) BuildIncremental(ctx context.Context, input IncrementalInput) (Injection, error) {
	return s.injector.BuildIncremental(ctx, input)
}

// RenderSessionPrompt returns the concatenated SessionStart string
// ready to append onto an agent's system_prompt, or "" when there is
// nothing to inject. WorkspaceName comes back empty here; the runtime
// snapshot HTTP endpoint is the one that resolves the human-readable
// name.
func (s *Service) RenderSessionPrompt(ctx context.Context, workspaceID, userID string) (string, error) {
	inj, err := s.BuildSnapshot(ctx, SnapshotInput{
		WorkspaceID: workspaceID,
		UserID:      userID,
	})
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, 3)
	if inj.SpecBlock != "" {
		parts = append(parts, inj.SpecBlock)
	}
	if inj.MemoryBlock != "" {
		parts = append(parts, inj.MemoryBlock)
	}
	if inj.MemoryWriteGuide != "" {
		parts = append(parts, inj.MemoryWriteGuide)
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n\n"), nil
}

// ----- helpers --------------------------------------------------------------

// userIDForActor returns the UUID to stamp into the row's created_by
// column. Agent writes have NULL created_by (store maps "" to NULL).
func userIDForActor(a Actor) string {
	if a.Type == audit.ActorTypeUser {
		return a.UserID
	}
	return ""
}

// agentActorForActor mirrors userIDForActor for the agent_actor column.
func agentActorForActor(a Actor) string {
	if a.Type == audit.ActorTypeAgent {
		return a.AgentActor
	}
	return ""
}

// Compile-time guards: *audit.Ingester satisfies AuditIngester, and
// *store.Store satisfies Store.
var (
	_ AuditIngester = (*audit.Ingester)(nil)
	_ Store         = (*store.Store)(nil)
)
