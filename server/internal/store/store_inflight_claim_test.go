package store

// Covers the multi-pod-safe claim query. These tests need real
// Postgres semantics (SELECT FOR UPDATE SKIP LOCKED, jsonb claim
// drift), so they live in the integration tier
// (PARSAR_TEST_DATABASE_URL).

import (
	"context"
	"testing"
	"time"
)

// seedFeishuRunForClaim wires the dev fixture's conversation into
// platform=feishu and pushes a user message to spawn a run.
func seedFeishuRunForClaim(t *testing.T, store *Store, externalChatID string) (conversationID, runID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := store.SeedDevFixture(ctx); err != nil {
		t.Fatal(err)
	}
	ids := DefaultDevFixtureIDs()
	if _, err := store.ConfigureDevConversationExternalRef(ctx, ConfigureDevConversationExternalRefInput{
		ConversationID: ids.ConversationID,
		Gateway:        "feishu",
		ExternalChatID: externalChatID,
	}); err != nil {
		t.Fatalf("configure conv external ref: %v", err)
	}
	sendRes, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    ids.ConversationID,
		UserID:            ids.UserID,
		Content:           "@产品Agent claim-test",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatalf("send user msg: %v", err)
	}
	if len(sendRes.RunIDs) == 0 {
		t.Fatal("no run id")
	}
	return ids.ConversationID, sendRes.RunIDs[0]
}

// TestClaim_DisjointBatchesAcrossSiblings: SKIP LOCKED gives disjoint
// batches when two pods run the same query at the same time.
func TestClaim_DisjointBatchesAcrossSiblings(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_disjoint")
	// One tool.call so the row qualifies as having "something to
	// render" — lifecycle-only events are filtered out.
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "tool.call",
		Payload:    map[string]any{"name": "Bash", "args": map[string]any{"command": "echo hi"}},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)
	stale := now.Add(-30 * time.Second)

	type result struct {
		rows []FeishuInflightConversation
		err  error
	}
	doneA := make(chan result, 1)
	doneB := make(chan result, 1)
	go func() {
		rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
			FinishedCutoff: cutoff,
			StaleBefore:    stale,
			ClaimedBy:      "pod-A",
			Limit:          32,
		})
		doneA <- result{rows, err}
	}()
	go func() {
		rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
			FinishedCutoff: cutoff,
			StaleBefore:    stale,
			ClaimedBy:      "pod-B",
			Limit:          32,
		})
		doneB <- result{rows, err}
	}()
	a := <-doneA
	b := <-doneB

	if a.err != nil {
		t.Fatalf("pod A claim: %v", a.err)
	}
	if b.err != nil {
		t.Fatalf("pod B claim: %v", b.err)
	}

	// SKIP LOCKED races the pods so we don't pin WHICH one gets the
	// row — only that no double-delivery occurs.
	hitCount := 0
	for _, r := range a.rows {
		if r.AgentRunID == runID {
			hitCount++
		}
	}
	for _, r := range b.rows {
		if r.AgentRunID == runID {
			hitCount++
		}
	}
	if hitCount != 1 {
		t.Errorf("run appeared in %d pod claims, want exactly 1 (SKIP LOCKED must give disjoint batches)", hitCount)
	}
}

// TestClaim_StaleClaimGetsAdopted: failover. A pod that took the claim
// and then died must release it back after stale_before; otherwise an
// OOM would pin the conversation forever.
func TestClaim_StaleClaimGetsAdopted(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_stale")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "tool.call",
		Payload:    map[string]any{"name": "Bash"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)

	// Pod A claims with a recoverable stale_before so it grabs the row.
	veryStale := now.Add(-2 * time.Minute)
	if _, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    now,
		ClaimedBy:      "pod-A-dead",
		Limit:          32,
	}); err != nil {
		t.Fatalf("pod A initial claim: %v", err)
	}

	// Pod B with a strict stale_before must NOT poach pod A's fresh claim.
	rowsB1, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    veryStale,
		ClaimedBy:      "pod-B",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("pod B fresh-window claim: %v", err)
	}
	for _, r := range rowsB1 {
		if r.AgentRunID == runID {
			t.Fatal("pod B grabbed a non-stale claim from pod A — stale_before logic is broken")
		}
	}

	// Pod B with stale_before in the future: A's claim now looks stale,
	// B takes over.
	rowsB2, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    time.Now().UTC().Add(time.Second),
		ClaimedBy:      "pod-B",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("pod B stale-takeover claim: %v", err)
	}
	found := false
	for _, r := range rowsB2 {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("pod B failed to adopt pod A's stale claim; failover is broken")
	}
}

// TestClaim_SamePodReacquires: a pod must re-acquire its own claim
// every tick regardless of stale_before, otherwise a healthy pod whose
// tick straddles the stale boundary would self-evict.
func TestClaim_SamePodReacquires(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_reacquire")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "tool.call",
		Payload:    map[string]any{"name": "Bash"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)

	if _, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-A",
		Limit:          32,
	}); err != nil {
		t.Fatalf("pod A tick 1: %v", err)
	}

	// stale_before set to make own claim look stale: @claimed_by branch
	// must let pod A take its own row back.
	rowsA2, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    time.Now().UTC().Add(time.Second),
		ClaimedBy:      "pod-A",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("pod A tick 2: %v", err)
	}
	found := false
	for _, r := range rowsA2 {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("pod A self-evicted its own claim across ticks; the @claimed_by branch is broken")
	}
}

// TestClaim_RunStartedTriggersDriver: run.started must wake the driver
// so it can send a placeholder card and lock in the message_id. The
// placeholder send is how we prevent the double-send race.
func TestClaim_RunStartedTriggersDriver(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_runstarted")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.started",
		Payload:    map[string]any{"source": "conversation_stream"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.started: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-runstarted-test",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("run with only run.started event did NOT trigger the driver; the placeholder-card mechanism relies on this — see ClaimActiveFeishuInflightConversations event_kind filter")
	}
}

// TestClaim_MessageThinkingTriggersDriver: thinking events ride their
// own event_kind (no longer miscast as message.delta) and must still
// wake the driver so the inflight card patches as reasoning lands.
func TestClaim_MessageThinkingTriggersDriver(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_thinking")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "message.thinking",
		Payload:    map[string]any{"thinking": "The user said X. I will Y."},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record message.thinking: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-thinking-test",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("run with only message.thinking event did NOT trigger the driver; the SQL filter must include message.thinking so the Done card's Thinking panel can patch")
	}
}

// TestClaim_RunCompletedTriggersDriver: when seq_emitted has caught up
// to the last user-visible event but run.completed lands afterwards,
// the driver MUST still be woken so it can PATCH the working card into
// its Done state.
func TestClaim_RunCompletedTriggersDriver(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_runcompleted")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.completed",
		Payload:    map[string]any{"source": "conversation_stream"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.completed: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-runcompleted-test",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("run with only run.completed event did NOT trigger the driver; the terminal Done patch relies on this — see ClaimActiveFeishuInflightConversations event_kind filter")
	}
}

// TestClaim_RunFailedTriggersDriver: failure-path mirror of
// RunCompletedTriggersDriver. The driver consumes the run.failed
// payload via foldEventsIntoCardState and renders the Error card from
// payload.user_visible_message.
func TestClaim_RunFailedTriggersDriver(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_runfailed")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.failed",
		Payload:    map[string]any{"user_visible_message": "upstream agent died"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.failed: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-runfailed-test",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("run with only run.failed event did NOT trigger the driver; the terminal Error patch relies on this")
	}
}

// TestClaim_TerminalLifecycleStaysFiltered: run.cancelled, run.requeued,
// run.superseded go through runtime's status-flip + ClearSlot path and
// must NOT wake the driver. (run.completed and run.failed DO trigger —
// see the two tests above — so they can PATCH the final card.)
func TestClaim_TerminalLifecycleStaysFiltered(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_terminal_lc")
	for _, kind := range []string{"run.cancelled", "run.requeued", "run.superseded"} {
		if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
			RunID:      runID,
			EventKind:  kind,
			Payload:    map[string]any{"source": "test"},
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("record %s: %v", kind, err)
		}
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-terminal-lc-test",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, r := range rows {
		if r.AgentRunID == runID {
			t.Error("run with only non-terminal lifecycle events (run.cancelled/requeued/superseded) triggered the driver; these must stay excluded from the event_kind filter")
		}
	}
}

// TestClaim_FinishedRunWithoutTerminalDeliveryStaysClaimable: a run
// that completed before any working card landed must be claimable so
// the driver can send the Done card. Once
// MarkGatewayOutboundDelivered stamps gateway_delivered_at the very
// next claim must NOT re-pick this run — otherwise the driver re-sends
// the Done card every tick.
func TestClaim_FinishedRunWithoutTerminalDeliveryStaysClaimable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	convID, runID := seedFeishuRunForClaim(t, store, "oc_claim_terminal_idempotent")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "message.delta",
		Payload:    map[string]any{"delta": "Hi! What would you like to work on?"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record message.delta: %v", err)
	}

	// CompleteAgentRun stamps r.status=completed + r.finished_at and
	// writes output_message_id pointing at a fresh messages row (with
	// no gateway_delivered_at yet).
	completion, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{
		RunID:   runID,
		Source:  "runtime",
		Content: "Hi! What would you like to work on?",
	})
	if err != nil {
		t.Fatalf("complete run: %v", err)
	}
	if completion.MessageID == "" {
		t.Fatal("CompleteAgentRun returned empty output message id; can't test terminal-delivery filter")
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)
	stale := now.Add(-30 * time.Second)

	// First tick: run finished but output message not yet delivered.
	rowsTick1, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-terminal-idempotent",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	found1 := false
	for _, r := range rowsTick1 {
		if r.AgentRunID == runID {
			found1 = true
			if r.OutputMessageID != completion.MessageID {
				t.Errorf("first claim returned output_message_id=%q, want %q", r.OutputMessageID, completion.MessageID)
			}
			break
		}
	}
	if !found1 {
		t.Fatal("finished run with un-delivered output message was NOT claimed on first tick; driver can't send the Done card")
	}

	// Stamp gateway_delivered_at. ClearSlot is deliberately skipped —
	// the delivery marker, not the metadata slot, is the idempotency
	// key the second tick must respect.
	if _, err := store.MarkGatewayOutboundDelivered(ctx, MarkGatewayOutboundDeliveredInput{
		MessageID:  completion.MessageID,
		DeliveryID: "om_terminal_test_delivery",
	}); err != nil {
		t.Fatalf("mark delivered: %v", err)
	}

	// Second tick: only the delivery marker changed. The
	// gateway_delivered_at filter must keep the driver from re-picking.
	rowsTick2, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-terminal-idempotent",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, r := range rowsTick2 {
		if r.AgentRunID == runID {
			t.Fatalf("finished + delivered run was RE-CLAIMED on second tick; the terminal-idempotency filter on gateway_delivered_at is broken. Without it the driver re-sends the Done card every tick.")
		}
	}

	// convID isn't asserted on but kept for traceability in failures.
	_ = convID
}

// TestClaim_RunningRunNotFilteredByTerminalDelivery: the terminal-
// delivery filter MUST NOT short-circuit a still-running run.
// Short-circuited by r.finished_at IS NULL before the m. check.
func TestClaim_RunningRunNotFilteredByTerminalDelivery(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_running_no_filter")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "tool.call",
		Payload:    map[string]any{"name": "Bash", "args": map[string]any{"command": "echo running"}},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record event: %v", err)
	}
	// Do NOT finalize. r.finished_at stays NULL; the claim query must
	// keep returning it regardless of unrelated message rows carrying
	// gateway_delivered_at.

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-running-no-filter",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Error("running run was NOT claimed; the gateway_delivered_at filter must NOT apply while r.finished_at IS NULL")
	}
}

// TestClaim_FailedRunWithoutOutputMessageStopsAfterFingerprint pins
// the per-run fingerprint at
// conversations.metadata.gateway_inflight.terminal_delivered.run_id:
// when a run hit FailAgentRun before producing an output message,
// agent_runs.output_message_id stays NULL so the LEFT JOIN's
// gateway_delivered_at check is permanently empty. The fingerprint is
// the only thing that prevents the claim CTE from re-picking the row
// and re-sending an error card every tick.
func TestClaim_FailedRunWithoutOutputMessageStopsAfterFingerprint(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	_, runID := seedFeishuRunForClaim(t, store, "oc_claim_failed_no_output")

	// One run.failed event so max_event_sequence > 0; mirrors
	// FailAgentRun callers recording the lifecycle event after status flip.
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.failed",
		Payload:    map[string]any{"error": "capability_credential_missing"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.failed: %v", err)
	}

	// FailAgentRun (no CompleteAgentRun) → r.status='failed',
	// r.finished_at NOT NULL, r.output_message_id IS NULL.
	if err := store.FailAgentRun(ctx, FailAgentRunInput{
		RunID:  runID,
		Source: "runtime",
		Reason: "capability_credential_missing",
	}); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)
	stale := now.Add(-30 * time.Second)

	// First tick: finished run, no output message, no fingerprint
	// → driver must claim once to deliver the terminal card.
	rowsTick1, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-failed-no-output",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	var picked FeishuInflightConversation
	found := false
	for _, r := range rowsTick1 {
		if r.AgentRunID == runID {
			picked = r
			found = true
			break
		}
	}
	if !found {
		t.Fatal("failed run with NULL output_message_id was NOT claimed on first tick; driver can never send the terminal card")
	}
	if picked.OutputMessageID != "" {
		t.Errorf("first claim returned OutputMessageID=%q; want empty (FailAgentRun never sets it)", picked.OutputMessageID)
	}

	// Write the per-run fingerprint; MarkGatewayOutboundDelivered
	// no-ops when OutputMessageID is empty, so the fingerprint is what
	// closes the gate in this branch.
	if err := store.MarkConversationInflightTerminalDelivered(ctx, picked.ConversationID, runID); err != nil {
		t.Fatalf("mark fingerprint: %v", err)
	}

	// Second tick: only the fingerprint changed. The terminal_delivered
	// filter must keep the driver from re-picking.
	rowsTick2, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-failed-no-output",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, r := range rowsTick2 {
		if r.AgentRunID == runID {
			t.Fatalf("failed run with terminal fingerprint was RE-CLAIMED on second tick; the per-run idempotency filter is broken. Without it the chat sees the same red 'Agent 执行失败' card every tick.")
		}
	}
}

// TestClaim_NewRunNotBlockedByPreviousRunsWorkingSlot: when the
// conversation's working slot still carries a previous run's
// agent_run_id + high seq_emitted, the claim must still pick the
// current run so the driver can send a fresh card (was the
// 2026-06-18 sharedbot regression where "second question in same
// 话题" got no card until the previous run's slot cleared).
func TestClaim_NewRunNotBlockedByPreviousRunsWorkingSlot(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	convID, runID := seedFeishuRunForClaim(t, store, "oc_claim_cross_run")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.started",
		Payload:    map[string]any{},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.started: %v", err)
	}

	// Plant a stale working slot that belongs to a DIFFERENT (fake) run
	// with a high seq_emitted. The old claim filter would gate on
	// seq_emitted < max_seq and refuse to claim because the run has only
	// one event vs slot's reported 99 — the fix's "slot.agent_run_id !=
	// r.id" branch must short-circuit that comparison.
	if _, err := store.UpsertConversationInflightWorkingCard(ctx, UpsertConversationInflightWorkingCardInput{
		ConversationID: convID,
		Slot: WorkingInflightSlot{
			ExternalMsgID:  "om_stale_from_prev_run",
			AgentRunID:     "00000000-0000-0000-0000-000000000001",
			ExternalChatID: "oc_claim_cross_run",
			SeqEmitted:     99,
			UpdatedAt:      time.Now().UTC(),
		},
		ExpectedOldRunID: "",
	}); err != nil {
		t.Fatalf("plant stale slot: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-cross-run",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.AgentRunID == runID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("run %s was NOT claimed even though slot belongs to a different run; the cross-run gate is broken — new questions in a thread will never get their own card.", runID)
	}
}

// TestClearConversationInflightSlot_RunIDGuard: a non-empty
// expectedAgentRunID must skip the wipe when the slot belongs to a
// different run — prevents a late terminal of run A from clearing
// run B's freshly-written slot.
func TestClearConversationInflightSlot_RunIDGuard(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	convID, _ := seedFeishuRunForClaim(t, store, "oc_clear_guard")
	if _, err := store.UpsertConversationInflightWorkingCard(ctx, UpsertConversationInflightWorkingCardInput{
		ConversationID: convID,
		Slot: WorkingInflightSlot{
			ExternalMsgID:  "om_clear_guard_card",
			AgentRunID:     "run-current",
			ExternalChatID: "oc_clear_guard",
			SeqEmitted:     5,
			UpdatedAt:      time.Now().UTC(),
		},
		ExpectedOldRunID: "",
	}); err != nil {
		t.Fatalf("plant slot: %v", err)
	}

	// Mismatching run_id must be a no-op.
	if err := store.ClearConversationInflightSlot(ctx, convID, InflightSlotWorking, "run-stale"); err != nil {
		t.Fatalf("clear with wrong run_id: %v", err)
	}
	cards, err := store.GetConversationInflightCards(ctx, convID)
	if err != nil {
		t.Fatalf("get cards: %v", err)
	}
	if cards.Working.ExternalMsgID != "om_clear_guard_card" {
		t.Fatalf("slot wiped by mismatching run_id guard: got %q, want om_clear_guard_card", cards.Working.ExternalMsgID)
	}

	// Matching run_id wipes.
	if err := store.ClearConversationInflightSlot(ctx, convID, InflightSlotWorking, "run-current"); err != nil {
		t.Fatalf("clear with right run_id: %v", err)
	}
	cards, err = store.GetConversationInflightCards(ctx, convID)
	if err != nil {
		t.Fatalf("get cards after wipe: %v", err)
	}
	if cards.Working.ExternalMsgID != "" {
		t.Fatalf("slot not wiped on matching run_id: still %q", cards.Working.ExternalMsgID)
	}

	// Empty run_id wipes unconditionally (dead-letter path).
	if _, err := store.UpsertConversationInflightWorkingCard(ctx, UpsertConversationInflightWorkingCardInput{
		ConversationID: convID,
		Slot: WorkingInflightSlot{
			ExternalMsgID:  "om_clear_guard_v2",
			AgentRunID:     "run-second",
			ExternalChatID: "oc_clear_guard",
			SeqEmitted:     1,
			UpdatedAt:      time.Now().UTC(),
		},
		ExpectedOldRunID: "",
	}); err != nil {
		t.Fatalf("re-plant slot: %v", err)
	}
	if err := store.ClearConversationInflightSlot(ctx, convID, InflightSlotWorking, ""); err != nil {
		t.Fatalf("clear with empty run_id: %v", err)
	}
	cards, err = store.GetConversationInflightCards(ctx, convID)
	if err != nil {
		t.Fatalf("get cards after empty-guard wipe: %v", err)
	}
	if cards.Working.ExternalMsgID != "" {
		t.Fatalf("empty-guard wipe didn't clear; still %q", cards.Working.ExternalMsgID)
	}
}

// TestClaim_MultiFailedRunsBothExcludedAfterFingerprint: two failed
// runs in one conv (prod 2026-06-22 storm). Old single-value
// terminal_delivered.run_id overwrote on the second stamp and the
// claim re-picked the first run. The set must keep both marked.
func TestClaim_MultiFailedRunsBothExcludedAfterFingerprint(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	convID, runA := seedFeishuRunForClaim(t, store, "oc_multi_failed_runs")
	ids := DefaultDevFixtureIDs()

	sendRes, err := store.SendUserMessageToConversation(ctx, SendUserMessageToConversationInput{
		ConversationID:    convID,
		UserID:            ids.UserID,
		Content:           "@产品Agent claim-test-second",
		MentionedAgentIDs: []string{ids.ProductAgentID},
	})
	if err != nil {
		t.Fatalf("send second user msg: %v", err)
	}
	if len(sendRes.RunIDs) == 0 {
		t.Fatal("no run id from second send")
	}
	runB := sendRes.RunIDs[0]

	for _, rid := range []string{runA, runB} {
		if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
			RunID:      rid,
			EventKind:  "run.failed",
			Payload:    map[string]any{"error": "connection refused"},
			OccurredAt: time.Now().UTC(),
		}); err != nil {
			t.Fatalf("record run.failed for %s: %v", rid, err)
		}
		if err := store.FailAgentRun(ctx, FailAgentRunInput{
			RunID:  rid,
			Source: "runtime",
			Reason: "connection refused",
		}); err != nil {
			t.Fatalf("fail run %s: %v", rid, err)
		}
	}

	now := time.Now().UTC()
	cutoff := now.Add(-5 * time.Minute)
	stale := now.Add(-30 * time.Second)

	tick1, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-multi-failed",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	got := map[string]bool{}
	for _, r := range tick1 {
		got[r.AgentRunID] = true
	}
	if !got[runA] || !got[runB] {
		t.Fatalf("first claim missed runs: runA=%v runB=%v rows=%d", got[runA], got[runB], len(tick1))
	}

	if err := store.MarkConversationInflightTerminalDelivered(ctx, convID, runA); err != nil {
		t.Fatalf("mark fingerprint for runA: %v", err)
	}
	if err := store.MarkConversationInflightTerminalDelivered(ctx, convID, runB); err != nil {
		t.Fatalf("mark fingerprint for runB: %v", err)
	}

	tick2, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: cutoff,
		StaleBefore:    stale,
		ClaimedBy:      "pod-multi-failed",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	for _, r := range tick2 {
		if r.AgentRunID == runA {
			t.Fatalf("runA re-claimed after stamp — second run's stamp overwrote (prod 2026-06-22 regression)")
		}
		if r.AgentRunID == runB {
			t.Fatalf("runB re-claimed after stamp")
		}
	}

	// Re-stamping must stay idempotent (upsert strips before append).
	if err := store.MarkConversationInflightTerminalDelivered(ctx, convID, runA); err != nil {
		t.Fatalf("re-mark fingerprint for runA: %v", err)
	}
}

// TestClaim_LegacyRunIDFingerprintStillRespected: rows stamped by an
// older binary use the single-value run_id shape. Claim filter must
// still skip via the OR branch.
func TestClaim_LegacyRunIDFingerprintStillRespected(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)

	convID, runID := seedFeishuRunForClaim(t, store, "oc_legacy_runid")
	if err := store.RecordAgentRunEvent(ctx, RecordAgentRunEventInput{
		RunID:      runID,
		EventKind:  "run.failed",
		Payload:    map[string]any{"error": "legacy"},
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("record run.failed: %v", err)
	}
	if err := store.FailAgentRun(ctx, FailAgentRunInput{
		RunID:  runID,
		Source: "runtime",
		Reason: "legacy",
	}); err != nil {
		t.Fatalf("fail run: %v", err)
	}

	if _, err := db.Exec(ctx, `
		update conversations
		set metadata = coalesce(metadata, '{}'::jsonb) || jsonb_build_object(
			'gateway_inflight',
			coalesce(metadata->'gateway_inflight', '{}'::jsonb) || jsonb_build_object(
				'terminal_delivered', jsonb_build_object(
					'run_id', $1::text,
					'delivered_at', to_jsonb($2::timestamptz)
				)
			)
		)
		where id = $3::uuid
	`, runID, time.Now().UTC(), convID); err != nil {
		t.Fatalf("plant legacy fingerprint: %v", err)
	}

	now := time.Now().UTC()
	rows, err := store.ClaimActiveFeishuInflightConversations(ctx, ClaimActiveFeishuInflightConversationsInput{
		FinishedCutoff: now.Add(-5 * time.Minute),
		StaleBefore:    now.Add(-30 * time.Second),
		ClaimedBy:      "pod-legacy",
		Limit:          32,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	for _, r := range rows {
		if r.AgentRunID == runID {
			t.Fatalf("legacy run_id fingerprint not respected: row re-claimed")
		}
	}
}
