package specmemory

import "testing"

func TestSourceValid(t *testing.T) {
	valid := []Source{SourceManual, SourceAgent, SourceImport, SourceUser, SourceAutoReview}
	for _, s := range valid {
		if !s.Valid() {
			t.Errorf("Source %q should be Valid()", s)
		}
	}
	invalid := []Source{"", "unknown", "Manual", "AGENT", " manual"}
	for _, s := range invalid {
		if s.Valid() {
			t.Errorf("Source %q should NOT be Valid()", s)
		}
	}
}

func TestSourceFromString(t *testing.T) {
	got, err := SourceFromString("agent")
	if err != nil {
		t.Fatalf("SourceFromString(agent): unexpected error %v", err)
	}
	if got != SourceAgent {
		t.Errorf("SourceFromString(agent) = %q, want %q", got, SourceAgent)
	}

	if _, err := SourceFromString("nope"); err == nil {
		t.Fatal("SourceFromString(nope): expected error, got nil")
	}
}

func TestScopeValid(t *testing.T) {
	for _, s := range []Scope{ScopeUser, ScopeWorkspace} {
		if !s.Valid() {
			t.Errorf("Scope %q should be Valid()", s)
		}
	}
	for _, s := range []Scope{"", "project", "USER", " workspace"} {
		if s.Valid() {
			t.Errorf("Scope %q should NOT be Valid()", s)
		}
	}
}

func TestScopeFromString(t *testing.T) {
	got, err := ScopeFromString("workspace")
	if err != nil {
		t.Fatalf("ScopeFromString(workspace): unexpected error %v", err)
	}
	if got != ScopeWorkspace {
		t.Errorf("ScopeFromString(workspace) = %q, want %q", got, ScopeWorkspace)
	}

	if _, err := ScopeFromString("team"); err == nil {
		t.Fatal("ScopeFromString(team): expected error, got nil")
	}
}

func TestMemoryTypeValid(t *testing.T) {
	for _, mt := range []MemoryType{MemoryTypeUser, MemoryTypeFeedback, MemoryTypeWorkspace, MemoryTypeReference} {
		if !mt.Valid() {
			t.Errorf("MemoryType %q should be Valid()", mt)
		}
	}
	for _, mt := range []MemoryType{"", "note", "Memory", "users"} {
		if mt.Valid() {
			t.Errorf("MemoryType %q should NOT be Valid()", mt)
		}
	}
}

func TestMemoryTypeFromString(t *testing.T) {
	got, err := MemoryTypeFromString("feedback")
	if err != nil {
		t.Fatalf("MemoryTypeFromString(feedback): unexpected error %v", err)
	}
	if got != MemoryTypeFeedback {
		t.Errorf("MemoryTypeFromString(feedback) = %q, want %q", got, MemoryTypeFeedback)
	}

	if _, err := MemoryTypeFromString("rule"); err == nil {
		t.Fatal("MemoryTypeFromString(rule): expected error, got nil")
	}
}

func TestEnumStringRoundTrip(t *testing.T) {
	if SourceAgent.String() != "agent" {
		t.Errorf("SourceAgent.String() = %q, want %q", SourceAgent.String(), "agent")
	}
	if ScopeWorkspace.String() != "workspace" {
		t.Errorf("ScopeWorkspace.String() = %q, want %q", ScopeWorkspace.String(), "workspace")
	}
	if MemoryTypeReference.String() != "reference" {
		t.Errorf("MemoryTypeReference.String() = %q, want %q", MemoryTypeReference.String(), "reference")
	}
}
