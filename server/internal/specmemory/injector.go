package specmemory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Injection is the bundle of pre-rendered prompt fragments the
// connector layer injects into the agent's system prompt.
//
// SessionStart consumers populate SpecBlock + MemoryBlock +
// MemoryWriteGuide. Per-turn consumers populate IncrementalMemory only.
type Injection struct {
	SpecBlock         string
	MemoryBlock       string
	MemoryWriteGuide  string
	IncrementalMemory string
}

// SnapshotInput is the per-session injection request. WorkspaceName
// is rendered into <spec workspace="..."> so the agent can name it.
//
// ProjectID empty → project-scope memory bucket is skipped.
// Limits default to the store's defaultReadLimit when <= 0.
type SnapshotInput struct {
	WorkspaceID        string
	WorkspaceName      string
	UserID             string
	ProjectID          string
	SpecLimit          int32
	UserMemoryLimit    int32
	ProjectMemoryLimit int32
}

// IncrementalInput is the per-turn delta request. Since is the cursor
// from the end of the last turn; rows updated strictly after this
// timestamp are surfaced. ProjectID empty → project bucket skipped.
type IncrementalInput struct {
	UserID    string
	ProjectID string
	Since     time.Time
	Limit     int32
}

// SnapshotReader is the read surface BuildSnapshot needs. *store.Store
// satisfies it; tests pass a fake.
type SnapshotReader interface {
	ListWorkspaceSpecFragments(ctx context.Context, input store.ListWorkspaceSpecFragmentsInput) ([]store.SpecFragmentRead, error)
	ListUserMemories(ctx context.Context, input store.ListUserMemoriesInput) ([]store.MemoryRead, error)
	ListProjectMemories(ctx context.Context, input store.ListProjectMemoriesInput) ([]store.MemoryRead, error)
}

// IncrementalReader is the read surface BuildIncremental needs.
type IncrementalReader interface {
	ListUserMemoriesSince(ctx context.Context, input store.ListUserMemoriesSinceInput) ([]store.MemoryRead, error)
	ListProjectMemoriesSince(ctx context.Context, input store.ListProjectMemoriesSinceInput) ([]store.MemoryRead, error)
}

// InjectionReader composes both halves so a single Injector can serve
// SessionStart and per-turn requests against the same backing store.
type InjectionReader interface {
	SnapshotReader
	IncrementalReader
}

// Injector turns store reads into rendered Injection strings.
// Stateless beyond its reader dependency — safe to share across
// concurrent requests.
type Injector struct {
	reader InjectionReader
}

func NewInjector(reader InjectionReader) *Injector {
	return &Injector{reader: reader}
}

// BuildSnapshot assembles the SessionStart injection bundle. Rows with
// unknown enum values are silently dropped. User-scope and project-scope
// memories are merged before rendering; the prompt groups them by
// memory type, not scope.
func (i *Injector) BuildSnapshot(ctx context.Context, input SnapshotInput) (Injection, error) {
	if input.WorkspaceID == "" {
		return Injection{}, errors.New("specmemory: workspace_id is required for snapshot")
	}
	if input.UserID == "" {
		return Injection{}, errors.New("specmemory: user_id is required for snapshot")
	}

	fragmentRows, err := i.reader.ListWorkspaceSpecFragments(ctx, store.ListWorkspaceSpecFragmentsInput{
		WorkspaceID: input.WorkspaceID,
		Limit:       input.SpecLimit,
	})
	if err != nil {
		return Injection{}, fmt.Errorf("specmemory: snapshot spec list: %w", err)
	}

	userMemoryRows, err := i.reader.ListUserMemories(ctx, store.ListUserMemoriesInput{
		UserID: input.UserID,
		Limit:  input.UserMemoryLimit,
	})
	if err != nil {
		return Injection{}, fmt.Errorf("specmemory: snapshot user memory list: %w", err)
	}

	var projectMemoryRows []store.MemoryRead
	if input.ProjectID != "" {
		projectMemoryRows, err = i.reader.ListProjectMemories(ctx, store.ListProjectMemoriesInput{
			ProjectID: input.ProjectID,
			Limit:     input.ProjectMemoryLimit,
		})
		if err != nil {
			return Injection{}, fmt.Errorf("specmemory: snapshot project memory list: %w", err)
		}
	}

	fragments := convertFragments(fragmentRows)
	memories := convertMemories(mergeMemoryRows(userMemoryRows, projectMemoryRows))

	return Injection{
		SpecBlock:        RenderSpecBlock(input.WorkspaceName, fragments),
		MemoryBlock:      RenderMemoryBlock(memories),
		MemoryWriteGuide: MemoryWriteGuide(),
	}, nil
}

// BuildIncremental assembles the per-turn delta. Only IncrementalMemory
// is populated — the SessionStart-only fields stay empty so the hook
// can detect "delta-only" and append rather than re-stamp the system
// prompt. An empty bundle means "nothing to inject this turn".
func (i *Injector) BuildIncremental(ctx context.Context, input IncrementalInput) (Injection, error) {
	if input.UserID == "" {
		return Injection{}, errors.New("specmemory: user_id is required for incremental")
	}
	if input.Since.IsZero() {
		return Injection{}, errors.New("specmemory: since cursor is required for incremental")
	}

	userMemoryRows, err := i.reader.ListUserMemoriesSince(ctx, store.ListUserMemoriesSinceInput{
		UserID: input.UserID,
		Since:  input.Since,
		Limit:  input.Limit,
	})
	if err != nil {
		return Injection{}, fmt.Errorf("specmemory: incremental user memory list: %w", err)
	}

	var projectMemoryRows []store.MemoryRead
	if input.ProjectID != "" {
		projectMemoryRows, err = i.reader.ListProjectMemoriesSince(ctx, store.ListProjectMemoriesSinceInput{
			ProjectID: input.ProjectID,
			Since:     input.Since,
			Limit:     input.Limit,
		})
		if err != nil {
			return Injection{}, fmt.Errorf("specmemory: incremental project memory list: %w", err)
		}
	}

	memories := convertMemories(mergeMemoryRows(userMemoryRows, projectMemoryRows))

	return Injection{
		IncrementalMemory: RenderIncrementalMemory(memories),
	}, nil
}

// mergeMemoryRows concatenates user + project rows, user first.
func mergeMemoryRows(user, project []store.MemoryRead) []store.MemoryRead {
	if len(user) == 0 {
		return project
	}
	if len(project) == 0 {
		return user
	}
	out := make([]store.MemoryRead, 0, len(user)+len(project))
	out = append(out, user...)
	out = append(out, project...)
	return out
}

// convertFragments turns store rows into typed Fragments. Rows with
// unknown Source are dropped — the DB has no CHECK constraint so a
// bad value shouldn't leak into the prompt.
func convertFragments(rows []store.SpecFragmentRead) []Fragment {
	if len(rows) == 0 {
		return nil
	}
	out := make([]Fragment, 0, len(rows))
	for _, r := range rows {
		f, ok := FragmentFromStoreRow(r)
		if !ok {
			continue
		}
		out = append(out, f)
	}
	return out
}

// convertMemories turns store rows into typed Memory values.
// Defensive dedupe by ID — user-scope and project-scope queries are
// disjoint by design today, but a future cross-scope sharing schema
// shouldn't double-render.
func convertMemories(rows []store.MemoryRead) []Memory {
	if len(rows) == 0 {
		return nil
	}
	out := make([]Memory, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, r := range rows {
		m, ok := MemoryFromStoreRow(r)
		if !ok {
			continue
		}
		if _, dup := seen[m.ID]; dup {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	return out
}

// FragmentFromStoreRow converts a single store row into the typed
// business view. Returns ok=false when Source is unknown.
func FragmentFromStoreRow(r store.SpecFragmentRead) (Fragment, bool) {
	src, err := SourceFromString(r.Source)
	if err != nil {
		return Fragment{}, false
	}
	return Fragment{
		ID:          r.ID,
		WorkspaceID: r.WorkspaceID,
		Title:       r.Title,
		Body:        r.Body,
		Tags:        r.Tags,
		Source:      src,
		CreatedBy:   r.CreatedBy,
		AgentActor:  r.AgentActor,
		CreatedAt:   r.CreatedAt,
		UpdatedAt:   r.UpdatedAt,
	}, true
}

// MemoryFromStoreRow converts a single memory row. Returns ok=false
// when any of scope / memory_type / source is unknown.
func MemoryFromStoreRow(r store.MemoryRead) (Memory, bool) {
	scope, err := ScopeFromString(r.Scope)
	if err != nil {
		return Memory{}, false
	}
	mt, err := MemoryTypeFromString(r.MemoryType)
	if err != nil {
		return Memory{}, false
	}
	src, err := SourceFromString(r.Source)
	if err != nil {
		return Memory{}, false
	}
	return Memory{
		ID:             r.ID,
		Scope:          scope,
		UserID:         r.UserID,
		ProjectID:      r.ProjectID,
		MemoryType:     mt,
		Title:          r.Title,
		Body:           r.Body,
		Why:            r.Why,
		Tags:           r.Tags,
		Source:         src,
		AgentActor:     r.AgentActor,
		ConversationID: r.ConversationID,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}, true
}
