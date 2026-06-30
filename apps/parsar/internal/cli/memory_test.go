package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestRunMemoryListEmpty(t *testing.T) {
	ctx, stdout, _, _ := newTestCtx(t, http.StatusOK, `{"memories":[]}`)
	if err := runMemory(ctx, []string{"list", "--scope", "user"}); err != nil {
		t.Fatalf("memory list: %v", err)
	}
	if !strings.Contains(stdout.String(), "(no memories)") {
		t.Errorf("expected empty-state, got %q", stdout.String())
	}
}

func TestRunMemoryListTable(t *testing.T) {
	body := `{"memories":[{"id":"m1","scope":"user","user_id":"u","memory_type":"feedback","body":"don't mock the DB","why":"prod migration broke","tags":["test"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusOK, body)
	if err := runMemory(ctx, []string{"list", "--scope", "user", "--type", "feedback"}); err != nil {
		t.Fatalf("memory list: %v", err)
	}
	got := stdout.String()
	for _, w := range []string{"m1", "feedback", "don't mock"} {
		if !strings.Contains(got, w) {
			t.Errorf("missing %q in:\n%s", w, got)
		}
	}
	if !strings.Contains(rec.query, "scope=user") || !strings.Contains(rec.query, "memory_type=feedback") {
		t.Errorf("query = %q", rec.query)
	}
}

func TestRunMemoryAddRequiresTypeAndBody(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusCreated, `{}`)
	if err := runMemory(ctx, []string{"add"}); err == nil || !strings.Contains(err.Error(), "--type") {
		t.Fatalf("expected --type error, got %v", err)
	}
	if err := runMemory(ctx, []string{"add", "--type", "user"}); err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected --body error, got %v", err)
	}
}

func TestRunMemoryAddSuccess(t *testing.T) {
	body := `{"id":"m1","scope":"user","user_id":"u","memory_type":"user","body":"hi","tags":["x"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusCreated, body)
	if err := runMemory(ctx, []string{"add", "--type", "user", "--body", "hi", "--tag", "x"}); err != nil {
		t.Fatalf("memory add: %v", err)
	}
	if !strings.Contains(stdout.String(), "created memory m1") {
		t.Errorf("output = %q", stdout.String())
	}
	if !strings.Contains(rec.body, `"memory_type":"user"`) {
		t.Errorf("request body = %s", rec.body)
	}
}

func TestRunMemoryAddWorkspaceScope(t *testing.T) {
	body := `{"id":"m1","scope":"workspace","user_id":"u","workspace_id":"p","memory_type":"feedback","body":"hi","why":"because","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx, _, _, rec := newTestCtx(t, http.StatusCreated, body)
	err := runMemory(ctx, []string{"add", "--scope", "workspace", "--type", "feedback", "--body", "hi", "--why", "because"})
	if err != nil {
		t.Fatalf("memory add: %v", err)
	}
	if !strings.Contains(rec.body, `"scope":"workspace"`) || !strings.Contains(rec.body, `"why":"because"`) {
		t.Errorf("request body = %s", rec.body)
	}
}

func TestRunMemoryEditClearTags(t *testing.T) {
	body := `{"id":"m1","scope":"user","user_id":"u","memory_type":"user","body":"hi","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx, _, _, rec := newTestCtx(t, http.StatusOK, body)
	if err := runMemory(ctx, []string{"edit", "--clear-tags", "m1"}); err != nil {
		t.Fatalf("memory edit: %v", err)
	}
	if !strings.Contains(rec.body, `"tags":[]`) {
		t.Errorf("expected empty-tags body, got %s", rec.body)
	}
}

func TestRunMemoryRm(t *testing.T) {
	ctx, _, _, rec := newTestCtx(t, http.StatusNoContent, "")
	if err := runMemory(ctx, []string{"rm", "m1"}); err != nil {
		t.Fatalf("memory rm: %v", err)
	}
	if rec.method != http.MethodDelete {
		t.Errorf("method = %q", rec.method)
	}
}

func TestRunMemoryRmServerError(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusNotFound, `{"error":"not_found","message":""}`)
	err := runMemory(ctx, []string{"rm", "missing"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "memory rm") {
		t.Errorf("error should be wrapped with subcommand prefix: %v", err)
	}
}
