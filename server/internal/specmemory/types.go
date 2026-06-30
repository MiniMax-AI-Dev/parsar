// Package specmemory owns the workspace-level spec fragments and the
// user-/workspace-level memories that get injected into agent sessions
// at SessionStart and per-turn. See docs/spec-memory-module.md for
// the end-to-end design.
//
// The DB columns behind the string enums (Source / Scope / MemoryType)
// are plain `text NOT NULL` (no CHECK IN constraint) so adding a new
// enum value never needs a migration — add the constant here and
// update Valid().
//
// Convention: store layer reads/writes plain strings to sqlc; callers
// convert to/from typed enums at the boundary. Handler/CLI input
// validation goes through XxxFromString() so unknown values surface
// as a typed error before reaching the DB.
package specmemory

import (
	"fmt"
	"time"
)

// Source describes who/what created a spec fragment or memory row.
// Shared by spec_fragments.source and memories.source; the service
// layer enforces per-table subsets so this type stays small.
type Source string

const (
	// SourceManual — UI user typed it in. spec_fragments + memories.
	SourceManual Source = "manual"
	// SourceAgent — agent CLI wrote it. spec_fragments + memories.
	SourceAgent Source = "agent"
	// SourceImport — bulk text paste import path. spec_fragments only.
	SourceImport Source = "import"
	// SourceUser — UI user wrote a memory (memories.source uses this
	// instead of SourceManual so the column name matches legacy systems).
	SourceUser Source = "user"
	// SourceAutoReview — reserved for the post-turn LLM review path.
	SourceAutoReview Source = "auto-review"
)

// Valid reports whether s is a known Source. The DB will happily
// store any text, so unknown values must be rejected here.
func (s Source) Valid() bool {
	switch s {
	case SourceManual, SourceAgent, SourceImport, SourceUser, SourceAutoReview:
		return true
	}
	return false
}

// String returns the wire / DB representation.
func (s Source) String() string { return string(s) }

// SourceFromString parses a DB-side string back into a typed Source.
// Use at the service-layer read boundary so a poisoned row surfaces
// before reaching injection prompts.
func SourceFromString(s string) (Source, error) {
	v := Source(s)
	if !v.Valid() {
		return "", fmt.Errorf("specmemory: unknown source %q", s)
	}
	return v, nil
}

// Scope distinguishes user-level memories (visible only to the
// author) from workspace-level memories (shared across all users on
// the workspace). spec_fragments is implicitly workspace-scoped.
type Scope string

const (
	// ScopeUser — memory belongs to a single user across the workspace.
	ScopeUser Scope = "user"
	// ScopeWorkspace — memory is shared across all users on a workspace.
	// memories.workspace_id required (enforced by
	// memories_scope_workspace_id_match_check).
	ScopeWorkspace Scope = "workspace"
)

func (s Scope) Valid() bool {
	switch s {
	case ScopeUser, ScopeWorkspace:
		return true
	}
	return false
}

func (s Scope) String() string { return string(s) }

func ScopeFromString(s string) (Scope, error) {
	v := Scope(s)
	if !v.Valid() {
		return "", fmt.Errorf("specmemory: unknown scope %q", s)
	}
	return v, nil
}

// MemoryType is the four-category auto-memory taxonomy. UI groups
// memory lists by type; the agent picks one when calling
// `parsar memory add --type ...`.
type MemoryType string

const (
	// MemoryTypeUser — facts about the user's role, preferences,
	// responsibilities, knowledge.
	MemoryTypeUser MemoryType = "user"
	// MemoryTypeFeedback — guidance the user has given about how to
	// approach work. Why field is strongly recommended.
	MemoryTypeFeedback MemoryType = "feedback"
	// MemoryTypeWorkspace — ongoing workspace state (initiatives,
	// constraints, deadlines) not derivable from code or git history.
	// Why field is strongly recommended.
	MemoryTypeWorkspace MemoryType = "workspace"
	// MemoryTypeReference — pointers to external systems (dashboards,
	// docs, Slack channels).
	MemoryTypeReference MemoryType = "reference"
)

func (t MemoryType) Valid() bool {
	switch t {
	case MemoryTypeUser, MemoryTypeFeedback, MemoryTypeWorkspace, MemoryTypeReference:
		return true
	}
	return false
}

func (t MemoryType) String() string { return string(t) }

func MemoryTypeFromString(s string) (MemoryType, error) {
	v := MemoryType(s)
	if !v.Valid() {
		return "", fmt.Errorf("specmemory: unknown memory type %q", s)
	}
	return v, nil
}

// Fragment is the business-level view of a spec_fragments row.
//
// CreatedBy is "" for agent writes (spec_fragments.created_by IS NULL);
// AgentActor is "" for human writes.
type Fragment struct {
	ID          string
	WorkspaceID string
	Title       string
	Body        string
	Tags        []string
	Source      Source
	CreatedBy   string // user UUID or "" for agent writes
	AgentActor  string // "connector:agentID" or "" for human writes
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Memory is the business-level view of a memories row. WorkspaceID and
// ConversationID use "" for SQL NULL (matches audit.Event).
type Memory struct {
	ID             string
	Scope          Scope
	UserID         string
	WorkspaceID    string // "" when scope=user
	MemoryType     MemoryType
	Title          string
	Body           string
	Why            string
	Tags           []string
	Source         Source
	AgentActor     string // "" for human writes
	ConversationID string // "" when the write wasn't tied to a session turn
	CreatedAt      time.Time
	UpdatedAt      time.Time
}
