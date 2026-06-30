package feishuoutbound

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// upstreamRecorder is the Feishu mock the driver tests share — it
// distinguishes send vs patch vs delete by HTTP method (POST vs
// PATCH vs DELETE on /reactions/...) so a test can assert the right
// sequence of API calls.
//
// sends counts ALL POST /messages calls (interactive cards + plain-
// text at-mention pings). cardSends and textSends partition that
// total by msg_type so tests can assert "the card got sent once AND
// a ping followed" without inspecting bodies. allSends preserves
// every request body in arrival order for tests that need to verify
// the ping content (open_id, copy, anchor).
type upstreamRecorder struct {
	server          *httptest.Server
	sends           atomic.Int32 // POST /messages (total — cards + text pings)
	cardSends       atomic.Int32 // POST /messages with msg_type=interactive
	textSends       atomic.Int32 // POST /messages with msg_type=text (at-mention pings)
	patches         atomic.Int32 // PATCH /messages/{id}
	reactionDeletes atomic.Int32 // DELETE /messages/{id}/reactions/{rid}
	lastSend        []byte

	mu       sync.Mutex
	allSends [][]byte // every POST /messages body, captured under mu

	lastID atomic.Int32 // assigned message_id seed
}

// snapshotSends returns a defensive copy of allSends so tests can
// iterate without racing the server goroutine.
func (r *upstreamRecorder) snapshotSends() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]byte, len(r.allSends))
	copy(out, r.allSends)
	return out
}

// textSendBodies filters snapshotSends to just the msg_type=text
// entries — useful for asserting the ping content.
func (r *upstreamRecorder) textSendBodies() [][]byte {
	all := r.snapshotSends()
	out := make([][]byte, 0, len(all))
	for _, body := range all {
		var probe struct {
			MsgType string `json:"msg_type"`
		}
		_ = json.Unmarshal(body, &probe)
		if probe.MsgType == "text" {
			out = append(out, body)
		}
	}
	return out
}

func newUpstreamRecorder(t *testing.T) *upstreamRecorder {
	t.Helper()
	rec := &upstreamRecorder{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions/") && r.Method == http.MethodDelete:
			rec.reactionDeletes.Add(1)
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok"}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			switch r.Method {
			case http.MethodPost:
				body, _ := io.ReadAll(r.Body)
				rec.lastSend = body
				rec.sends.Add(1)
				// Classify by msg_type so test assertions can pin
				// "one card + one ping" rather than just "two sends".
				var probe struct {
					MsgType string `json:"msg_type"`
				}
				_ = json.Unmarshal(body, &probe)
				switch probe.MsgType {
				case "text":
					rec.textSends.Add(1)
				default:
					// interactive (Done / Error / Permission / form)
					// or anything else we haven't started sending —
					// either way it's not a ping, so it counts as a
					// card for assertion purposes.
					rec.cardSends.Add(1)
				}
				rec.mu.Lock()
				rec.allSends = append(rec.allSends, body)
				rec.mu.Unlock()
				id := rec.lastID.Add(1)
				_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_drv_`+strings.Repeat("0", 4-len(itoa(int(id))))+itoa(int(id))+`"}}`)
			case http.MethodPatch:
				rec.patches.Add(1)
				_, _ = io.WriteString(w, `{"code":0}`)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(rec.server.Close)
	return rec
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	out := ""
	for n > 0 {
		out = string('0'+byte(n%10)) + out
		n /= 10
	}
	return out
}

// TestInflightTickOnce_NoActiveConversations confirms the cheap fast
// path: when ListActiveFeishuInflightConversations returns nothing,
// the driver is a no-op (no Feishu calls, no Upserts, no Clears).
func TestInflightTickOnce_NoActiveConversations(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})

	n, err := worker.InflightTickOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("processed = %d, want 0", n)
	}
	if rec.cardSends.Load() != 0 || rec.patches.Load() != 0 {
		t.Errorf("upstream calls sends=%d patches=%d, want 0/0", rec.cardSends.Load(), rec.patches.Load())
	}
	if len(fs.inflightUpserts) != 0 || len(fs.inflightClears) != 0 {
		t.Errorf("store mutations upserts=%d clears=%d, want 0/0", len(fs.inflightUpserts), len(fs.inflightClears))
	}
}

// TestInflightTickOnce_FirstSendCreatesWorkingCard covers the
// initial-send branch: a running conversation with no inflight slot
// and one tool.call event in agent_run_events. The driver should
// POST a working card and Upsert the slot capturing the returned
// message_id.
func TestInflightTickOnce_FirstSendCreatesWorkingCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:   "conv-1",
		WorkspaceID:      "ws-1",
		ExternalChatID:   "oc_drv_chat",
		ExternalThreadID: "",
		SourceAppID:      "cli_drv",
		AgentRunID:       "run-1",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-3 * time.Second).UTC(),
		MaxEventSequence: 1,
	}}
	fs.inflightEvents["run-1"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "ls -la"}},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.cardSends.Load() != 1 {
		t.Errorf("sends = %d, want 1 (initial reply)", rec.cardSends.Load())
	}
	if rec.patches.Load() != 0 {
		t.Errorf("patches = %d, want 0 on first send", rec.patches.Load())
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(fs.inflightUpserts))
	}
	got := fs.inflightUpserts[0]
	if got.ExpectedOldRunID != "" {
		t.Errorf("first send ExpectedOldRunID = %q, want \"\"", got.ExpectedOldRunID)
	}
	if got.Slot.ExternalMsgID == "" {
		t.Errorf("first send slot ExternalMsgID empty; want captured Feishu message_id")
	}
	if got.Slot.SeqEmitted != 1 {
		t.Errorf("first send SeqEmitted = %d, want 1", got.Slot.SeqEmitted)
	}

	// Confirm the card content carried the Bash step.
	var outer struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.lastSend, &outer); err != nil {
		t.Fatalf("upstream body did not parse: %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(outer.Content), &card); err != nil {
		t.Fatalf("card content not JSON: %v", err)
	}
	header, _ := card["header"].(map[string]any)
	if header["template"] != "indigo" {
		t.Errorf("first send template = %v, want indigo (WorkingCard)", header["template"])
	}
}

// TestInflightTickOnce_SubsequentTickPatches covers the patch
// branch: a conversation that already has an inflight slot and one
// fresh tool.call event. The driver should PATCH the existing
// message_id rather than POST, and Upsert with the previous
// message_id as the optimistic-lock guard.
func TestInflightTickOnce_SubsequentTickPatches(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	// Pre-existing inflight slot in the metadata blob.
	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_existing_123",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-2",
				"seq_emitted":      float64(1),
				"payload":          map[string]any{"steps": []any{map[string]any{"tool": "Read", "label": "Read · main.go"}}},
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-2",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-2",
		RunStatus:            "running",
		RunStartedAt:         time.Now().Add(-5 * time.Second).UTC(),
		MaxEventSequence:     2,
	}}
	fs.inflightEvents["run-2"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Read", "stage": "before", "args": map[string]any{"file_path": "main.go"}}},
		{Sequence: 2, EventKind: "tool.call", Payload: map[string]any{"name": "Edit", "stage": "before", "args": map[string]any{"file_path": "main.go"}}},
	}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.patches.Load() != 1 {
		t.Errorf("patches = %d, want 1", rec.patches.Load())
	}
	if rec.cardSends.Load() != 0 {
		t.Errorf("sends = %d, want 0 on subsequent tick", rec.cardSends.Load())
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(fs.inflightUpserts))
	}
	got := fs.inflightUpserts[0]
	if got.ExpectedOldRunID != "run-2" {
		t.Errorf("patch ExpectedOldRunID = %q, want run-2", got.ExpectedOldRunID)
	}
	if got.Slot.SeqEmitted != 2 {
		t.Errorf("patch SeqEmitted = %d, want 2", got.Slot.SeqEmitted)
	}
	// Slot payload should contain both Read (preserved from prev)
	// and Edit (newly folded).
	stepsList, _ := got.Slot.Payload["steps"].([]map[string]any)
	if len(stepsList) != 2 {
		t.Fatalf("steps preserved+new = %d, want 2", len(stepsList))
	}
}

// TestInflightTickOnce_NewRunWithStaleSlotSendsFreshCard covers the
// regression that prompted the per-run slot rewrite: a conversation
// whose working slot still points at a previous run's message_id
// must not have its next run PATCH that stale card. The driver
// should treat the prev slot as "not mine" and POST a fresh card.
func TestInflightTickOnce_NewRunWithStaleSlotSendsFreshCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_prev_run_card",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-old",
				"seq_emitted":      int64(99),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-cross-run",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		AgentRunID:           "run-new",
		RunStatus:            "running",
		RunStartedAt:         time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence:     1,
		ConversationMetadata: metadata,
	}}
	fs.inflightEvents["run-new"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "echo hi"}},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.cardSends.Load() != 1 {
		t.Errorf("sends = %d, want 1 (first send for new run)", rec.cardSends.Load())
	}
	if rec.patches.Load() != 0 {
		t.Errorf("patches = %d, want 0; new run must not PATCH old run's card", rec.patches.Load())
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("upserts = %d, want 1", len(fs.inflightUpserts))
	}
	got := fs.inflightUpserts[0]
	if got.ExpectedOldRunID != "run-old" {
		t.Errorf("ExpectedOldRunID = %q, want run-old; new run must claim the stale slot in one shot", got.ExpectedOldRunID)
	}
	if got.Slot.AgentRunID != "run-new" {
		t.Errorf("Slot.AgentRunID = %q, want run-new", got.Slot.AgentRunID)
	}

	// Second tick: slot is now owned by run-new, no new events. Driver
	// must NOT send another card. Without the prev.AgentRunID fix the
	// first Upsert would have failed silently each tick and a fresh
	// card would be sent every time (prod 2026-06-18 16:02 regression).
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.cardSends.Load() != 1 {
		t.Errorf("sends after 2nd tick = %d, want 1; driver re-sent the card on tick 2 — stale-slot guard broken", rec.cardSends.Load())
	}
}


// terminal-state branch: a run with status=completed AND an existing
// inflight slot. The driver should PATCH the working card into the
// final DoneCard shape, mark the output messages row delivered to
// suppress the duplicate P1 send, and clear the inflight slot.
func TestInflightTickOnce_RunCompletedPatchesAndClears(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_about_to_finalise",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-3",
				"seq_emitted":      float64(2),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-3",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-3",
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-12 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-3",
		MaxEventSequence:     3,
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.patches.Load() != 1 {
		t.Errorf("patches = %d, want 1 (final Done patch)", rec.patches.Load())
	}
	if len(fs.inflightClears) != 1 {
		t.Errorf("clears = %d, want 1", len(fs.inflightClears))
	} else if fs.inflightClears[0].Slot != store.InflightSlotWorking {
		t.Errorf("clear slot = %q, want %q", fs.inflightClears[0].Slot, store.InflightSlotWorking)
	}
	// The P1 outbound suppression: the terminal messages row should
	// be marked delivered with the patched message_id as delivery_id.
	if len(fs.delivered) != 1 {
		t.Fatalf("delivered = %d, want 1 (suppress P1 duplicate)", len(fs.delivered))
	}
	if fs.delivered[0].MessageID != "msg-output-3" {
		t.Errorf("delivered MessageID = %q, want msg-output-3", fs.delivered[0].MessageID)
	}
	// DeliveryID was P1-era metadata; Phase 6 stopped persisting it
	// and the driver no longer threads it.
}

// TestInflightTickOnce_RunCompletedWithoutWorkingSlotOwnsSend covers
// the short-run case: agent finished so fast (< 1 tick) that no
// working card was ever sent. New behaviour (single-card design): the
// driver MUST send the terminal card itself rather than deferring to
// P1, because the P1/P2 race would otherwise produce two cards. It
// records the delivery id so P1 stays suppressed.
func TestInflightTickOnce_RunCompletedWithoutWorkingSlotOwnsSend(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-4",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{}, // no inflight slot
		AgentRunID:           "run-4",
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-200 * time.Millisecond).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-4",
		MaxEventSequence:     1, // run.completed event present but never rendered
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// P2 sends exactly one terminal card (Send, not Patch — there is
	// no message_id yet).
	if rec.patches.Load() != 0 || rec.cardSends.Load() != 1 {
		t.Errorf("upstream calls sends=%d patches=%d, want sends=1 patches=0 (P2 owns the terminal send)",
			rec.cardSends.Load(), rec.patches.Load())
	}
	// Suppress P1: the messages row gets marked delivered with the
	// newly-sent external message_id as delivery_id.
	if len(fs.delivered) != 1 {
		t.Fatalf("delivered marks = %d, want 1 (P2 must suppress P1)", len(fs.delivered))
	}
	if fs.delivered[0].MessageID != "msg-output-4" {
		t.Errorf("delivered MessageID = %q, want msg-output-4", fs.delivered[0].MessageID)
	}
	if len(fs.inflightClears) != 1 {
		t.Errorf("clears = %d, want 1 (always-clear after terminal)", len(fs.inflightClears))
	}
}

// TestInflightTickOnce_RunFailedPatchesErrorCard mirrors the
// completed case but for status=failed. Same patch+clear flow, but
// the final card content should be a red ErrorCard.
func TestInflightTickOnce_RunFailedPatchesErrorCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	// Custom upstream that captures the PATCH body so we can
	// inspect the card template.
	var patchBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages") && r.Method == http.MethodPatch:
			patchBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_fail_target",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-5",
				"seq_emitted":      float64(1),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-5",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-5",
		RunStatus:            "failed",
		RunStartedAt:         time.Now().Add(-2 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-5",
		MaxEventSequence:     2,
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(patchBody) == 0 {
		t.Fatalf("no PATCH body captured")
	}
	var outer struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(patchBody, &outer); err != nil {
		t.Fatalf("patch body did not parse: %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(outer.Content), &card); err != nil {
		t.Fatalf("card content not JSON: %v", err)
	}
	header, _ := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Errorf("failed-run final card template = %v, want red", header["template"])
	}
}

// TestInflightTickOnce_RunFailedRendersUserVisibleMessage pins the
// Phase 3 contract: a run.failed agent_run_event carries a
// user_visible_message in its payload, the driver folds it into the
// slot, and the terminal ErrorCard body shows that exact text rather
// than the generic "Agent 运行失败" fallback. Before Phase 3 this
// payload field didn't exist and the (now-removed) P1 outbound worker
// rendered the message from a messages-table row instead.
func TestInflightTickOnce_RunFailedRendersUserVisibleMessage(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	var patchBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages") && r.Method == http.MethodPatch:
			patchBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	wantMessage := "Agent 需要的能力凭据还没设置，请先到我的凭据补齐后重试。"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID: "conv-vis",
		WorkspaceID:    "ws-1",
		ExternalChatID: "oc_drv_chat",
		SourceAppID:    "cli_drv",
		ConversationMetadata: map[string]any{
			"gateway_inflight": map[string]any{
				"working": map[string]any{
					"external_msg_id":  "om_vis_target",
					"app_id":           "cli_drv",
					"external_chat_id": "oc_drv_chat",
					"agent_run_id":     "run-vis",
					"seq_emitted":      float64(0),
				},
			},
		},
		AgentRunID:       "run-vis",
		RunStatus:        "failed",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		RunFinishedAt:    time.Now().UTC(),
		OutputMessageID:  "msg-output-vis",
		MaxEventSequence: 1,
	}}
	// One pending event: the run.failed lifecycle event the
	// (driver-only) failRunWithVisibleMessage path emits, carrying the
	// translated user message in its payload.
	fs.inflightEvents["run-vis"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "run.failed",
		Payload: map[string]any{
			"source":               "agent_run",
			"error":                "capability_credential_missing",
			"user_visible_message": wantMessage,
		},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(patchBody) == 0 {
		t.Fatalf("no PATCH body captured")
	}
	var outer struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(patchBody, &outer); err != nil {
		t.Fatalf("patch body did not parse: %v", err)
	}
	if !strings.Contains(outer.Content, wantMessage) {
		t.Errorf("ErrorCard body does not contain user_visible_message %q; full content: %s", wantMessage, outer.Content)
	}
}

// TestInflightTickOnce_TerminalPatchWhenSeqEmittedCatchUp pins the
// exact race the Phase 1 SQL fix targets. Setup:
//
//   - slot.seq_emitted == FeishuInflightConversation.MaxEventSequence
//     (every user-visible event has already been folded into the card),
//   - RunStatus = "completed" (run.completed event has landed),
//   - MaxEventSequence advanced to include the run.completed event.
//
// Before the fix the SQL claim filter excluded run.completed from the
// run_event_max CTE, so max_seq stayed at the last user-visible event.
// Once seq_emitted caught up to that value the conversation dropped
// out of the claim batch and the terminal Done patch never fired —
// the placeholder "executing" card stayed stuck and a second card
// was written downstream. After the fix run.completed bumps max_seq
// and the conversation gets re-claimed; the driver then PATCHes the
// final Done card and ClearSlots.
//
// This is the driver-side companion to TestClaim_RunCompletedTriggersDriver
// in store_inflight_claim_test.go.
func TestInflightTickOnce_TerminalPatchWhenSeqEmittedCatchUp(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	// seq_emitted = 5 means the driver has already PATCHed every
	// user-visible event into the working card. The conversation
	// is only here because run.completed bumped MaxEventSequence
	// to 6 (Phase 1 fix). Pre-fix: MaxEventSequence would have
	// stayed at 5, this row would not be claimed, and the
	// terminal patch would never happen.
	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_catch_up_target",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-catchup",
				"seq_emitted":      float64(5),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-catchup",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-catchup",
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-30 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-catchup",
		MaxEventSequence:     6, // run.completed bumped this past seq_emitted
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// One PATCH on the existing card — no SEND, no second card.
	if rec.cardSends.Load() != 0 {
		t.Errorf("sends = %d, want 0 (terminal must PATCH, not SEND a second card)", rec.cardSends.Load())
	}
	if rec.patches.Load() != 1 {
		t.Errorf("patches = %d, want 1 (final Done patch on the existing card)", rec.patches.Load())
	}
	// Slot must be cleared so the next run starts fresh.
	if len(fs.inflightClears) != 1 {
		t.Errorf("clears = %d, want 1 (always-clear after terminal)", len(fs.inflightClears))
	} else if fs.inflightClears[0].Slot != store.InflightSlotWorking {
		t.Errorf("clear slot = %q, want %q", fs.inflightClears[0].Slot, store.InflightSlotWorking)
	}
	// The terminal messages row gets marked delivered with the
	// patched message_id so the (soon-to-be-removed) P1 worker can't
	// race-send a duplicate Done card during the cutover.
	if len(fs.delivered) != 1 {
		t.Fatalf("delivered = %d, want 1 (suppress P1 duplicate)", len(fs.delivered))
	}
	// DeliveryID was P1-era metadata; Phase 6 stopped persisting it
	// and the driver no longer threads it.
}

// TestFoldEventsIntoCardState_StepDedupAndOrder is a pure-fold test
// (no driver, no fakes). Verifies tool.call events become steps in
// sequence order, tool.result events backfill EndedAt on the matching
// id, and prev.Payload steps are preserved on the front.
func TestFoldEventsIntoCardState_StepDedupAndOrder(t *testing.T) {
	prev := store.WorkingInflightSlot{
		SeqEmitted: 3,
		Payload: map[string]any{
			"steps": []any{
				map[string]any{"tool": "Read", "label": "Read · main.go"},
			},
		},
	}
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	events := []store.AgentRunEvent{
		{Sequence: 4, EventKind: "tool.call", Payload: map[string]any{"id": "call_edit", "name": "Edit", "stage": "before", "args": map[string]any{"file_path": "main.go"}}, OccurredAt: t0},
		{Sequence: 5, EventKind: "tool.result", Payload: map[string]any{"id": "call_edit", "name": "Edit", "stage": "after"}, OccurredAt: t0.Add(2 * time.Second)},
		{Sequence: 6, EventKind: "tool.call", Payload: map[string]any{"id": "call_bash", "name": "Bash", "stage": "before", "args": map[string]any{"command": "go test ./..."}}, OccurredAt: t0.Add(3 * time.Second)},
	}
	steps, streamingText, _, newSeq, _, _, _ := foldEventsIntoCardState(prev, events)

	if newSeq != 6 {
		t.Errorf("newSeq = %d, want 6", newSeq)
	}
	if streamingText != "" {
		t.Errorf("streamingText = %q, want \"\"", streamingText)
	}
	if len(steps) != 3 {
		t.Fatalf("len(steps) = %d, want 3 (Read+Edit+Bash)", len(steps))
	}
	if steps[0].Tool != "Read" {
		t.Errorf("steps[0] = %+v, want Read preserved from prev", steps[0])
	}
	if steps[1].Tool != "Edit" {
		t.Errorf("steps[1] = %+v, want Edit", steps[1])
	}
	if steps[1].ID != "call_edit" {
		t.Errorf("steps[1].ID = %q, want call_edit", steps[1].ID)
	}
	if !steps[1].StartedAt.Equal(t0) {
		t.Errorf("steps[1].StartedAt = %v, want %v", steps[1].StartedAt, t0)
	}
	if !steps[1].EndedAt.Equal(t0.Add(2 * time.Second)) {
		t.Errorf("steps[1].EndedAt = %v, want %v (paired by id)", steps[1].EndedAt, t0.Add(2*time.Second))
	}
	if got := steps[1].Duration(time.Time{}); got != 2*time.Second {
		t.Errorf("steps[1].Duration = %v, want 2s", got)
	}
	if steps[2].Tool != "Bash" {
		t.Errorf("steps[2] = %+v, want Bash", steps[2])
	}
	if !steps[2].EndedAt.IsZero() {
		t.Errorf("steps[2].EndedAt = %v, want zero (no paired result yet)", steps[2].EndedAt)
	}
	if !strings.Contains(steps[2].Label, "go test") {
		t.Errorf("steps[2].Label = %q, want it to include 'go test' from arg summary", steps[2].Label)
	}
}

// TestFoldEventsIntoCardState_SkillLabel locks down the Skill row
// label across the live-fold path. Regression: the row used to
// collapse to a bare "Skill" because summariseToolArgs had no
// Skill case — the user couldn't tell which skill ran from the card.
// Mirror test for the DoneCard rebuild path lives in
// gateway/done_card_assembly_test.go.
func TestFoldEventsIntoCardState_SkillLabel(t *testing.T) {
	t0 := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	events := []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"id": "call_skill", "name": "Skill", "stage": "before", "args": map[string]any{"skill": "parsar-debug"}}, OccurredAt: t0},
		// Skill with no args still degrades cleanly to bare "Skill".
		{Sequence: 2, EventKind: "tool.call", Payload: map[string]any{"id": "call_skill_bare", "name": "Skill", "stage": "before"}, OccurredAt: t0.Add(time.Second)},
	}
	steps, _, _, _, _, _, _ := foldEventsIntoCardState(store.WorkingInflightSlot{}, events)
	if len(steps) != 2 {
		t.Fatalf("len(steps) = %d, want 2", len(steps))
	}
	if steps[0].Tool != "Skill" || steps[0].Label != "Skill · parsar-debug" {
		t.Errorf("steps[0] = %+v, want Skill + `Skill · parsar-debug` label", steps[0])
	}
	if steps[1].Tool != "Skill" || steps[1].Label != "Skill" {
		t.Errorf("steps[1] = %+v, want bare `Skill` label when args missing", steps[1])
	}
}

// TestFoldEventsIntoCardState_StreamingDeltaAppends asserts that
// message.delta events concatenate into streamingText so the
// StreamingCard can render the running reply.
func TestFoldEventsIntoCardState_StreamingDeltaAppends(t *testing.T) {
	prev := store.WorkingInflightSlot{Payload: map[string]any{"streaming_text": "hel"}}
	events := []store.AgentRunEvent{
		{Sequence: 1, EventKind: "message.delta", Payload: map[string]any{"delta": "lo "}},
		{Sequence: 2, EventKind: "message.delta", Payload: map[string]any{"delta": "world"}},
	}
	_, streamingText, _, _, _, _, _ := foldEventsIntoCardState(prev, events)
	if streamingText != "hello world" {
		t.Errorf("streamingText = %q, want %q", streamingText, "hello world")
	}
}

// TestFoldEventsIntoCardState_ThinkingStaysSeparateFromStreaming
// pins the contract Bug 1 of the single-card MR specifically
// targeted: message.thinking events MUST accumulate into the
// thinkingText return value, NOT into streamingText. If they leaked
// into streamingText the inflight driver would render a
// StreamingCard with the model's reasoning trace and send an extra
// card into the chat — exactly the regression we're fixing.
func TestFoldEventsIntoCardState_ThinkingStaysSeparateFromStreaming(t *testing.T) {
	prev := store.WorkingInflightSlot{Payload: map[string]any{"thinking_text": "Before. "}}
	events := []store.AgentRunEvent{
		{Sequence: 1, EventKind: "message.thinking", Payload: map[string]any{"thinking": "User said X. "}},
		{Sequence: 2, EventKind: "message.delta", Payload: map[string]any{"delta": "Hi"}},
		{Sequence: 3, EventKind: "message.thinking", Payload: map[string]any{"thinking": "I should reply briefly."}},
	}
	_, streamingText, thinkingText, _, _, _, _ := foldEventsIntoCardState(prev, events)
	if streamingText != "Hi" {
		t.Errorf("streamingText = %q, want %q (thinking must not splice in)", streamingText, "Hi")
	}
	want := "Before. User said X. I should reply briefly."
	if thinkingText != want {
		t.Errorf("thinkingText = %q, want %q", thinkingText, want)
	}
}

// TestSlotPayload_RoundTripsThinking verifies the jsonb shape the
// driver writes into conversations.metadata.gateway_inflight.working
// preserves thinking_text across ticks. Without round-tripping the
// driver's next tick would lose the reasoning and emit a Done card
// with an empty Thinking panel.
func TestSlotPayload_RoundTripsThinking(t *testing.T) {
	steps := []gateway.StepInfo{{Tool: "Bash", Label: "ls"}}
	payload := slotPayload(steps, "hi there", "the reasoning", "", "")
	if got := thinkingTextFromPayload(payload); got != "the reasoning" {
		t.Errorf("thinkingTextFromPayload round-trip = %q, want %q", got, "the reasoning")
	}
	if got := streamingTextFromPayload(payload); got != "hi there" {
		t.Errorf("streamingTextFromPayload round-trip = %q, want %q", got, "hi there")
	}
	// Empty values should be omitted to keep the jsonb blob compact.
	bare := slotPayload(steps, "", "", "", "")
	if _, has := bare["thinking_text"]; has {
		t.Errorf("slotPayload kept empty thinking_text key; want it omitted")
	}
	if _, has := bare["streaming_text"]; has {
		t.Errorf("slotPayload kept empty streaming_text key; want it omitted")
	}
	if _, has := bare["error_message"]; has {
		t.Errorf("slotPayload kept empty error_message key; want it omitted")
	}
	if _, has := bare["raw_error"]; has {
		t.Errorf("slotPayload kept empty raw_error key; want it omitted")
	}
}

// TestSlotPayload_RoundTripsStepTimestamps pins step ID + StartedAt +
// EndedAt through the jsonb round-trip. Without this the driver's
// next tick re-reads steps from prev.Payload, drops the timestamps,
// and the working card loses per-step duration mid-run.
//
// The round-trip MUST go through json.Marshal+Unmarshal because
// jsonb_set stores the payload as a JSON blob and PG returns it back
// as map[string]any with []any (not []map[string]any) for arrays.
func TestSlotPayload_RoundTripsStepTimestamps(t *testing.T) {
	started := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	ended := started.Add(3 * time.Second)
	in := []gateway.StepInfo{
		{Tool: "Bash", Label: "Bash · go test", ID: "call_1", StartedAt: started, EndedAt: ended},
		{Tool: "Read", Label: "Read · main.go", ID: "call_2", StartedAt: started.Add(4 * time.Second)},
	}
	raw, err := json.Marshal(slotPayload(in, "", "", "", ""))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped map[string]any
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := stepsFromPayload(roundTripped)
	if len(out) != 2 {
		t.Fatalf("round-trip len = %d, want 2", len(out))
	}
	if out[0].ID != "call_1" || !out[0].StartedAt.Equal(started) || !out[0].EndedAt.Equal(ended) {
		t.Errorf("step[0] round-trip = %+v, want id+started+ended preserved", out[0])
	}
	if !out[1].EndedAt.IsZero() {
		t.Errorf("step[1].EndedAt = %v, want zero (still running)", out[1].EndedAt)
	}
}

// TestBuildFinalCardForRun_PreservesStreamingBody is the regression
// guard for the single-card terminal-patch bug: when the driver folds
// message.delta events into streamingText and then transitions to
// completed, the terminal card content MUST contain the streamed
// reply, not the empty string. Without this the user sees only the
// green "已完成" header + footer and loses the assistant's answer —
// the symptom that reached production (Feishu thread reply showed
// stats-only card, sandboxTest web showed the real reply).
//
// The failed-path branch must still ignore streamingText: an
// ErrorCard mixing a partial streamed body with an error message
// would mislead the user about whether the run actually replied.
func TestBuildFinalCardForRun_PreservesStreamingBody(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	const runID = "00000000-0000-0000-0000-0000000feed1"
	start := time.Now().UTC().Add(-3 * time.Second)
	fs.doneCardData[runID] = store.DoneCardRunData{
		StartedAt:  start,
		FinishedAt: start.Add(2 * time.Second),
		HasUsage:   false,
	}
	c := store.FeishuInflightConversation{
		WorkspaceID:   "ws",
		AgentRunID:    runID,
		RunStatus:     "completed",
		RunStartedAt:  start,
		RunFinishedAt: start.Add(2 * time.Second),
	}
	const body = "你好！有什么我可以帮你的吗?"
	content, err := buildFinalCardForRun(
		context.Background(), fs, c,
		nil,  // steps
		body, // streamingText
		"",   // thinkingText
		"",   // errorMessage
		"",   // rawError
		"",   // publicURL
	)
	if err != nil {
		t.Fatalf("buildFinalCardForRun err = %v", err)
	}
	if !strings.Contains(content, body) {
		t.Fatalf("terminal card content missing streamed body.\n got: %s\nwant substring: %q", content, body)
	}
	// Failed path must still short-circuit to ErrorCard and not leak
	// the streamed body into it.
	cFail := c
	cFail.RunStatus = "failed"
	errContent, err := buildFinalCardForRun(
		context.Background(), fs, cFail, nil, body, "", "boom", "", "",
	)
	if err != nil {
		t.Fatalf("buildFinalCardForRun(failed) err = %v", err)
	}
	if strings.Contains(errContent, body) {
		t.Errorf("failed-path card leaked streamingText into ErrorCard: %s", errContent)
	}
	if !strings.Contains(errContent, "boom") {
		t.Errorf("failed-path card missing error message: %s", errContent)
	}
}

// TestBuildFinalCardForRun_SurfacesGuestHintOnFailure pins the contract
// that the visibility=public guest "go register" hint reaches the
// terminal failed card. Without this surfacing, an unregistered user
// hitting a capability_credential_missing run sees only the generic
// red "empty final output" error with no actionable next step — the
// credential-form path can't recover an inbound for sender_type=
// 'external' messages.
func TestBuildFinalCardForRun_SurfacesGuestHintOnFailure(t *testing.T) {
	fs := newFakeStore()
	const conv = "conv-guest"
	const runID = "run-guest"
	const hint = "您还未绑定账号，请前往 Parsar 网页端完成绑定后再使用机器人。"
	fs.guestReplyHint = map[string]string{conv: hint}

	c := store.FeishuInflightConversation{
		ConversationID: conv,
		WorkspaceID:    "ws",
		AgentRunID:     runID,
		RunStatus:      "failed",
	}
	content, err := buildFinalCardForRun(
		context.Background(), fs, c, nil, "", "",
		"Agent 执行失败，请展开本轮错误详情查看具体原因。", "empty final output", "",
	)
	if err != nil {
		t.Fatalf("buildFinalCardForRun err = %v", err)
	}
	if !strings.Contains(content, hint) {
		t.Errorf("failed card missing guest hint.\n got: %s\nwant substring: %q", content, hint)
	}
}

// --- helpers ---

func happyAgentWithAppID(appID string) store.FeishuAgentRoute {
	cfg := map[string]any{
		"connectors": map[string]any{
			"feishu": map[string]any{
				"enabled":                true,
				"app_id":                 appID,
				"app_secret_ref":         "secret_happy",
				"verification_token_ref": "secret_verify",
			},
		},
	}
	raw, _ := json.Marshal(cfg)
	return store.FeishuAgentRoute{
		AgentID:     "agent-drv-" + appID,
		WorkspaceID: "ws-1",
		Visibility:  string(gateway.VisibilityWorkspace),
		Config:      raw,
	}
}

// TestInflightTickOnce_UsesClaim covers the multi-pod-race fix: the
// driver MUST call ClaimActiveFeishuInflightConversations rather
// than the legacy ListActive — otherwise N sibling pods each see the
// same row and each call Feishu SendMessage, spamming N working
// cards. The unit test only asserts the call signature (real
// disjoint-batch semantics need Postgres; covered in the integration
// test). We assert:
//
//   - Claim was called exactly once per tick.
//   - claimed_by was non-empty (the worker's pod identity flows through).
//   - stale_before sits in the past, so a stalled sibling's claim
//     would be revokable.
//   - finished_cutoff sits in the past, matching the 5-minute window.
//
// Plus the negative assertion: ListActive must NOT be called from
// the driver path (a regression would silently re-introduce the
// race).
func TestInflightTickOnce_UsesClaim(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})

	before := time.Now().UTC()
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC()

	if len(fs.inflightClaimCalls) != 1 {
		t.Fatalf("claim calls = %d, want 1 (driver must claim, not list)", len(fs.inflightClaimCalls))
	}
	// Negative assertion: legacy ListActive must not be invoked from
	// the tick path. inflightCutoffs is only ever appended by
	// ListActive's fake (not by ClaimActive's fake), so it staying
	// empty proves the driver no longer reaches that codepath.
	if len(fs.inflightCutoffs) != 0 {
		t.Errorf("ListActive was called %d times during tick; driver must use ClaimActive instead", len(fs.inflightCutoffs))
	}
	c := fs.inflightClaimCalls[0]
	if c.ClaimedBy == "" {
		t.Error("claimed_by is empty; pod identity must flow through to give the SQL's @claimed_by branch something to compare against")
	}
	if !c.StaleBefore.Before(after) || !c.StaleBefore.After(before.Add(-time.Hour)) {
		t.Errorf("stale_before = %v, want a recent past timestamp (now - 30s)", c.StaleBefore)
	}
	if !c.FinishedCutoff.Before(after) {
		t.Errorf("finished_cutoff = %v, want in the past", c.FinishedCutoff)
	}
	// Sanity: limit is whatever the driver passes; just ensure it's
	// positive so a misconfigured constant doesn't slip through.
	if c.Limit <= 0 {
		t.Errorf("limit = %d, want > 0", c.Limit)
	}
}

// TestInflightTickOnce_FullLifecycle pins the driver-only happy path
// end-to-end: a run flows from started → tool.call patch →
// run.completed terminal patch in three driver ticks (each simulating
// one DB cycle), and on the terminal tick the typing-reaction DELETE
// goroutine fires and the messages row is marked delivered exactly
// once. No P1 outbound path is involved — this lifecycle is what the
// Phase 5 refactor leaves in place.
//
// Expected API call shape: 1 POST (initial working card) + 1 PATCH
// (tool.call delta) + 1 PATCH (terminal Done card) + 1 DELETE
// (reaction undo on terminal landing).
func TestInflightTickOnce_FullLifecycle(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	// Seed a typing reaction on the inbound side so the terminal
	// DELETE has something to undo.
	fs.reactionsByConv["conv-life"] = store.FeishuInboundReactionRow{
		MessageID:         "inbound-msg-life",
		WorkspaceID:       "ws-1",
		ExternalMessageID: "om_user_typed",
		ReactionID:        "r-typing",
		AppID:             "cli_drv",
	}
	rec := newUpstreamRecorder(t)

	// Helper: rebuild the conversation row each tick with the
	// up-to-date metadata blob (mimicking what the real
	// ClaimActive query would observe after the previous tick's
	// Upsert lands).
	convForTick := func(metadata map[string]any, runStatus string, finishedAt time.Time, maxSeq int64) []store.FeishuInflightConversation {
		return []store.FeishuInflightConversation{{
			ConversationID:       "conv-life",
			WorkspaceID:          "ws-1",
			ExternalChatID:       "oc_drv_chat",
			SourceAppID:          "cli_drv",
			ConversationMetadata: metadata,
			AgentRunID:           "run-life",
			RunStatus:            runStatus,
			RunStartedAt:         time.Now().Add(-30 * time.Second).UTC(),
			RunFinishedAt:        finishedAt,
			OutputMessageID:      "msg-life-output",
			MaxEventSequence:     maxSeq,
		}}
	}

	// Tick 1: run.started + first tool.call → initial POST.
	fs.inflightConvs = convForTick(nil, "running", time.Time{}, 1)
	fs.inflightEvents["run-life"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "echo hi"}},
	}}

	worker, err := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("tick 1: %v", err)
	}
	if rec.cardSends.Load() != 1 {
		t.Fatalf("after tick 1: sends = %d, want 1", rec.cardSends.Load())
	}
	if rec.patches.Load() != 0 {
		t.Fatalf("after tick 1: patches = %d, want 0", rec.patches.Load())
	}
	if len(fs.inflightUpserts) != 1 {
		t.Fatalf("after tick 1: upserts = %d, want 1", len(fs.inflightUpserts))
	}
	postedMsgID := fs.inflightUpserts[0].Slot.ExternalMsgID
	if postedMsgID == "" {
		t.Fatal("tick 1: posted message id not captured into slot")
	}

	// Tick 2: another tool.call event → driver should PATCH the
	// existing message_id rather than POST again.
	tick2Metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  postedMsgID,
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-life",
				"seq_emitted":      float64(1),
			},
		},
	}
	fs.inflightConvs = convForTick(tick2Metadata, "running", time.Time{}, 2)
	fs.inflightEvents["run-life"] = append(fs.inflightEvents["run-life"], store.AgentRunEvent{
		Sequence:  2,
		EventKind: "tool.call",
		Payload:   map[string]any{"name": "Read", "stage": "before", "args": map[string]any{"file": "main.go"}},
	})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("tick 2: %v", err)
	}
	if rec.cardSends.Load() != 1 {
		t.Errorf("after tick 2: sends = %d, want still 1 (no fresh POST)", rec.cardSends.Load())
	}
	if rec.patches.Load() != 1 {
		t.Errorf("after tick 2: patches = %d, want 1 (mid-run PATCH)", rec.patches.Load())
	}

	// Tick 3: run.completed. Driver patches the terminal Done card
	// into the same message_id, marks the messages row delivered,
	// clears the working slot, and fires the typing-reaction DELETE.
	tick3Metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  postedMsgID,
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-life",
				"seq_emitted":      float64(2),
			},
		},
	}
	fs.inflightConvs = convForTick(tick3Metadata, "completed", time.Now().UTC(), 3)
	fs.inflightEvents["run-life"] = append(fs.inflightEvents["run-life"], store.AgentRunEvent{
		Sequence:  3,
		EventKind: "run.completed",
	})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("tick 3: %v", err)
	}
	if rec.cardSends.Load() != 1 {
		t.Errorf("after tick 3: sends = %d, want 1 across whole lifecycle", rec.cardSends.Load())
	}
	if rec.patches.Load() != 2 {
		t.Errorf("after tick 3: patches = %d, want 2 (mid-run + terminal)", rec.patches.Load())
	}
	if len(fs.delivered) != 1 {
		t.Fatalf("after tick 3: delivered = %d, want 1", len(fs.delivered))
	}
	if fs.delivered[0].MessageID != "msg-life-output" {
		t.Errorf("delivered MessageID = %q, want msg-life-output", fs.delivered[0].MessageID)
	}
	_ = postedMsgID // DeliveryID is no longer threaded; presence-on-slot remains the contract.
	if len(fs.inflightClears) != 1 || fs.inflightClears[0].Slot != store.InflightSlotWorking {
		t.Errorf("expected one working-slot clear, got %+v", fs.inflightClears)
	}

	// Reaction DELETE is fire-and-forget on a 5s timeout, spin
	// briefly so the goroutine lands the HTTP call + metadata clear.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rec.reactionDeletes.Load() >= 1 && len(fs.reactionClears) >= 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.reactionDeletes.Load() != 1 {
		t.Errorf("reactionDeletes = %d, want 1", rec.reactionDeletes.Load())
	}
	if len(fs.reactionClears) != 1 || fs.reactionClears[0] != "inbound-msg-life" {
		t.Errorf("reactionClears = %+v, want [inbound-msg-life]", fs.reactionClears)
	}
}

// TestInflightTickOnce_CredentialFormCardReplacesDoneCard locks in the
// outbound override: when the resolver soft-degraded a
// credential-missing MCP this run, the terminal card the user sees is
// the orange credential-form card with the matching kinds + a qkey that
// the submit callback uses to re-enqueue the original raw_query.
// We assert: (a) a qkey row was stashed with the correct raw_query +
// chat scope, (b) exactly one Feishu send fired (not a patch — short
// run, no working slot), (c) the regular DoneCard did NOT render
// (no SendSystemNoticeMessage / no normal usage rollup read).
func TestInflightTickOnce_CredentialFormCardReplacesDoneCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-cred-form"
	runID := "run-cred-form"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_form_chat",
		ExternalThreadID:     "om_form_anchor",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-form-output",
		MaxEventSequence:     1,
	}}
	// Two missing notices — one with a duplicate kind+capability to
	// exercise the de-dup, one fresh.
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{
				MessageID:      "msg-notice-1",
				CapabilityID:   "cap-github",
				CapabilityName: "GitHub",
				CredentialKind: "github_pat",
			},
			{
				MessageID:      "msg-notice-1b",
				CapabilityID:   "cap-github",
				CapabilityName: "GitHub",
				CredentialKind: "github_pat",
			},
			{
				MessageID:      "msg-notice-2",
				CapabilityID:   "cap-slack",
				CapabilityName: "Slack",
				CredentialKind: "slack_bot_token",
			},
		},
	}
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:         "inbound-msg-1",
			RawQuery:          "list my open PRs",
			TargetAgentID:     "agt-1",
			ExternalChatID:    "oc_form_chat",
			ExternalThreadID:  "om_form_anchor",
			ExternalMessageID: "om_user_input",
			SenderUserID:      "user-bob",
			// SenderOpenID is required by the form-card path so the
			// submit-card callback can pin the operator's open_id
			// against the stash. Without it the driver skips the
			// form card and falls through to the regular DoneCard.
			SenderOpenID: "ou_bob",
		},
	}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.cardSends.Load() != 1 {
		t.Errorf("upstream sends = %d, want 1 (form-card replaces DoneCard)", rec.cardSends.Load())
	}
	if rec.patches.Load() != 0 {
		t.Errorf("upstream patches = %d, want 0 (no working slot, no patch)", rec.patches.Load())
	}
	if len(fs.pendingFormsWritten) != 1 {
		t.Fatalf("pending forms written = %d, want 1", len(fs.pendingFormsWritten))
	}
	write := fs.pendingFormsWritten[0]
	if write.Slot.RawQuery != "list my open PRs" {
		t.Errorf("stashed raw_query = %q, want verbatim user input", write.Slot.RawQuery)
	}
	if write.ConversationID != convID || write.Slot.InitiatorUserID != "user-bob" {
		t.Errorf("stash identity mismatch: conv=%s slot=%+v", write.ConversationID, write.Slot)
	}
	if !strings.HasPrefix(write.Slot.Qkey, "qkey_") {
		t.Errorf("qkey prefix wrong: %q", write.Slot.Qkey)
	}
	// Slot version drops the explicit AgentID / Source / Chat / Thread
	// columns — the rerun inbound re-resolves agent_id at submit time
	// via gateway_sessions.selected_agent_id, and chat/thread/app come
	// from the host conversation. So we only assert the 5 fields the
	// slot still stores; the others are conversation-scope facts the
	// submit handler reads back fresh.
}

// TestInflightTickOnce_CredentialFormCardFallsBackWhenNoInbound documents
// the safety branch: if we can't recover the raw_query (no inbound
// message lineage) we DO NOT ship a form card that can't auto-resume.
// We fall back to the regular DoneCard so the user at least sees the
// run's output; the in-chat system message still tells them to bind.
func TestInflightTickOnce_CredentialFormCardFallsBackWhenNoInbound(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-cred-noinbound"
	runID := "run-cred-noinbound"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-out",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-x", CredentialKind: "github_pat"},
		},
	}
	// inboundUserMsg empty → GetInboundUserMessageForRun returns zero
	// value, fall-back triggers.

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Regular DoneCard send still fires.
	if rec.cardSends.Load() != 1 {
		t.Errorf("upstream sends = %d, want 1 (fallback to regular terminal card)", rec.cardSends.Load())
	}
	if len(fs.pendingFormsWritten) != 0 {
		t.Errorf("must not stash a pending form when inbound is unrecoverable: %+v", fs.pendingFormsWritten)
	}
}

// TestInflightTickOnce_CredentialFormCardFallsBackWhenNoSenderOpenID
// pins the safety guard at the form-card emit side: if the
// inbound has a SenderUserID + RawQuery but no SenderOpenID, the
// submit-card callback would have no authoritative open_id to compare
// against. Shipping a form anyway would silently degrade the auth
// check to "any chat member can submit". The driver MUST refuse and
// fall back to the regular DoneCard, leaving the in-chat system
// message to nudge the user to the web UI.
func TestInflightTickOnce_CredentialFormCardFallsBackWhenNoSenderOpenID(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-no-openid"
	runID := "run-no-openid"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-out-no-openid",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-x", CredentialKind: "github_pat"},
		},
	}
	// Inbound has everything EXCEPT SenderOpenID — the legacy code path
	// would have shipped a form anyway; the post-review path refuses.
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:     "inbound-no-openid",
			RawQuery:      "ask the agent",
			TargetAgentID: "agt-1",
			SenderUserID:  "user-bob",
			// SenderOpenID intentionally omitted.
		},
	}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec.cardSends.Load() != 1 {
		t.Errorf("upstream sends = %d, want 1 (fallback to regular DoneCard)", rec.cardSends.Load())
	}
	if len(fs.pendingFormsWritten) != 0 {
		t.Errorf("must not stash a pending form when sender_open_id is missing: %+v", fs.pendingFormsWritten)
	}
}

// TestInflightTickOnce_CredentialFormCardLoopGuard pins the second-try
// safety net: the inbound that triggered this run was itself produced
// by a credential-form submit (metadata.reenqueued_from), and the
// resolver STILL emitted a missing-credential notice. That means the
// user mistyped their credential and a second form card would just
// loop. We must fall through to the regular terminal card so the user
// is forced to fix it via the web UI.
func TestInflightTickOnce_CredentialFormCardLoopGuard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-loop-guard"
	runID := "run-loop-guard"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-out-loop",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-x", CredentialKind: "github_pat", CapabilityName: "GitHub"},
		},
	}
	// The inbound carries the loop marker: it was re-enqueued by the
	// previous form-submit handler. Same kind still missing → mistyped.
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:      "inbound-replay",
			RawQuery:       "list my open PRs",
			TargetAgentID:  "agt-1",
			ExternalChatID: "oc_chat",
			ReenqueuedFrom: "credential_form_submit",
			SenderUserID:   "user-bob",
			SenderOpenID:   "ou_bob",
		},
	}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Regular DoneCard fires (fallback path), NOT a fresh form card.
	if rec.cardSends.Load() != 1 {
		t.Errorf("upstream sends = %d, want 1 (fallback to regular terminal card)", rec.cardSends.Load())
	}
	if len(fs.pendingFormsWritten) != 0 {
		t.Errorf("must not stash a second pending form when the turn was already a retry: %+v", fs.pendingFormsWritten)
	}
}

// TestInflightTickOnce_CredentialFormSecondTickPatchesExistingMessage
// is the core regression guard for the prod 2026-06-18 "3 cards in 3
// minutes" incident: when a credential-form slot is already on the
// conversation (from a previous tick or a sibling pod) the driver
// MUST PatchMessage the existing om_… rather than SendMessage a new
// card. Same qkey, same Feishu message, exactly one card in the
// chat regardless of how many ticks the run stays in the claim set.
func TestInflightTickOnce_CredentialFormSecondTickPatchesExistingMessage(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-form-reuse"
	runID := "run-form-reuse"
	existingMsgID := "om_existing_form_card"
	existingQkey := "qkey_pre_existing_slot"
	// Seed an already-stashed slot with an external_msg_id pinned.
	// The insert-or-noop writer will return this to the driver.
	fs.pendingFormStored = map[string]store.PendingCredentialFormSlot{
		convID: {
			Qkey:            existingQkey,
			ExternalMsgID:   existingMsgID,
			InitiatorOpenID: "ou_bob",
			InitiatorUserID: "user-bob",
			AgentID:         "agt-1",
			RawQuery:        "list my open PRs",
			ExpiresAt:       time.Now().Add(time.Hour).UTC(),
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_form_chat",
		ExternalThreadID:     "om_form_anchor",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-form-output",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-github", CapabilityName: "GitHub", CredentialKind: "github_pat"},
		},
	}
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:        "inbound-msg-1",
			RawQuery:         "list my open PRs",
			TargetAgentID:    "agt-1",
			ExternalChatID:   "oc_form_chat",
			ExternalThreadID: "om_form_anchor",
			SenderUserID:     "user-bob",
			SenderOpenID:     "ou_bob",
		},
	}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The critical invariant: NO new card was sent. The existing
	// om_… was patched in place.
	if got := rec.cardSends.Load(); got != 0 {
		t.Errorf("upstream card sends = %d, want 0 (slot reuse must patch, not send)", got)
	}
	if got := rec.patches.Load(); got != 1 {
		t.Errorf("upstream patches = %d, want 1 (the pre-existing form card)", got)
	}
	// Fingerprint MUST be stamped so the next tick exits the claim
	// set instead of running this path again.
	if len(fs.terminalDeliveredMarks) != 1 || fs.terminalDeliveredMarks[0].RunID != runID {
		t.Errorf("terminalDeliveredMarks = %+v, want one entry for run %s", fs.terminalDeliveredMarks, runID)
	}
	// No new external_msg_id stamp — the slot already had one.
	if len(fs.pendingFormMsgIDStamps) != 0 {
		t.Errorf("pendingFormMsgIDStamps = %+v, want empty (slot's om_… was already pinned)", fs.pendingFormMsgIDStamps)
	}
}

// TestInflightTickOnce_CredentialFormFirstTickStampsExternalMsgID is
// the partner test: when no slot exists yet, the driver mints a slot
// (the insert-or-noop SQL persists the new payload), SendMessage's
// the card, then stamps the returned om_… onto the slot so the
// next tick's reuse path has something to PatchMessage.
func TestInflightTickOnce_CredentialFormFirstTickStampsExternalMsgID(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-form-first"
	runID := "run-form-first"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_form_chat",
		ExternalThreadID:     "om_form_anchor",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-form-output",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-github", CapabilityName: "GitHub", CredentialKind: "github_pat"},
		},
	}
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:        "inbound-msg-1",
			RawQuery:         "list my open PRs",
			TargetAgentID:    "agt-1",
			ExternalChatID:   "oc_form_chat",
			ExternalThreadID: "om_form_anchor",
			SenderUserID:     "user-bob",
			SenderOpenID:     "ou_bob",
		},
	}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := rec.cardSends.Load(); got != 1 {
		t.Errorf("card sends = %d, want 1 (first send)", got)
	}
	if len(fs.pendingFormsWritten) != 1 {
		t.Fatalf("stashes = %d, want 1", len(fs.pendingFormsWritten))
	}
	if len(fs.pendingFormMsgIDStamps) != 1 {
		t.Fatalf("external_msg_id stamps = %d, want 1: %+v", len(fs.pendingFormMsgIDStamps), fs.pendingFormMsgIDStamps)
	}
	stamp := fs.pendingFormMsgIDStamps[0]
	if stamp.ConversationID != convID {
		t.Errorf("stamp conversation = %q, want %q", stamp.ConversationID, convID)
	}
	if stamp.Qkey != fs.pendingFormsWritten[0].Slot.Qkey {
		t.Errorf("stamp qkey = %q, want stashed qkey %q", stamp.Qkey, fs.pendingFormsWritten[0].Slot.Qkey)
	}
	if stamp.ExternalMsgID == "" {
		t.Errorf("stamp external_msg_id is empty; want the om_… the upstream recorder returned")
	}
}

// TestInflightTickOnce_CredentialFormFingerprintStampedWhenSendFails
// pins the C half of the fix: even when SendMessage fails, the slot
// is already persisted (so the next tick will reuse it via Patch) and
// the per-run fingerprint MUST be stamped so the claim filter closes.
// Without this, the original 2026-06-18 bug returns: every tick stashes
// a new slot's worth of work, increments nothing, and Send keeps
// hitting the same upstream error.
func TestInflightTickOnce_CredentialFormFingerprintStampedWhenSendFails(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	convID := "conv-form-sendfail"
	runID := "run-form-sendfail"
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       convID,
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		ExternalThreadID:     "om_anchor",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           runID,
		RunStatus:            "failed",
		RunStartedAt:         time.Now().Add(-1 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "",
		MaxEventSequence:     1,
	}}
	fs.missingNotices = map[string][]store.CapabilityCredentialMissingNotice{
		convID + "|" + runID: {
			{CapabilityID: "cap-github", CredentialKind: "github_pat"},
		},
	}
	fs.inboundUserMsg = map[string]store.InboundUserMessageForRun{
		convID: {
			MessageID:        "inbound-msg-1",
			RawQuery:         "list my open PRs",
			TargetAgentID:    "agt-1",
			ExternalChatID:   "oc_drv_chat",
			ExternalThreadID: "om_anchor",
			SenderUserID:     "user-bob",
			SenderOpenID:     "ou_bob",
		},
	}

	// Upstream returns 500 on every POST — every Send fails. The
	// driver should still write the slot and stamp the fingerprint.
	rec := &upstreamRecorder{}
	rec.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(rec.server.Close)

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	// handleUpstreamWorkingFailure returns nil — transient failures are
	// not propagated to the tick (would block other conversations).
	_, _ = worker.InflightTickOnce(context.Background())

	// The slot WAS written, regardless of upstream failure.
	if len(fs.pendingFormsWritten) != 1 {
		t.Fatalf("slot stash count = %d, want 1 even when upstream Send failed", len(fs.pendingFormsWritten))
	}
	// Fingerprint MUST be stamped so the run exits the claim set.
	// This is the prod fix: without it, ClaimActiveFeishuInflightConversations
	// keeps re-picking the run and the chat sees N cards.
	if len(fs.terminalDeliveredMarks) != 1 || fs.terminalDeliveredMarks[0].RunID != runID {
		t.Errorf("terminalDeliveredMarks = %+v, want one entry for run %s (fingerprint MUST land even on upstream failure)",
			fs.terminalDeliveredMarks, runID)
	}
}

// TestInflightTickOnce_FailedRunNoOutputMessageStampsTerminalFingerprint
// is the regression guard for the "Agent 执行失败 card spam" bug. A run
// that failed before producing an output_message_id (the FailAgentRun
// path, which never sets agent_runs.output_message_id) used to drive
// the driver into a permanent re-claim loop:
//
//  1. claim SQL's "m.metadata->>'gateway_delivered_at' = ”" OR branch
//     was always true because the LEFT JOIN to messages found nothing
//  2. driver's MarkGatewayOutboundDelivered call no-op'd under the
//     `OutputMessageID != ""` guard
//  3. ClearSlot was a no-op (no slot in the short-run branch)
//  4. next tick re-claimed the run and re-sent another red card
//
// The fix is a per-run fingerprint on conversations.metadata. This
// test asserts the driver stamps that fingerprint exactly once even
// when the messages-side marker can't be written, so the claim filter
// can close on the next tick.
func TestInflightTickOnce_FailedRunNoOutputMessageStampsTerminalFingerprint(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	// No gateway_inflight.working slot — this is the short-run path
	// (run failed before any working card was sent). And no
	// OutputMessageID — this is the FailAgentRun-before-output case.
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-fail-noom",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{}, // no gateway_inflight tree
		AgentRunID:           "run-fail-noom",
		RunStatus:            "failed",
		RunStartedAt:         time.Now().Add(-2 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "", // <-- the trigger condition
		MaxEventSequence:     1,
	}}
	fs.inflightEvents["run-fail-noom"] = []store.AgentRunEvent{{
		Sequence:  1,
		EventKind: "run.failed",
		Payload: map[string]any{
			"source":               "agent_run",
			"error":                "capability_credential_missing",
			"user_visible_message": "Agent 需要的能力凭据还没设置",
		},
	}}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	// One terminal card sent (no PATCH because no working slot).
	if got := rec.cardSends.Load(); got != 1 {
		t.Errorf("first tick sends = %d, want 1", got)
	}
	if got := rec.patches.Load(); got != 0 {
		t.Errorf("first tick patches = %d, want 0 (short run, no working slot)", got)
	}
	// The messages-side MarkGatewayOutboundDelivered MUST be skipped
	// because OutputMessageID is empty — that's the original guard.
	if len(fs.delivered) != 0 {
		t.Errorf("MarkGatewayOutboundDelivered fired %d times despite empty OutputMessageID; want 0",
			len(fs.delivered))
	}
	// But MarkConversationInflightTerminalDelivered MUST fire so the
	// claim filter can close on the next tick.
	if len(fs.terminalDeliveredMarks) != 1 {
		t.Fatalf("MarkConversationInflightTerminalDelivered fired %d times; want 1: %+v",
			len(fs.terminalDeliveredMarks), fs.terminalDeliveredMarks)
	}
	mark := fs.terminalDeliveredMarks[0]
	if mark.ConversationID != "conv-fail-noom" {
		t.Errorf("fingerprint conversation_id = %q, want conv-fail-noom", mark.ConversationID)
	}
	if mark.RunID != "run-fail-noom" {
		t.Errorf("fingerprint run_id = %q, want run-fail-noom", mark.RunID)
	}
}

// TestMarkConversationInflightTerminalDeliveredFailureStopsTick checks
// the safety inverse of the previous test: if the fingerprint write
// fails (e.g. transient pg outage), the driver MUST surface the
// error so the next tick re-claims and retries. Silently swallowing
// here would have the same effect as the original bug (no
// fingerprint → claim re-picks → re-send), just delayed by one tick.
func TestMarkConversationInflightTerminalDeliveredFailureStopsTick(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()

	// Wrap fs so the fingerprint call fails.
	wrap := &failingFingerprintStore{fakeStore: fs, err: errors.New("pg down")}

	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-fail-fp",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: map[string]any{},
		AgentRunID:           "run-fail-fp",
		RunStatus:            "failed",
		RunStartedAt:         time.Now().Add(-2 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "",
		MaxEventSequence:     1,
	}}

	rec := newUpstreamRecorder(t)
	worker, _ := NewWorker(Options{Store: wrap, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	// InflightTickOnce swallows per-conversation errors into Warn logs
	// and returns a count, so the failure surfaces as "send fired but
	// fingerprint never recorded" — exactly what the next tick needs
	// to know to retry. Assert that shape.
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatalf("tick returned an error; expected per-conv failure to be logged + swallowed: %v", err)
	}
	if got := rec.cardSends.Load(); got != 1 {
		t.Errorf("send fired %d times; want 1 (the card landed before fingerprint)", got)
	}
	if len(fs.terminalDeliveredMarks) != 0 {
		t.Errorf("fingerprint should not be recorded when its write fails: %+v",
			fs.terminalDeliveredMarks)
	}
}

// failingFingerprintStore is a thin override on fakeStore that makes
// MarkConversationInflightTerminalDelivered fail. Embeds rather than
// re-implements the rest of Storer so we don't have to keep this
// fixture in sync with every other method.
type failingFingerprintStore struct {
	*fakeStore
	err error
}

func (s *failingFingerprintStore) MarkConversationInflightTerminalDelivered(_ context.Context, _ string, _ string) error {
	return s.err
}

// ---------- at-mention ping (post-card text message) ----------
//
// The terminal / permission card paths each follow up with a
// msg_type=text message that embeds `<at user_id="...">用户</at>`. The
// tests below pin one assertion per scenario: the right body lands, in
// the right order, with the right idempotency.

// TestInflightTickOnce_RunCompletedSendsPingAfterCard locks in the
// happy path: a completed run with a known sender_open_id produces
// one card patch AND one text ping carrying the at-mention escape +
// the standard "任务已完成 ✓ 耗时 Ns" body.
func TestInflightTickOnce_RunCompletedSendsPingAfterCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_finalise_with_ping",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-ping-ok",
				"seq_emitted":      float64(2),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-ping-ok",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		ExternalThreadID:     "om_user_input_ok",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-ping-ok",
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-17 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-ping-ok",
		MaxEventSequence:     3,
		SenderOpenID:         "ou_alice",
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := rec.patches.Load(); got != 1 {
		t.Errorf("patches = %d, want 1 (DoneCard patch)", got)
	}
	if got := rec.cardSends.Load(); got != 0 {
		t.Errorf("cardSends = %d, want 0 (terminal patches an existing card)", got)
	}
	if got := rec.textSends.Load(); got != 1 {
		t.Fatalf("textSends = %d, want 1 (post-card at-mention ping)", got)
	}

	pings := rec.textSendBodies()
	if len(pings) != 1 {
		t.Fatalf("collected ping bodies = %d, want 1", len(pings))
	}
	var outer struct {
		MsgType       string `json:"msg_type"`
		Content       string `json:"content"`
		ReplyInThread bool   `json:"reply_in_thread"`
	}
	if err := json.Unmarshal(pings[0], &outer); err != nil {
		t.Fatalf("ping body unmarshal: %v", err)
	}
	if outer.MsgType != "text" {
		t.Errorf("ping msg_type = %q, want text", outer.MsgType)
	}
	if !outer.ReplyInThread {
		t.Errorf("ping reply_in_thread = false, want true (with thread anchor)")
	}
	var inner struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(outer.Content), &inner); err != nil {
		t.Fatalf("ping content unmarshal: %v", err)
	}
	if !strings.Contains(inner.Text, `<at user_id="ou_alice">用户</at>`) {
		t.Errorf("ping text missing at-mention escape: %q", inner.Text)
	}
	if !strings.HasPrefix(inner.Text, `<at user_id="ou_alice">用户</at> 任务已完成 ✓ 耗时 `) {
		t.Errorf("ping prefix wrong: %q", inner.Text)
	}
}

// TestInflightTickOnce_RunCompletedPingDegradesWithoutSender locks in
// the documented degradation: when the inflight row is missing a
// sender_open_id (legacy fixtures, system-initiated runs), the ping
// still fires but as a plain-text bubble (no <at> tag). The user gets
// a visible nudge in the thread but no desktop push.
func TestInflightTickOnce_RunCompletedPingDegradesWithoutSender(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"working": map[string]any{
				"external_msg_id":  "om_finalise_no_sender",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-ping-nosend",
				"seq_emitted":      float64(2),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-ping-nosend",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-ping-nosend",
		RunStatus:            "completed",
		RunStartedAt:         time.Now().Add(-3 * time.Second).UTC(),
		RunFinishedAt:        time.Now().UTC(),
		OutputMessageID:      "msg-output-ping-nosend",
		MaxEventSequence:     3,
		// SenderOpenID deliberately empty — exercise the degrade path.
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := rec.textSends.Load(); got != 1 {
		t.Fatalf("textSends = %d, want 1 (degraded ping)", got)
	}
	pings := rec.textSendBodies()
	if len(pings) != 1 {
		t.Fatalf("collected ping bodies = %d, want 1", len(pings))
	}
	var outer struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(pings[0], &outer)
	var inner struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(outer.Content), &inner)
	if strings.Contains(inner.Text, "<at ") {
		t.Errorf("degraded ping leaked <at> tag: %q", inner.Text)
	}
	if !strings.HasPrefix(inner.Text, "任务已完成 ✓ 耗时 ") {
		t.Errorf("degraded ping prefix wrong: %q", inner.Text)
	}
}

// TestInflightTickOnce_PermissionCardSendsPing covers the
// permission-card path: alongside the orange "Allow / Deny" card the
// driver POSTs a separate at-mention text. Pinned by request_id, so a
// follow-up tick that re-encounters the same request must NOT send a
// second ping (covered by TestInflightTickOnce_PermissionAskedIsIdempotent
// — its assertion `cardSends == 0` already implies `textSends == 0`).
func TestInflightTickOnce_PermissionCardSendsPing(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:   "conv-perm-ping",
		WorkspaceID:      "ws-1",
		ExternalChatID:   "oc_drv_chat",
		ExternalThreadID: "om_user_perm_anchor",
		SourceAppID:      "cli_drv",
		AgentRunID:       "run-perm-ping",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence: 2,
		SenderOpenID:     "ou_carol",
	}}
	fs.inflightEvents["run-perm-ping"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{
			"name":  "Bash",
			"stage": "before",
			"args":  map[string]any{"command": "ls"},
		}},
		{Sequence: 2, EventKind: "permission.asked", Payload: map[string]any{
			"request_id": "perm-ping-1",
			"action":     "Bash",
			"detail":     "ls",
		}},
	}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Two cards (working + permission) + exactly one ping (for the
	// permission slot — no terminal yet, so the run-completed ping
	// hasn't fired).
	if got := rec.cardSends.Load(); got != 2 {
		t.Errorf("cardSends = %d, want 2 (working + permission)", got)
	}
	if got := rec.textSends.Load(); got != 1 {
		t.Fatalf("textSends = %d, want 1 (permission ping)", got)
	}
	pings := rec.textSendBodies()
	if len(pings) != 1 {
		t.Fatalf("collected ping bodies = %d, want 1", len(pings))
	}
	var outer struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(pings[0], &outer)
	var inner struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal([]byte(outer.Content), &inner)
	if !strings.Contains(inner.Text, `<at user_id="ou_carol">用户</at>`) {
		t.Errorf("permission ping missing at-mention: %q", inner.Text)
	}
	if !strings.HasSuffix(inner.Text, UserPingPermission) {
		t.Errorf("permission ping copy wrong: %q (want suffix %q)", inner.Text, UserPingPermission)
	}
}
