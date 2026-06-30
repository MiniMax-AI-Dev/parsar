package feishuoutbound

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestAssembleDoneCardData_FullFooter is the happy path the user
// originally reported as broken: a finished claudecode run with steps,
// usage_logs rows, and timing populated. The helper should return a
// fully-populated UsageStats that lets the renderer emit the
// `6m49s | $6.32 | 152k/200k (76%) | opus-4-8`-style footer.
func TestAssembleDoneCardData_FullFooter(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	const runID = "00000000-0000-0000-0000-00000000aaaa"
	start := time.Now().UTC().Add(-7 * time.Minute)
	finish := start.Add(6*time.Minute + 49*time.Second)
	fs.doneCardData[runID] = store.DoneCardRunData{
		StartedAt:         start,
		FinishedAt:        finish,
		Model:             "claude-opus-4-1-20250805",
		ContextUsedTokens: 152_000,
		CostUSD:           6.32,
		HasUsage:          true,
	}
	// Seed a couple of tool.call events so steps are populated too.
	fs.inflightEvents[runID] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Bash", "args": map[string]any{"command": "ls"}}},
		{Sequence: 2, EventKind: "tool.call", Payload: map[string]any{"name": "Read", "args": map[string]any{"file_path": "x.go"}}},
	}

	out, err := assembleDoneCardData(context.Background(), fs, assembleDoneCardInput{
		WorkspaceID: "ws",
		RunID:       runID,
	})
	if err != nil {
		t.Fatalf("assembleDoneCardData err = %v", err)
	}
	if got, want := out.Elapsed, 6*time.Minute+49*time.Second; got != want {
		t.Errorf("Elapsed = %s, want %s", got, want)
	}
	if out.Usage == nil {
		t.Fatalf("Usage is nil, want full footer")
	}
	if out.Usage.CostUSD != 6.32 {
		t.Errorf("CostUSD = %v, want 6.32", out.Usage.CostUSD)
	}
	if out.Usage.ContextUsed != 152_000 {
		t.Errorf("ContextUsed = %d, want 152000", out.Usage.ContextUsed)
	}
	if out.Usage.ContextWindow != 200_000 {
		t.Errorf("ContextWindow = %d, want 200000 (claude family)", out.Usage.ContextWindow)
	}
	if out.Usage.Model != "claude-opus-4-1-20250805" {
		t.Errorf("Model = %q, want claude-opus-4-1-20250805", out.Usage.Model)
	}
	if len(out.Steps) != 2 {
		t.Errorf("Steps len = %d, want 2", len(out.Steps))
	}
}

// TestAssembleDoneCardData_UnknownModelDegrades verifies the user's
// requested fallback: when the model id doesn't match any known
// context window, the footer renders without the percentage line
// rather than misrepresenting an unknown model as "0/0 (NaN%)".
func TestAssembleDoneCardData_UnknownModelDegrades(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	const runID = "00000000-0000-0000-0000-00000000bbbb"
	fs.doneCardData[runID] = store.DoneCardRunData{
		StartedAt:         time.Now().UTC().Add(-30 * time.Second),
		FinishedAt:        time.Now().UTC(),
		Model:             "custom-llm-from-some-gateway",
		ContextUsedTokens: 5000,
		CostUSD:           0.02,
		HasUsage:          true,
	}

	out, err := assembleDoneCardData(context.Background(), fs, assembleDoneCardInput{
		WorkspaceID: "ws",
		RunID:       runID,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Usage != nil {
		t.Errorf("Usage = %+v, want nil for unknown-model fallback", out.Usage)
	}
	if out.Elapsed <= 0 {
		t.Errorf("Elapsed still computed despite missing window, got %s", out.Elapsed)
	}
}

// TestAssembleDoneCardData_NoUsageYet covers the "run too fast for
// usage_logs to land" case: the helper returns nil Usage and a card
// path renders the short footer.
func TestAssembleDoneCardData_NoUsageYet(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	const runID = "00000000-0000-0000-0000-00000000cccc"
	fs.doneCardData[runID] = store.DoneCardRunData{
		StartedAt:  time.Now().UTC().Add(-2 * time.Second),
		FinishedAt: time.Now().UTC(),
		HasUsage:   false,
	}

	out, err := assembleDoneCardData(context.Background(), fs, assembleDoneCardInput{
		WorkspaceID: "ws",
		RunID:       runID,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Usage != nil {
		t.Errorf("Usage = %+v, want nil when HasUsage=false", out.Usage)
	}
}

// TestAssembleDoneCardData_PrefilledSkipsEventRead covers the inflight
// driver's optimisation: when the driver hands prefilled steps the
// helper must not re-read events.
func TestAssembleDoneCardData_PrefilledSkipsEventRead(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	const runID = "00000000-0000-0000-0000-00000000dddd"
	// Seed events the helper would normally fold — these MUST be
	// ignored because PrefilledSteps is non-nil.
	fs.inflightEvents[runID] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Glob"}},
	}
	prefilled := []gateway.StepInfo{{Tool: "Bash", Label: "echo hi"}}

	out, err := assembleDoneCardData(context.Background(), fs, assembleDoneCardInput{
		WorkspaceID:      "ws",
		RunID:            runID,
		PrefilledSteps:   prefilled,
		PrefilledElapsed: 12 * time.Second,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(out.Steps) != 1 || out.Steps[0].Tool != "Bash" {
		t.Errorf("Steps = %+v, want PrefilledSteps preserved (no event read)", out.Steps)
	}
	if out.Elapsed != 12*time.Second {
		t.Errorf("Elapsed = %s, want 12s prefilled (no run-data read for elapsed)", out.Elapsed)
	}
}

// TestAssembleDoneCardData_EmptyRunID covers the defensive path: a
// producer regression that strips run_id from metadata shouldn't crash
// the send loop. Helper returns empty values and the caller renders
// the short fallback footer.
func TestAssembleDoneCardData_EmptyRunID(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	out, err := assembleDoneCardData(context.Background(), fs, assembleDoneCardInput{
		WorkspaceID: "ws",
		RunID:       "",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if out.Usage != nil {
		t.Errorf("Usage = %+v, want nil for empty runID", out.Usage)
	}
	if len(out.Steps) != 0 {
		t.Errorf("Steps len = %d, want 0", len(out.Steps))
	}
}

// TestBuildOutboundCardContent_RendersUsageFromStore and
// TestBuildOutboundCardContent_RunFailureKeepsErrorCard were deleted
// in the driver-only refactor (Phase 5): buildOutboundCardContent
// was a P1-worker-only helper that no longer exists. The driver
// path uses buildFinalCardForRun (inflight_driver.go) instead, and
// the inflight driver tests + the assembleDoneCardData unit tests
// above cover the same content contract.
