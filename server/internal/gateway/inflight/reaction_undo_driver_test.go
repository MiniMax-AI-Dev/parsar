package inflight

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

// Reaction-undo edge-case coverage for the driver's async DELETE
// path. The lifecycle test in inflight_driver_test.go covers the
// happy path; this file covers the 3 branches the (deleted) P1-era
// reaction_undo_test.go used to pin: orphan row (missing reaction id
// or external_message_id), nothing-to-undo (no reaction-bearing
// inbound at all), and a 404 from Feishu that should still clear the
// local reaction metadata so the next terminal in the same
// conversation doesn't hammer a dead id forever.

// reactionUndoFixture drives a single async-clear-reaction call so
// each test can stay focused on one branch.
type reactionUndoFixture struct {
	fs       *fakeStore
	worker   *Worker
	upstream *httptest.Server

	deleteCalls  atomic.Int32
	deleteStatus atomic.Int32 // when >0, /reactions DELETE replies with that status code instead of 200
	// lastDeletedURL captures the most recent DELETE request path so a
	// test can assert WHICH inbound's reaction was undone, not just
	// that some undo fired. Path shape:
	// /open-apis/im/v1/messages/{external_message_id}/reactions/{reaction_id}
	lastDeletedURL atomic.Value // string
}

func newReactionUndoFixture(t *testing.T) *reactionUndoFixture {
	t.Helper()
	rf := &reactionUndoFixture{}
	rf.upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-undo","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions/") && r.Method == http.MethodDelete:
			rf.deleteCalls.Add(1)
			rf.lastDeletedURL.Store(r.URL.Path)
			if code := rf.deleteStatus.Load(); code > 0 {
				w.WriteHeader(int(code))
				_, _ = io.WriteString(w, `{"code":1,"msg":"reaction not found"}`)
				return
			}
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rf.upstream.Close)

	rf.fs = newFakeStore()
	rf.fs.agents["cli_react"] = happyAgentWithAppID("cli_react")
	rf.fs.secrets["secret_happy"] = happySecret()

	worker, err := NewWorker(Options{Store: rf.fs, Secrets: fakeDecrypter{}, BaseURL: rf.upstream.URL})
	if err != nil {
		t.Fatal(err)
	}
	rf.worker = worker
	return rf
}

func (rf *reactionUndoFixture) waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// TestAsyncClearTypingReaction_NothingToUndo: no reaction-bearing
// inbound row exists for the conversation. The driver must NOT call
// Feishu DELETE, NOT call ClearReaction, and NOT log an error — this
// is a normal case (command echoes, fresh chats, AddReaction earlier
// failed). Silent no-op.
func TestAsyncClearTypingReaction_NothingToUndo(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)
	// No row seeded into reactionsByConv → FindLatestFeishuInboundReactionByConversation
	// returns ErrUnknownMessage.

	rf.worker.asyncClearTypingReaction("conv-empty", "ws-1", "cli_react", "")

	// Give the goroutine a beat to fire (or not).
	time.Sleep(80 * time.Millisecond)

	if rf.deleteCalls.Load() != 0 {
		t.Errorf("DELETE called %d times, want 0 (nothing-to-undo)", rf.deleteCalls.Load())
	}
	if len(rf.fs.reactionClears) != 0 {
		t.Errorf("ClearReaction called %d times, want 0 (nothing-to-undo)", len(rf.fs.reactionClears))
	}
}

// TestAsyncClearTypingReaction_OrphanRowMissingReactionID: the
// inbound row matched but the reaction subtree is empty (reaction_id
// or external_message_id is ”). The driver must skip the HTTP call
// but still ClearReaction to clean up the orphan metadata stub —
// otherwise every future terminal would keep matching the same row.
func TestAsyncClearTypingReaction_OrphanRowMissingReactionID(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)
	rf.fs.reactionsByConv["conv-orphan"] = store.FeishuInboundReactionRow{
		MessageID:         "inbound-orphan",
		WorkspaceID:       "ws-1",
		ExternalMessageID: "", // <- orphan: webhook landed but external id was lost
		ReactionID:        "r-typing",
		AppID:             "cli_react",
	}

	rf.worker.asyncClearTypingReaction("conv-orphan", "ws-1", "cli_react", "")

	if !rf.waitFor(2*time.Second, func() bool { return len(rf.fs.reactionClears) >= 1 }) {
		t.Fatalf("ClearReaction never fired; reactionClears=%v", rf.fs.reactionClears)
	}
	if rf.deleteCalls.Load() != 0 {
		t.Errorf("DELETE called %d times, want 0 on orphan row (skip HTTP)", rf.deleteCalls.Load())
	}
	if got := rf.fs.reactionClears[0]; got != "inbound-orphan" {
		t.Errorf("ClearReaction called for %q, want inbound-orphan", got)
	}
}

// TestAsyncClearTypingReaction_FeishuReturns404StillClears: Feishu
// returns a 4xx (reaction already gone). The driver must STILL clear
// the local metadata so the next terminal in the same conversation
// doesn't keep retrying the same dead id. Failing to clear here would
// have us hammer Feishu with the same 404 every time the user got a
// new agent message.
func TestAsyncClearTypingReaction_FeishuReturns404StillClears(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)
	rf.deleteStatus.Store(404)
	rf.fs.reactionsByConv["conv-stale"] = store.FeishuInboundReactionRow{
		MessageID:         "inbound-stale",
		WorkspaceID:       "ws-1",
		ExternalMessageID: "om_user_typed",
		ReactionID:        "r-already-gone",
		AppID:             "cli_react",
	}

	rf.worker.asyncClearTypingReaction("conv-stale", "ws-1", "cli_react", "")

	if !rf.waitFor(2*time.Second, func() bool {
		return rf.deleteCalls.Load() >= 1 && len(rf.fs.reactionClears) >= 1
	}) {
		t.Fatalf("expected DELETE attempt + clear; deleteCalls=%d reactionClears=%v",
			rf.deleteCalls.Load(), rf.fs.reactionClears)
	}
	if rf.deleteCalls.Load() != 1 {
		t.Errorf("DELETE called %d times, want 1", rf.deleteCalls.Load())
	}
	if got := rf.fs.reactionClears[0]; got != "inbound-stale" {
		t.Errorf("ClearReaction called for %q, want inbound-stale (must clear even on 404)", got)
	}
}

// TestAsyncClearTypingReaction_PerRunPinPreemptsConversationLatest:
// when the test seeds BOTH a conversation-latest row (representing a
// new still-loading inbound the user just typed) AND a per-run row
// (representing the original inbound this run is finishing), the
// driver must clear the per-run one — not the latest. This is the prod
// regression from 2026-06-15 the user reported: a thread reply got
// answered but the typing reaction ended up cleared on a later
// un-replied message.
func TestAsyncClearTypingReaction_PerRunPinPreemptsConversationLatest(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)
	// New inbound, still loading — the user just typed it while the
	// previous run was finishing. Its reaction should NOT be cleared.
	rf.fs.reactionsByConv["conv-race"] = store.FeishuInboundReactionRow{
		MessageID:         "inbound-new-loading",
		WorkspaceID:       "ws-1",
		ExternalMessageID: "om_user_new",
		ReactionID:        "r-new-typing",
		AppID:             "cli_react",
	}
	// Original inbound that triggered the run we're terminating. This
	// is the one whose reaction must be cleared.
	rf.fs.reactionsByAgentRun["run-original"] = store.FeishuInboundReactionRow{
		MessageID:         "inbound-original",
		WorkspaceID:       "ws-1",
		ExternalMessageID: "om_user_original",
		ReactionID:        "r-original-typing",
		AppID:             "cli_react",
	}

	rf.worker.asyncClearTypingReaction("conv-race", "ws-1", "cli_react", "run-original")

	if !rf.waitFor(2*time.Second, func() bool {
		return rf.deleteCalls.Load() >= 1 && len(rf.fs.reactionClears) >= 1
	}) {
		t.Fatalf("DELETE / Clear never fired; deleteCalls=%d reactionClears=%v",
			rf.deleteCalls.Load(), rf.fs.reactionClears)
	}
	if got := rf.fs.reactionClears[0]; got != "inbound-original" {
		t.Errorf("ClearReaction cleared %q, want inbound-original — per-run pin must win over conversation-latest", got)
	}
	deletedURL, _ := rf.lastDeletedURL.Load().(string)
	if !strings.Contains(deletedURL, "om_user_original") {
		t.Errorf("DELETE path = %q, want it to target om_user_original — driver hit the wrong Feishu message", deletedURL)
	}
	if strings.Contains(deletedURL, "om_user_new") {
		t.Errorf("DELETE path = %q, MUST NOT target om_user_new (the still-loading reply)", deletedURL)
	}
}

// TestAsyncClearTypingReaction_EmptyConversationIDNoop: empty
// conversation_id is fed by the dead-letter path on a slot with no
// chat context yet. The driver must short-circuit cleanly — no
// goroutine, no calls, no panic.
func TestAsyncClearTypingReaction_EmptyConversationIDNoop(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)

	rf.worker.asyncClearTypingReaction("   ", "ws-1", "cli_react", "")

	time.Sleep(50 * time.Millisecond)
	if rf.deleteCalls.Load() != 0 || len(rf.fs.reactionClears) != 0 {
		t.Errorf("expected no-op on empty conversation_id; deleteCalls=%d clears=%v",
			rf.deleteCalls.Load(), rf.fs.reactionClears)
	}
}

// TestAsyncClearTypingReaction_StoreLookupFailureSurfaces: a
// non-ErrUnknownMessage lookup error is logged and the goroutine
// returns — DO NOT clear metadata blindly (the inbound might still
// have a valid reaction), DO NOT call DELETE without a valid row.
func TestAsyncClearTypingReaction_StoreLookupFailureSurfaces(t *testing.T) {
	t.Parallel()
	rf := newReactionUndoFixture(t)
	// Wrap fakeStore with a custom Storer that returns a generic error
	// for the lookup.
	wrap := &reactionUndoLookupErrStore{fakeStore: rf.fs, lookupErr: errors.New("pg conn refused")}
	worker, err := NewWorker(Options{Store: wrap, Secrets: fakeDecrypter{}, BaseURL: rf.upstream.URL})
	if err != nil {
		t.Fatal(err)
	}

	worker.asyncClearTypingReaction("conv-pg-down", "ws-1", "cli_react", "")

	time.Sleep(80 * time.Millisecond)
	if rf.deleteCalls.Load() != 0 {
		t.Errorf("DELETE called %d times on lookup failure, want 0", rf.deleteCalls.Load())
	}
	if len(rf.fs.reactionClears) != 0 {
		t.Errorf("ClearReaction fired %d times on lookup failure, want 0", len(rf.fs.reactionClears))
	}
}

// reactionUndoLookupErrStore wraps fakeStore so the reaction lookup
// returns a generic error. Used only by the lookup-failure test.
type reactionUndoLookupErrStore struct {
	*fakeStore
	lookupErr error
}

func (s *reactionUndoLookupErrStore) FindLatestFeishuInboundReactionByConversation(_ context.Context, _ string) (store.FeishuInboundReactionRow, error) {
	return store.FeishuInboundReactionRow{}, s.lookupErr
}

// FindFeishuInboundReactionByAgentRun returns the same lookupErr so the
// per-run path also fails out, ensuring resolveReactionRowForRun falls
// through to the conversation-latest path which then surfaces the
// generic error — matching what the test asserts.
func (s *reactionUndoLookupErrStore) FindFeishuInboundReactionByAgentRun(_ context.Context, _ string) (store.FeishuInboundReactionRow, error) {
	return store.FeishuInboundReactionRow{}, s.lookupErr
}
