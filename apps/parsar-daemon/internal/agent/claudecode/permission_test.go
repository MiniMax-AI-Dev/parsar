package claudecode_test

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/claudecode"
)

func TestPendingTableRoundTrip(t *testing.T) {
	tbl := claudecode.NewPendingTableForTest()
	tbl.Record("perm_aabbccdd", "req_42", map[string]any{"command": "ls -la"})

	e, ok := tbl.Resolve("perm_aabbccdd")
	if !ok {
		t.Fatal("Resolve missed a recorded perm id")
	}
	if e.CCRequestID != "req_42" {
		t.Errorf("CCRequestID = %q, want req_42", e.CCRequestID)
	}
	if e.Input["command"] != "ls -la" {
		t.Errorf("input not preserved, got %v", e.Input)
	}

	permID, ok := tbl.LookupByCC("req_42")
	if !ok || permID != "perm_aabbccdd" {
		t.Errorf("LookupByCC returned (%q,%v), want (perm_aabbccdd,true)", permID, ok)
	}
}

func TestPendingTableDeleteIsBidirectional(t *testing.T) {
	tbl := claudecode.NewPendingTableForTest()
	tbl.Record("perm_1", "req_a", nil)
	tbl.Record("perm_2", "req_b", nil)

	tbl.Delete("perm_1")
	if _, ok := tbl.Resolve("perm_1"); ok {
		t.Error("Resolve still returns deleted perm")
	}
	if _, ok := tbl.LookupByCC("req_a"); ok {
		t.Error("LookupByCC still returns deleted cc id")
	}
	if tbl.Len() != 1 {
		t.Errorf("Len = %d, want 1", tbl.Len())
	}
	if _, ok := tbl.Resolve("perm_2"); !ok {
		t.Error("untouched perm_2 disappeared")
	}
}

func TestPendingTableUnknownLookupsAreFalse(t *testing.T) {
	tbl := claudecode.NewPendingTableForTest()
	if _, ok := tbl.Resolve("perm_nope"); ok {
		t.Error("Resolve returned ok for unknown perm")
	}
	if _, ok := tbl.LookupByCC("req_nope"); ok {
		t.Error("LookupByCC returned ok for unknown cc")
	}
	tbl.Delete("perm_nope")
}

func TestPendingTableIgnoresEmptyIDs(t *testing.T) {
	tbl := claudecode.NewPendingTableForTest()
	tbl.Record("", "req_a", nil)
	tbl.Record("perm_a", "", nil)
	if tbl.Len() != 0 {
		t.Errorf("Len = %d after empty-id records, want 0", tbl.Len())
	}
	if _, ok := tbl.LookupByCC(""); ok {
		t.Error("LookupByCC ok for empty cc id")
	}
}
