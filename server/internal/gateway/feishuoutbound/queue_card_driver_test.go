package feishuoutbound

import (
	"context"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// TestQueueCardTickOnce_MultiPodClaimNoDuplicates is the regression
// pin for the prod 2026-06-15 "排队中（第 2 位）"-card-spam: two
// pods both ticked, both ListPendingQueuedFeishuRuns SELECTed the
// same queued runs, both Feishu-sent, the user saw ~3 duplicates
// per run.
//
// With the new ClaimPendingQueuedFeishuRuns the first pod's tick
// drains the pending slice (fakeStore simulates the FOR UPDATE +
// stamp), and the second pod's tick sees nothing. Total Feishu
// sends == number of queued runs, not (pods × runs).
func TestQueueCardTickOnce_MultiPodClaimNoDuplicates(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_react"] = happyAgentWithAppID("cli_react")
	fs.secrets["secret_happy"] = happySecret()
	fs.pendingQueued = []store.PendingQueuedFeishuRun{
		{
			RunID:          "run-A",
			WorkspaceID:    "ws-1",
			ConversationID: "conv-1",
			ExternalChatID: "oc_chat",
			SourceAppID:    "cli_react",
		},
		{
			RunID:          "run-B",
			WorkspaceID:    "ws-1",
			ConversationID: "conv-1",
			ExternalChatID: "oc_chat",
			SourceAppID:    "cli_react",
		},
	}

	rec := newUpstreamRecorder(t)

	podA, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL, ClaimedBy: "pod-A"})
	if err != nil {
		t.Fatal(err)
	}
	podB, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL, ClaimedBy: "pod-B"})
	if err != nil {
		t.Fatal(err)
	}

	sentA, err := podA.QueueCardTickOnce(context.Background())
	if err != nil {
		t.Fatalf("pod A tick: %v", err)
	}
	sentB, err := podB.QueueCardTickOnce(context.Background())
	if err != nil {
		t.Fatalf("pod B tick: %v", err)
	}

	// One pod claims the batch, the other sees nothing.
	if sentA+sentB != 2 {
		t.Errorf("total cards sent = %d, want 2 (one per queued run, not duplicated by 2 pods)", sentA+sentB)
	}
	if sentA > 0 && sentB > 0 {
		t.Errorf("both pods sent (A=%d, B=%d) — claim semantics broken; each run should only be picked by one pod", sentA, sentB)
	}

	// Upstream side: exactly two POSTs, one per run, no duplication.
	if got := rec.sends.Load(); got != 2 {
		t.Errorf("upstream POSTs = %d, want 2 — multi-pod race delivered duplicate cards", got)
	}

	// StampQueueCardSent fired once per run.
	if got := len(fs.queueStamps); got != 2 {
		t.Errorf("StampQueueCardSent calls = %d, want 2 (one per run)", got)
	}

	// Both pods went through the claim path with their own identity.
	if len(fs.queueClaimedBys) != 2 {
		t.Fatalf("ClaimPendingQueuedFeishuRuns invocations = %d, want 2 (one per pod tick)", len(fs.queueClaimedBys))
	}
	gotA, gotB := fs.queueClaimedBys[0] == "pod-A", fs.queueClaimedBys[1] == "pod-B"
	if !(gotA && gotB) {
		t.Errorf("claimed_by sequence = %v, want [pod-A pod-B]", fs.queueClaimedBys)
	}
}

// TestQueueCardTickOnce_EmptyBatchNoOp confirms the cheap fast path:
// no queued runs → no Feishu calls, no Stamps.
func TestQueueCardTickOnce_EmptyBatchNoOp(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_react"] = happyAgentWithAppID("cli_react")
	fs.secrets["secret_happy"] = happySecret()
	// pendingQueued left empty

	rec := newUpstreamRecorder(t)
	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if err != nil {
		t.Fatal(err)
	}

	sent, err := worker.QueueCardTickOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Errorf("sent = %d, want 0", sent)
	}
	if got := rec.sends.Load(); got != 0 {
		t.Errorf("upstream POSTs = %d, want 0", got)
	}
	if got := len(fs.queueStamps); got != 0 {
		t.Errorf("StampQueueCardSent calls = %d, want 0", got)
	}
}
