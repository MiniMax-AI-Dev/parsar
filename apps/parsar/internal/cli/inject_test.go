package cli

import (
	"net/http"
	"strings"
	"testing"
)

func TestRunInjectSnapshotJSON(t *testing.T) {
	body := `{"spec_block":"<spec>x</spec>","memory_block":"<memory></memory>","memory_write_guide":"<guide></guide>","incremental_memory":""}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusOK, body)
	if err := runInject(ctx, []string{"snapshot"}); err != nil {
		t.Fatalf("inject snapshot: %v", err)
	}
	if !strings.Contains(stdout.String(), `"spec_block": "<spec>x</spec>"`) {
		t.Errorf("expected formatted spec_block, got %q", stdout.String())
	}
	if rec.path != "/api/v1/agent-runtime/injection/snapshot" {
		t.Errorf("path = %q", rec.path)
	}
}

func TestRunInjectIncrementalRequiresSince(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, `{}`)
	err := runInject(ctx, []string{"incremental"})
	if err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("expected --since error, got %v", err)
	}
}

func TestRunInjectIncrementalBadSince(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, `{}`)
	err := runInject(ctx, []string{"incremental", "--since", "yesterday"})
	if err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Fatalf("expected RFC3339 error, got %v", err)
	}
}

func TestRunInjectIncrementalSuccess(t *testing.T) {
	body := `{"spec_block":"","memory_block":"","memory_write_guide":"","incremental_memory":"<delta/>"}`
	ctx, stdout, _, rec := newTestCtx(t, http.StatusOK, body)
	err := runInject(ctx, []string{"incremental", "--since", "2026-01-02T03:04:05Z"})
	if err != nil {
		t.Fatalf("inject incremental: %v", err)
	}
	if !strings.Contains(stdout.String(), `"incremental_memory": "<delta/>"`) {
		t.Errorf("expected delta in output, got %q", stdout.String())
	}
	if !strings.Contains(rec.query, "since=2026-01-02T03%3A04%3A05Z") {
		t.Errorf("missing since in query: %q", rec.query)
	}
}

func TestRunInjectMissingSubcommand(t *testing.T) {
	ctx, _, _, _ := newTestCtx(t, http.StatusOK, ``)
	err := runInject(ctx, nil)
	if err == nil || !strings.Contains(err.Error(), "missing subcommand") {
		t.Fatalf("expected missing-subcommand, got %v", err)
	}
}

func TestRunSyncDumpsHumanReadable(t *testing.T) {
	body := `{"spec_block":"<spec>X</spec>","memory_block":"","memory_write_guide":"<guide/>","incremental_memory":""}`
	srv, _ := stubServer(t, http.StatusOK, body)
	ctx, stdout, _ := newCapturedCtx(&Config{
		ServerURL:   srv.URL,
		RunnerToken: "tok",
		RuntimeID:   "rt-1",
		WorkspaceID: "ws-1",
		UserID:      "u-1",
	})
	if err := runSync(ctx, nil); err != nil {
		t.Fatalf("sync: %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"runtime_id", "rt-1", "workspace_id", "ws-1", "--- spec ---", "<spec>X</spec>", "--- memory ---", "(empty)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in sync output:\n%s", want, out)
		}
	}
}
