package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestCtx wires the runContext to a stub HTTP server so subcommand
// integration tests exercise the full parse → client → server path.
func newTestCtx(t *testing.T, status int, body string) (*runContext, *bytes.Buffer, *httptest.Server, *recordedReq) {
	t.Helper()
	srv, rec := stubServer(t, status, body)
	ctx, stdout, _ := newCapturedCtx(&Config{ServerURL: srv.URL, RunnerToken: "tok"})
	return ctx, stdout, srv, rec
}

func TestRunSpecListEmpty(t *testing.T) {
	ctx, stdout, _, _ := newTestCtx(t, http.StatusOK, `{"fragments":[]}`)
	if err := runSpec(ctx, []string{"list"}); err != nil {
		t.Fatalf("spec list: %v", err)
	}
	if !strings.Contains(stdout.String(), "(no spec fragments)") {
		t.Errorf("expected empty-state message, got %q", stdout.String())
	}
}

func TestRunSpecListTable(t *testing.T) {
	body := `{"fragments":[{"id":"f1","workspace_id":"ws","title":"Use gin","body":"...","tags":["go"],"source":"manual","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusOK, body)
	if err := runSpec(ctx, []string{"list", "--tag", "go", "--limit", "10"}); err != nil {
		t.Fatalf("spec list: %v", err)
	}
	got := stdout.String()
	for _, w := range []string{"ID", "TITLE", "f1", "Use gin", "manual"} {
		if !strings.Contains(got, w) {
			t.Errorf("table missing %q in:\n%s", w, got)
		}
	}
	if !strings.Contains(rec.query, "tag=go") {
		t.Errorf("query missing tag: %q", rec.query)
	}
}

func TestRunSpecListJSON(t *testing.T) {
	body := `{"fragments":[{"id":"f1","workspace_id":"ws","title":"T","body":"B","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}]}`
	ctx, stdout, _, _ := newTestCtx(t, http.StatusOK, body)
	if err := runSpec(ctx, []string{"list", "--json"}); err != nil {
		t.Fatalf("spec list --json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "[") {
		t.Errorf("expected JSON array, got %q", stdout.String())
	}
}

func TestRunSpecAddRequiresTitleAndBody(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusCreated, `{}`)
	err := runSpec(ctx, []string{"add"})
	if err == nil || !strings.Contains(err.Error(), "--title") {
		t.Fatalf("expected --title error, got %v", err)
	}
	err = runSpec(ctx, []string{"add", "--title", "T"})
	if err == nil || !strings.Contains(err.Error(), "--body") {
		t.Fatalf("expected --body error, got %v", err)
	}
}

func TestRunSpecAddSuccess(t *testing.T) {
	body := `{"id":"new","workspace_id":"ws","title":"T","body":"B","tags":["a"],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusCreated, body)
	if err := runSpec(ctx, []string{"add", "--title", "T", "--body", "B", "--tag", "a,b"}); err != nil {
		t.Fatalf("spec add: %v", err)
	}
	if !strings.Contains(stdout.String(), "created fragment new") {
		t.Errorf("output = %q", stdout.String())
	}
	if !strings.Contains(rec.body, `"tags":["a","b"]`) {
		t.Errorf("body tags missing in request: %s", rec.body)
	}
}

func TestRunSpecEditNeedsPositionalID(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, `{}`)
	if err := runSpec(ctx, []string{"edit"}); err == nil || !strings.Contains(err.Error(), "positional") {
		t.Fatalf("expected positional-id error, got %v", err)
	}
}

func TestRunSpecEditClearTags(t *testing.T) {
	body := `{"id":"f1","workspace_id":"ws","title":"T","body":"B","tags":[],"source":"agent","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	ctx, _, _, rec := newTestCtx(t, http.StatusOK, body)
	if err := runSpec(ctx, []string{"edit", "--clear-tags", "f1"}); err != nil {
		t.Fatalf("spec edit: %v", err)
	}
	if !strings.Contains(rec.body, `"tags":[]`) {
		t.Errorf("expected empty-tags body, got %s", rec.body)
	}
}

func TestRunSpecRmDeletes(t *testing.T) {
	ctx, _, _, rec := newTestCtx(t, http.StatusNoContent, "")
	if err := runSpec(ctx, []string{"rm", "f1"}); err != nil {
		t.Fatalf("spec rm: %v", err)
	}
	if rec.method != http.MethodDelete {
		t.Errorf("method = %q", rec.method)
	}
}

func TestRunSpecMissingSubcommand(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, ``)
	err := runSpec(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("expected missing-subcommand, got %v", err)
	}
}

func TestRunSpecUnknownSubcommand(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, ``)
	err := runSpec(ctx, []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown-subcommand, got %v", err)
	}
}
