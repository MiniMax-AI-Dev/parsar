package feishuoutbound

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// stubPermissionRouter records every SubmitPermission call so tests
// can assert the driver's auto-expire path pushed the right verdict
// back into the runtime.
type stubPermissionRouter struct {
	mu        sync.Mutex
	calls     []PermissionDecision
	returnErr error
}

func (s *stubPermissionRouter) SubmitPermission(_ context.Context, decision PermissionDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, decision)
	return s.returnErr
}

// TestInflightTickOnce_PermissionAskedSendsCard covers the new P3
// branch: a permission.asked event in agent_run_events triggers a
// POST PermissionCard + an UpsertConversationInflightPermissionCard
// upsert pinning the permission_request_id. Working slot must NOT
// be touched in the upserts list — permission and working live in
// different inflight slots.
func TestInflightTickOnce_PermissionAskedSendsCard(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:   "conv-perm",
		WorkspaceID:      "ws-1",
		ExternalChatID:   "oc_drv_chat",
		SourceAppID:      "cli_drv",
		AgentRunID:       "run-perm",
		RunStatus:        "running",
		RunStartedAt:     time.Now().Add(-2 * time.Second).UTC(),
		MaxEventSequence: 2,
	}}
	fs.inflightEvents["run-perm"] = []store.AgentRunEvent{
		{Sequence: 1, EventKind: "tool.call", Payload: map[string]any{"name": "Bash", "stage": "before", "args": map[string]any{"command": "rm -rf /tmp/cache"}}},
		{Sequence: 2, EventKind: "permission.asked", Payload: map[string]any{
			"request_id": "perm-xyz-1",
			"action":     "Bash",
			"detail":     "rm -rf /tmp/cache",
		}},
	}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Two POSTs: working card (Bash step) + permission card.
	if rec.cardSends.Load() != 2 {
		t.Errorf("sends = %d, want 2 (working + permission)", rec.cardSends.Load())
	}
	if len(fs.permissionUpserts) != 1 {
		t.Fatalf("permissionUpserts = %d, want 1", len(fs.permissionUpserts))
	}
	got := fs.permissionUpserts[0]
	if got.Slot.PermissionRequestID != "perm-xyz-1" {
		t.Errorf("upsert PermissionRequestID = %q, want perm-xyz-1", got.Slot.PermissionRequestID)
	}
	if got.Slot.ExternalMsgID == "" {
		t.Errorf("upsert ExternalMsgID empty; want captured Feishu message_id")
	}
	if got.ExpectedOldRequestID != "" {
		t.Errorf("first permission send ExpectedOldRequestID = %q, want \"\"", got.ExpectedOldRequestID)
	}
}

// TestInflightTickOnce_PermissionAskedIsIdempotent verifies that if
// the permission slot already pins the same request_id, the driver
// does NOT send a second card. (Stewardhouse parity: one card per
// pending request, not one per tick.)
func TestInflightTickOnce_PermissionAskedIsIdempotent(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.agents["cli_drv"] = happyAgentWithAppID("cli_drv")
	fs.secrets["secret_happy"] = happySecret()
	rec := newUpstreamRecorder(t)

	metadata := map[string]any{
		"gateway_inflight": map[string]any{
			"permission": map[string]any{
				"external_msg_id":       "om_perm_existing",
				"app_id":                "cli_drv",
				"external_chat_id":      "oc_drv_chat",
				"agent_run_id":          "run-perm-2",
				"permission_request_id": "perm-xyz-2",
			},
			"working": map[string]any{
				"external_msg_id":  "om_working_existing",
				"app_id":           "cli_drv",
				"external_chat_id": "oc_drv_chat",
				"agent_run_id":     "run-perm-2",
				"seq_emitted":      float64(2),
			},
		},
	}
	fs.inflightConvs = []store.FeishuInflightConversation{{
		ConversationID:       "conv-perm-2",
		WorkspaceID:          "ws-1",
		ExternalChatID:       "oc_drv_chat",
		SourceAppID:          "cli_drv",
		ConversationMetadata: metadata,
		AgentRunID:           "run-perm-2",
		RunStatus:            "running",
		RunStartedAt:         time.Now().Add(-5 * time.Second).UTC(),
		MaxEventSequence:     3,
	}}
	fs.inflightEvents["run-perm-2"] = []store.AgentRunEvent{
		{Sequence: 3, EventKind: "permission.asked", Payload: map[string]any{
			"request_id": "perm-xyz-2",
			"action":     "Bash",
		}},
	}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: rec.server.URL})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.cardSends.Load() != 0 {
		t.Errorf("sends = %d, want 0 (permission already pinned)", rec.cardSends.Load())
	}
	if len(fs.permissionUpserts) != 0 {
		t.Errorf("permissionUpserts = %d, want 0", len(fs.permissionUpserts))
	}
}

// TestInflightTickOnce_AutoExpiresStalePermissionCard locks in the
// auto-expire path: a permission slot whose updated_at is past the
// 5-minute window gets a forced Deny pushed back via the
// PermissionRouter, the card patched into a warning notice, and the
// slot cleared.
func TestInflightTickOnce_AutoExpiresStalePermissionCard(t *testing.T) {
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

	router := &stubPermissionRouter{}
	fs.stalePermissions = []store.ConversationInflightCards{{
		ConversationID: "conv-stale",
		WorkspaceID:    "ws-1",
		SourceAppID:    "cli_drv",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "om_stale",
			AppID:               "cli_drv",
			ExternalChatID:      "oc_drv_chat",
			AgentRunID:          "run-stale",
			PermissionRequestID: "perm-stale-1",
			UpdatedAt:           time.Now().Add(-10 * time.Minute).UTC(),
		},
	}}

	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}, BaseURL: upstream.URL, PermissionRouter: router})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if len(router.calls) != 1 {
		t.Fatalf("router calls = %d, want 1", len(router.calls))
	}
	if router.calls[0].RequestID != "perm-stale-1" {
		t.Errorf("router RequestID = %q, want perm-stale-1", router.calls[0].RequestID)
	}
	if router.calls[0].Approved {
		t.Errorf("auto-expire should Deny, got Approved=true")
	}
	if len(patchBody) == 0 {
		t.Fatalf("expected PATCH to land timeout card; got nothing")
	}
	if len(fs.inflightClears) != 1 || fs.inflightClears[0].Slot != store.InflightSlotPermission {
		t.Errorf("clears = %+v, want one Permission clear", fs.inflightClears)
	}
}

// TestInflightTickOnce_AutoExpireSkipsWhenRouterUnset confirms the
// driver gracefully degrades when no PermissionRouter is wired: it
// does NOT crash, does NOT loop on the stale list, and surfaces no
// router call. The misconfiguration is operator-loud via logs (not
// asserted here) but the card simply stays pinned.
func TestInflightTickOnce_AutoExpireSkipsWhenRouterUnset(t *testing.T) {
	t.Parallel()
	fs := newFakeStore()
	fs.stalePermissions = []store.ConversationInflightCards{{
		ConversationID: "conv-no-router",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			PermissionRequestID: "perm-orphan",
			ExternalMsgID:       "om_orphan",
			UpdatedAt:           time.Now().Add(-30 * time.Minute).UTC(),
		},
	}}
	worker, _ := NewWorker(Options{Store: fs, Secrets: fakeDecrypter{}})
	if _, err := worker.InflightTickOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fs.inflightClears) != 0 {
		t.Errorf("clears = %d, want 0 (no router → no expire)", len(fs.inflightClears))
	}
}

// TestPendingPermissionFromPayload_PrefersDetailOverPayload locks in
// the pure fold for the permission payload extractor — the
// the card surface relies on `detail` as the rendered
// tool-input snippet, falling back to a JSON preview of `payload`
// only when detail is empty.
func TestPendingPermissionFromPayload_PrefersDetailOverPayload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		payload  map[string]any
		wantTool string
		wantIn   string
	}{
		{
			name: "detail wins",
			payload: map[string]any{
				"request_id": "perm-1",
				"action":     "Bash",
				"detail":     "rm -rf /tmp",
				"payload":    map[string]any{"command": "ignored"},
			},
			wantTool: "Bash",
			wantIn:   "rm -rf /tmp",
		},
		{
			name: "fallback to payload preview",
			payload: map[string]any{
				"request_id": "perm-2",
				"action":     "Bash",
				"payload":    map[string]any{"command": "ls -la"},
			},
			wantTool: "Bash",
			wantIn:   `{"command":"ls -la"}`,
		},
		{
			name: "missing tool name",
			payload: map[string]any{
				"request_id": "perm-3",
				"resource":   "FallbackName",
			},
			wantTool: "FallbackName",
			wantIn:   "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pendingPermissionFromPayload(tc.payload)
			if !ok {
				t.Fatalf("ok = false")
			}
			if got.ToolName != tc.wantTool {
				t.Errorf("ToolName = %q, want %q", got.ToolName, tc.wantTool)
			}
			if got.ToolInput != tc.wantIn {
				t.Errorf("ToolInput = %q, want %q", got.ToolInput, tc.wantIn)
			}
		})
	}
	// Empty request_id → not a pending request.
	if _, ok := pendingPermissionFromPayload(map[string]any{"action": "Bash"}); ok {
		t.Errorf("empty request_id should not produce a pending permission")
	}
}

// json import sanity — keeps the linter happy when other test
// expansions remove their use of encoding/json before this file
// catches up.
var _ = json.Marshal
