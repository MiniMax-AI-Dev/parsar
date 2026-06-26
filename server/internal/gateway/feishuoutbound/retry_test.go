package feishuoutbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// flakyUpstream returns an httptest.Server whose POST /messages and
// PATCH /messages/{id} both return HTTP 502 every time. tenant_access_token
// always succeeds so the driver gets past credential resolution. fails
// counts every 5xx returned so a test can assert exactly how many
// retry attempts the driver issued before persisting state / dead-
// lettering.
func flakyUpstream(t *testing.T) (server *httptest.Server, fails *atomic.Int32) {
	t.Helper()
	fails = &atomic.Int32{}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			fails.Add(1)
			http.Error(w, `{"code":99999,"msg":"upstream exploded"}`, http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, fails
}

// happyConv builds the canonical conversation row the retry tests
// share — single mid-run conversation with an existing slot whose
// SeqEmitted is one behind MaxEventSequence, so the driver enters
// the "patch existing card" branch on every tick.
func happyConvForRetry(seqEmitted int64, attempts int, lastErr string, nextRetry time.Time) store.FeishuInflightConversation {
	working := map[string]any{
		"external_msg_id":  "om_existing_card",
		"app_id":           "cli_drv",
		"external_chat_id": "oc_drv_chat",
		"agent_run_id":     "run-retry",
		"seq_emitted":      float64(seqEmitted),
	}
	if attempts > 0 {
		working["attempts"] = float64(attempts)
		working["last_error"] = lastErr
	}
	if !nextRetry.IsZero() {
		working["next_retry_at"] = nextRetry.UTC().Format(time.RFC3339Nano)
	}
	return store.FeishuInflightConversation{
		ConversationID: "conv-retry",
		WorkspaceID:    "ws-1",
		ExternalChatID: "oc_drv_chat",
		SourceAppID:    "cli_drv",
		ConversationMetadata: map[string]any{
			"gateway_inflight": map[string]any{
				"working": working,
			},
		},
		AgentRunID:       "run-retry",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-3 * time.Second).UTC(),
		MaxEventSequence: seqEmitted + 1,
	}
}

// TestRetry_TransientFailureBumpsAttempts verifies that a single 502
// from Feishu causes the driver to persist Attempts=1, LastError,
// and a future NextRetryAt onto the working slot — and that the tick
// returns nil (caller treats it as a soft outcome, not a hard error).
func TestRetry_TransientFailureBumpsAttempts(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	upstream, fails := flakyUpstream(t)

	fs.inflightConvs = []store.FeishuInflightConversation{happyConvForRetry(2, 0, "", time.Time{})}
	fs.inflightEvents["run-retry"] = []store.AgentRunEvent{{
		Sequence:  3,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before"},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("InflightTickOnce returned err %v, want nil (soft retry outcome)", err)
	}

	if got := fails.Load(); got != 1 {
		t.Errorf("upstream calls = %d, want 1 (single PATCH attempt before persisting state)", got)
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("Upserts = %d, want 1 (retry state persist)", len(fs.inflightUpserts))
	}
	got := fs.inflightUpserts[0].Slot
	if got.Attempts != 1 {
		t.Errorf("Slot.Attempts = %d, want 1", got.Attempts)
	}
	if !strings.Contains(got.LastError, "patch working card") {
		t.Errorf("Slot.LastError = %q, want it to mention the stage prefix", got.LastError)
	}
	if got.NextRetryAt.IsZero() {
		t.Error("Slot.NextRetryAt is zero; want a future timestamp from the backoff schedule")
	}
	if got.NextRetryAt.Before(time.Now().UTC()) {
		t.Errorf("Slot.NextRetryAt = %v, want in the future", got.NextRetryAt)
	}
	// First-attempt backoff is 1s; allow a small skew for test wall-clock.
	wantWindow := time.Now().UTC().Add(driverBackoffSchedule[0])
	if got.NextRetryAt.After(wantWindow.Add(2 * time.Second)) {
		t.Errorf("Slot.NextRetryAt = %v, want ~%v (1s after now)", got.NextRetryAt, wantWindow)
	}
	if got.ExternalMsgID != "om_existing_card" {
		t.Errorf("Slot.ExternalMsgID = %q, want om_existing_card preserved across retry", got.ExternalMsgID)
	}
	// No dead-letter notice on the first failure.
	if len(fs.systemNotices) != 0 {
		t.Errorf("systemNotices = %d, want 0 on first transient failure", len(fs.systemNotices))
	}
}

// TestRetry_SuccessClearsAttempts verifies that a tick which lands a
// successful PATCH against a slot that previously had Attempts > 0
// resets the retry counters back to zero in the persisted slot.
func TestRetry_SuccessClearsAttempts(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	// Seed an existing slot that already accumulated 2 failed
	// attempts; the next tick succeeds (rec returns 200) so the
	// counters should zero out.
	fs.inflightConvs = []store.FeishuInflightConversation{
		happyConvForRetry(2, 2, "patch working card: feishu 502", time.Now().Add(-1*time.Second).UTC()),
	}
	fs.inflightEvents["run-retry"] = []store.AgentRunEvent{{
		Sequence:  3,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Read", "stage": "before"},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("InflightTickOnce: %v", err)
	}

	if rec.patches.Load() != 1 {
		t.Errorf("patches = %d, want 1 (successful PATCH after recovery)", rec.patches.Load())
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("Upserts = %d, want 1", len(fs.inflightUpserts))
	}
	got := fs.inflightUpserts[0].Slot
	if got.Attempts != 0 {
		t.Errorf("Slot.Attempts = %d, want 0 (zeroed by zeroRetryWorking on success)", got.Attempts)
	}
	if got.LastError != "" {
		t.Errorf("Slot.LastError = %q, want \"\"", got.LastError)
	}
	if !got.NextRetryAt.IsZero() {
		t.Errorf("Slot.NextRetryAt = %v, want zero", got.NextRetryAt)
	}
}

// TestRetry_DeadLetterAfter5Failures forces the driver through the
// full backoff schedule by seeding Attempts=4 (one failure away from
// the budget) and observing that the 5th transient failure:
//
//   - writes Attempts=5 into the slot,
//   - emits one SendSystemNoticeMessage with the dead-letter Kind,
//   - clears the working inflight slot,
//   - keeps the tick returning nil (not a hard error).
//
// Subsequent ticks (out of scope here) wouldn't re-trigger because
// ClearSlot wiped the seq_emitted gate.
func TestRetry_DeadLetterAfter5Failures(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	upstream, _ := flakyUpstream(t)

	// Attempts=4 means this tick's bump → 5 → dead-letter.
	fs.inflightConvs = []store.FeishuInflightConversation{
		happyConvForRetry(2, 4, "patch working card: feishu 502", time.Now().Add(-1*time.Second).UTC()),
	}
	fs.inflightEvents["run-retry"] = []store.AgentRunEvent{{
		Sequence:  3,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Edit", "stage": "before"},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("InflightTickOnce returned err %v, want nil (dead-letter is a soft outcome)", err)
	}

	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("Upserts = %d, want 1 (retry state persist before dead-letter)", len(fs.inflightUpserts))
	}
	if got := fs.inflightUpserts[0].Slot.Attempts; got != 5 {
		t.Errorf("Slot.Attempts = %d, want 5", got)
	}

	if len(fs.systemNotices) != 1 {
		t.Fatalf("systemNotices = %d, want 1 (one dead-letter notice)", len(fs.systemNotices))
	}
	notice := fs.systemNotices[0]
	// Kind carries the per-run discriminator (slot + run_id) so a
	// later run in the same conversation can still write its own
	// notice — the store helper dedups on (conversation_id, kind).
	if !strings.HasPrefix(notice.Kind, "feishu_outbound_dead_letter_working_") {
		t.Errorf("notice.Kind = %q, want prefix feishu_outbound_dead_letter_working_", notice.Kind)
	}
	if !strings.HasSuffix(notice.Kind, "run-retry") {
		t.Errorf("notice.Kind = %q, want suffix run-retry (the agent_run_id discriminator)", notice.Kind)
	}
	if notice.SourceRunID != "run-retry" {
		t.Errorf("notice.SourceRunID = %q, want run-retry", notice.SourceRunID)
	}
	if !strings.Contains(notice.Content, "5 attempts") {
		t.Errorf("notice.Content = %q, want it to surface the attempt count", notice.Content)
	}
	if notice.ConversationID != "conv-retry" {
		t.Errorf("notice.ConversationID = %q, want conv-retry", notice.ConversationID)
	}

	if len(fs.inflightClears) != 1 {
		t.Fatalf("clears = %d, want 1 (always-clear after dead-letter)", len(fs.inflightClears))
	}
	if fs.inflightClears[0].Slot != store.InflightSlotWorking {
		t.Errorf("clear slot = %q, want %q", fs.inflightClears[0].Slot, store.InflightSlotWorking)
	}
}

// TestRetry_PersistFailureSurfaces verifies the narrow but important
// branch where the Upsert that persists the retry counter itself
// fails (e.g. PG unreachable). The tick MUST return the persist error
// so the higher-level loop logs it and retries — silently swallowing
// it would lose the failure counter and let the driver re-PATCH
// forever.
func TestRetry_PersistFailureSurfaces(t *testing.T) {
	t.Parallel()
	fs := &fakeStoreWithUpsertErr{
		fakeStore: newFakeStore(),
		upsertErr: errors.New("postgres: connection refused"),
	}
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	upstream, _ := flakyUpstream(t)

	fs.inflightConvs = []store.FeishuInflightConversation{happyConvForRetry(2, 0, "", time.Time{})}
	fs.inflightEvents["run-retry"] = []store.AgentRunEvent{{
		Sequence:  3,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before"},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL})
	// InflightTickOnce surfaces per-conversation errors via the
	// tickProcessedCounter pattern: failed conversations log Warn
	// but the top-level call returns nil unless something at the
	// LIST/CLAIM layer broke. Verify directly via
	// handleInflightConversation.
	err := worker.handleInflightConversation(context.Background(), fs.inflightConvs[0])
	if err == nil {
		t.Fatalf("handleInflightConversation = nil err, want persist error propagated")
	}
	if !strings.Contains(err.Error(), "persist retry state") {
		t.Errorf("err = %v, want to mention 'persist retry state'", err)
	}
}

// fakeStoreWithUpsertErr lets a single test force
// UpsertConversationInflightWorkingCard to fail. We embed the regular
// fakeStore so the rest of the Storer surface is unaffected.
type fakeStoreWithUpsertErr struct {
	*fakeStore
	upsertErr error
}

func (f *fakeStoreWithUpsertErr) UpsertConversationInflightWorkingCard(
	ctx context.Context,
	input store.UpsertConversationInflightWorkingCardInput,
) (store.WorkingInflightSlot, error) {
	return store.WorkingInflightSlot{}, f.upsertErr
}

// TestNextRetryAfter_BackoffShape pins the exact schedule the driver
// applies. Treats it as a contract — operators reading
// docs/feishu-driver-retry.md should match.
func TestNextRetryAfter_BackoffShape(t *testing.T) {
	base := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	want := map[int]time.Duration{
		1: 1 * time.Second,
		2: 5 * time.Second,
		3: 30 * time.Second,
		4: 5 * time.Minute,
		5: 5 * time.Minute,
	}
	for attempt, expected := range want {
		got := nextRetryAfter(base, attempt)
		if got.Sub(base) != expected {
			t.Errorf("attempt %d: nextRetryAfter delta = %v, want %v", attempt, got.Sub(base), expected)
		}
	}
	// Attempts past the schedule clamp to the last entry — defensive
	// against off-by-one bugs in the dead-letter dispatcher.
	if got := nextRetryAfter(base, 6); got.Sub(base) != 5*time.Minute {
		t.Errorf("clamp: nextRetryAfter delta = %v, want 5m", got.Sub(base))
	}
}

// TestDeadLetterKind_PerRunDiscriminator pins the dedup key contract:
// SendSystemNoticeMessage dedups on (conversation_id, kind), so the
// driver MUST vary kind by run_id to avoid swallowing notices from
// later failed runs in the same conversation.
func TestDeadLetterKind_PerRunDiscriminator(t *testing.T) {
	if got := deadLetterKind("working", "run-1"); got != "feishu_outbound_dead_letter_working_run-1" {
		t.Errorf("working/run-1 kind = %q", got)
	}
	if got := deadLetterKind("permission", "run-2"); got != "feishu_outbound_dead_letter_permission_run-2" {
		t.Errorf("permission/run-2 kind = %q", got)
	}
	// Two different runs in the same conversation must produce
	// distinct dedup keys.
	a := deadLetterKind("working", "run-A")
	b := deadLetterKind("working", "run-B")
	if a == b {
		t.Errorf("same-slot, different-run kinds collided: %q vs %q", a, b)
	}
	// Empty run id falls back to slot-level key (defensive — the
	// driver should always carry a run id by the time it dead-letters).
	if got := deadLetterKind("working", ""); got != "feishu_outbound_dead_letter_working" {
		t.Errorf("empty run kind = %q, want feishu_outbound_dead_letter_working fallback", got)
	}
}
