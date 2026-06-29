package discord

import (
	"reflect"
	"testing"
)

func TestMemoryPickStore_DrainFoldsSingleAndMulti(t *testing.T) {
	s := NewMemoryPickStore()
	s.Record("m1", "0", []string{"prod"})
	s.Record("m1", "1", []string{"a", "b"})

	got := s.Drain("m1")
	if got["q0"] != "prod" {
		t.Errorf("q0 = %v, want single string prod", got["q0"])
	}
	multi, ok := got["q1"].([]any)
	if !ok || !reflect.DeepEqual(multi, []any{"a", "b"}) {
		t.Errorf("q1 = %v, want []any{a,b}", got["q1"])
	}
	// Drain clears the message — a second drain finds nothing.
	if again := s.Drain("m1"); again != nil {
		t.Errorf("second Drain = %v, want nil (cleared)", again)
	}
}

func TestMemoryPickStore_RePickWins(t *testing.T) {
	s := NewMemoryPickStore()
	s.Record("m1", "0", []string{"old"})
	s.Record("m1", "0", []string{"new"})
	if got := s.Drain("m1"); got["q0"] != "new" {
		t.Errorf("q0 = %v, want new (re-pick wins)", got["q0"])
	}
}

func TestMemoryPickStore_EmptyAndUnknown(t *testing.T) {
	s := NewMemoryPickStore()
	if got := s.Drain("never-recorded"); got != nil {
		t.Errorf("Drain(unknown) = %v, want nil", got)
	}
	// A pick with only blank values folds to nothing.
	s.Record("m1", "0", []string{"  "})
	if got := s.Drain("m1"); got != nil {
		t.Errorf("Drain of all-blank picks = %v, want nil", got)
	}
	// Empty message/question ids are ignored.
	s.Record("", "0", []string{"x"})
	s.Record("m2", "", []string{"x"})
	if got := s.Drain("m2"); got != nil {
		t.Errorf("Drain(m2) = %v, want nil (blank question id ignored)", got)
	}
}
