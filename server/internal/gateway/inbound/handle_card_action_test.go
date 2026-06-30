package inbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// stubPermissionRouter records SubmitPermission invocations for the
// handleCardAction unit tests. Tests can also seed a returnErr to
// drive the "router rejected" toast branch.
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

// buildPermissionEvent assembles the minimum CardActionTriggerEvent
// shape handleCardAction reads — action + permission_request_id
// in Action.Value, plus the operator open_id which we log.
func buildPermissionEvent(action, permReqID, operatorID, openMsgID string) *callback.CardActionTriggerEvent {
	ev := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{}}
	ev.Event.Action = &callback.CallBackAction{Value: map[string]interface{}{
		"action":                action,
		"permission_request_id": permReqID,
	}}
	ev.Event.Operator = &callback.Operator{OpenID: operatorID}
	ev.Event.Context = &callback.Context{OpenMessageID: openMsgID}
	return ev
}

// upstreamPatchRecorder is a minimal feishu mock that handles only
// the tenant_access_token exchange + PATCH; sends fail with 405 so
// tests can assert the patch path took.
func upstreamPatchRecorder(t *testing.T) (*httptest.Server, *[]byte) {
	t.Helper()
	var last []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages") && r.Method == http.MethodPatch:
			b, _ := io.ReadAll(r.Body)
			last = b
			_, _ = io.WriteString(w, `{"code":0}`)
		default:
			http.Error(w, "unhandled", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &last
}

// TestHandleCardAction_PermissionAllowFullFlow locks in the happy
// path: the user clicks Allow, SubmitPermission is invoked with
// Approved=true, the existing permission card is PATCHed into a
// green result shape, and the inflight slot is cleared.
func TestHandleCardAction_PermissionAllowFullFlow(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	srv, lastPatch := upstreamPatchRecorder(t)

	// Seed default shared bot so the patch path can resolve creds
	// without needing a workspace-scoped secret.
	defaultBot := DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "shared_secret"}

	// Conversation has an inflight permission slot pointing at
	// permission_request_id="perm-allow-1", message_id="om_pending".
	fs.cardsByPermReq["perm-allow-1"] = store.ConversationInflightCards{
		ConversationID: "conv-allow",
		WorkspaceID:    "ws-1",
		SourceAppID:    "cli_shared",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "om_pending",
			AppID:               "cli_shared",
			PermissionRequestID: "perm-allow-1",
		},
	}

	router := &stubPermissionRouter{}
	mgr, err := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		OpenAPIBaseURL:   srv.URL,
		DefaultSharedBot: defaultBot,
		PermissionRouter: router,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := mgr.handleCardAction(context.Background(), "cli_shared",
		buildPermissionEvent("permission_allow", "perm-allow-1", "ou_operator_xy", "om_pending"))

	if resp == nil || resp.Toast == nil {
		t.Fatalf("nil toast response")
	}
	if resp.Toast.Type != "success" {
		t.Errorf("toast type = %q, want success", resp.Toast.Type)
	}
	if len(router.calls) != 1 {
		t.Fatalf("router calls = %d, want 1", len(router.calls))
	}
	if !router.calls[0].Approved {
		t.Errorf("router decision = denied, want approved")
	}
	if router.calls[0].RequestID != "perm-allow-1" {
		t.Errorf("router RequestID = %q", router.calls[0].RequestID)
	}
	if router.calls[0].OperatorID != "ou_operator_xy" {
		t.Errorf("router OperatorID = %q", router.calls[0].OperatorID)
	}
	if len(*lastPatch) == 0 {
		t.Fatalf("no PATCH body captured (result card not sent)")
	}
	if len(fs.permissionClears) != 1 || fs.permissionClears[0].Slot != store.InflightSlotPermission {
		t.Errorf("clears = %+v, want one Permission clear", fs.permissionClears)
	}
}

// TestHandleCardAction_PermissionDenyToast confirms the Deny path
// pushes Approved=false and surfaces an info toast (not success).
func TestHandleCardAction_PermissionDenyToast(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	srv, _ := upstreamPatchRecorder(t)
	fs.cardsByPermReq["perm-deny-1"] = store.ConversationInflightCards{
		ConversationID: "conv-deny",
		WorkspaceID:    "ws-1",
		SourceAppID:    "cli_shared",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "om_pending_deny",
			AppID:               "cli_shared",
			PermissionRequestID: "perm-deny-1",
		},
	}
	router := &stubPermissionRouter{}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		OpenAPIBaseURL:   srv.URL,
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "shared_secret"},
		PermissionRouter: router,
	})

	resp := mgr.handleCardAction(context.Background(), "cli_shared",
		buildPermissionEvent("permission_deny", "perm-deny-1", "ou_op", "om_pending_deny"))

	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if len(router.calls) != 1 || router.calls[0].Approved {
		t.Errorf("router calls = %+v, want one Approved=false", router.calls)
	}
}

// TestHandleCardAction_SlotAlreadyCleared exercises the "another pod
// resolved this already" branch: the FindConversationByPermissionRequestID
// lookup returns ErrUnknownConversation. We expect no router call,
// no patch, and an info toast indicating the request is already
// handled.
func TestHandleCardAction_SlotAlreadyCleared(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	router := &stubPermissionRouter{}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, PermissionRouter: router})

	resp := mgr.handleCardAction(context.Background(), "cli_shared",
		buildPermissionEvent("permission_allow", "missing-perm", "ou_op", "om_old"))

	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if len(router.calls) != 0 {
		t.Errorf("router calls = %d, want 0", len(router.calls))
	}
	if len(fs.permissionClears) != 0 {
		t.Errorf("clears = %d, want 0", len(fs.permissionClears))
	}
}

// TestHandleCardAction_RouterReturnsErrorKeepsSlot makes sure a
// SubmitPermission failure does NOT clear the slot (so the next
// click retries) and surfaces an error toast.
func TestHandleCardAction_RouterReturnsErrorKeepsSlot(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	srv, _ := upstreamPatchRecorder(t)
	fs.cardsByPermReq["perm-err-1"] = store.ConversationInflightCards{
		ConversationID: "conv-err",
		WorkspaceID:    "ws-1",
		SourceAppID:    "cli_shared",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "om_err",
			AppID:               "cli_shared",
			PermissionRequestID: "perm-err-1",
		},
	}
	router := &stubPermissionRouter{returnErr: errors.New("boom")}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		OpenAPIBaseURL:   srv.URL,
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "shared_secret"},
		PermissionRouter: router,
	})
	resp := mgr.handleCardAction(context.Background(), "cli_shared",
		buildPermissionEvent("permission_allow", "perm-err-1", "ou_op", "om_err"))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error", resp.Toast.Type)
	}
	if len(fs.permissionClears) != 0 {
		t.Errorf("clears = %d, want 0 (slot must stay for retry)", len(fs.permissionClears))
	}
}

// TestHandleCardAction_RouterNilToast asserts the "router not
// configured" guardrail — a misconfigured deployment still tells
// the user something happened instead of silently swallowing the
// click.
func TestHandleCardAction_RouterNilToast(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.cardsByPermReq["perm-nil"] = store.ConversationInflightCards{
		ConversationID: "conv-nil",
		WorkspaceID:    "ws-1",
		SourceAppID:    "cli_shared",
		HasPermission:  true,
		Permission: store.PermissionInflightSlot{
			ExternalMsgID:       "om_nil",
			AppID:               "cli_shared",
			PermissionRequestID: "perm-nil",
		},
	}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}})
	resp := mgr.handleCardAction(context.Background(), "cli_shared",
		buildPermissionEvent("permission_allow", "perm-nil", "ou_op", "om_nil"))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error (router missing)", resp.Toast.Type)
	}
	if len(fs.permissionClears) != 0 {
		t.Errorf("clears = %d, want 0", len(fs.permissionClears))
	}
}

// TestHandleCardAction_UnknownActionFallsThrough confirms a card
// button whose value action is not permission_* still gets a
// generic ack (so future card types don't 500 the user).
func TestHandleCardAction_UnknownActionFallsThrough(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	router := &stubPermissionRouter{}
	mgr, _ := NewManager(Options{Store: fs, Secrets: inboundFakeDecrypter{}, PermissionRouter: router})

	ev := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{}}
	ev.Event.Action = &callback.CallBackAction{Value: map[string]interface{}{"action": "noop"}}
	openMsg := "om_noop"
	openID := "ou_someone"
	ev.Event.Context = &callback.Context{OpenMessageID: openMsg}
	ev.Event.Operator = &callback.Operator{OpenID: openID}

	resp := mgr.handleCardAction(context.Background(), "cli_shared", ev)
	if resp == nil || resp.Toast == nil || resp.Toast.Type != "info" {
		t.Errorf("toast = %+v, want info ack", resp)
	}
	if len(router.calls) != 0 {
		t.Errorf("router calls = %d, want 0 on unknown action", len(router.calls))
	}
}

// buildCredentialFormEvent assembles the CardActionTriggerEvent shape
// handleCredentialFormSubmitAction reads: action=credential_form_submit
// in Action.Value with qkey, and form fields in Action.FormValue keyed
// "credential_<kind>" → plaintext. The operator open_id + open_chat_id
// participate in the post-review authz check; callers pass them
// explicitly so a test can drive the mismatch branches.
func buildCredentialFormEvent(qkey, operatorOpenID, openChatID string, fields map[string]string) *callback.CardActionTriggerEvent {
	ev := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{}}
	ev.Event.Action = &callback.CallBackAction{
		Value: map[string]interface{}{
			"action": "credential_form_submit",
			"qkey":   qkey,
		},
		FormValue: map[string]interface{}{},
	}
	for name, val := range fields {
		ev.Event.Action.FormValue[name] = val
	}
	ev.Event.Operator = &callback.Operator{OpenID: operatorOpenID}
	ev.Event.Context = &callback.Context{OpenMessageID: "om_form_card", OpenChatID: openChatID}
	return ev
}

// TestHandleCardAction_CredentialFormSubmitFullFlow is the ADR-004 happy
// path: qkey resolves, every kind is persisted as user_credential, qkey
// is deleted, and the raw_query is re-enqueued as a fresh inbound. We
// assert each step so a future refactor that drops one of them fails
// loudly.
func TestHandleCardAction_CredentialFormSubmitFullFlow(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	// Slot version: claim returns the slot + host-conversation IDs in
	// one round-trip. agent_id rides on the slot itself (stamped at
	// stash time by the outbound inflight driver), so the submit
	// handler reads it from slot.AgentID — no gateway_sessions lookup
	// in this path.
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_abc": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_abc",
				InitiatorOpenID: "ou_bob",
				InitiatorUserID: "user-bob",
				AgentID:         "agt-1",
				RawQuery:        "list my open PRs",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ConversationID:   "conv-1",
			WorkspaceID:      "ws-1",
			ExternalChatID:   "oc_chat",
			ExternalThreadID: "om_thread",
			SourceAppID:      "cli_app",
		},
	}
	fs.selections = map[string]string{selectionTestKey("feishu", "oc_chat", "om_thread"): "agt-1"}
	mgr, err := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_abc", "ou_bob", "oc_chat", map[string]string{
		"credential_github_pat":      "ghp_real_token",
		"credential_slack_bot_token": "xoxb-real",
	}))
	if resp == nil || resp.Toast == nil {
		t.Fatal("nil toast response")
	}
	if resp.Toast.Type != "success" {
		t.Errorf("toast type = %q, want success: %+v", resp.Toast.Type, resp.Toast)
	}
	// Feishu's callback contract uses response.card.data as the
	// canonical post-callback render. The PATCH /im/v1/messages path
	// we used before this MR returned ok but the client snapped the
	// orange form back whenever this field was missing, so the
	// finalized-card MUST be threaded through here.
	if resp.Card == nil {
		t.Fatal("resp.Card = nil, want the finalized-card payload Feishu uses to overwrite the orange form")
	}
	if resp.Card.Type != "raw" {
		t.Errorf("resp.Card.Type = %q, want \"raw\"", resp.Card.Type)
	}
	cardData, ok := resp.Card.Data.(map[string]any)
	if !ok {
		t.Fatalf("resp.Card.Data is %T, want map[string]any", resp.Card.Data)
	}
	header, _ := cardData["header"].(map[string]any)
	if got, _ := header["template"].(string); got != "green" {
		t.Errorf("resp.Card.Data header.template = %q, want \"green\" (finalized card)", got)
	}
	if len(fs.userCredentialsCreated) != 2 {
		t.Fatalf("user credentials persisted = %d, want 2: %+v", len(fs.userCredentialsCreated), fs.userCredentialsCreated)
	}
	byKind := map[string]store.CreateUserCredentialInput{}
	for _, c := range fs.userCredentialsCreated {
		byKind[c.Kind] = c
		if c.UserID != "user-bob" {
			t.Errorf("user credential for %q stored under user %q, want user-bob", c.Kind, c.UserID)
		}
		if len(c.EncryptedValue) == 0 {
			t.Errorf("user credential %q encrypted_value is empty", c.Kind)
		}
	}
	if _, ok := byKind["github_pat"]; !ok {
		t.Errorf("missing github_pat: %+v", byKind)
	}
	if _, ok := byKind["slack_bot_token"]; !ok {
		t.Errorf("missing slack_bot_token: %+v", byKind)
	}
	// ReplaceUserCredentials should have been called exactly once with
	// both kinds in one tx-wrapped batch (the legacy per-kind loop was
	// what leaked partial state on partial failure).
	if len(fs.replaceUserCredentialsCalls) != 1 {
		t.Errorf("expected 1 ReplaceUserCredentials call (tx-wrapped batch), got %d", len(fs.replaceUserCredentialsCalls))
	} else if fs.replaceUserCredentialsCalls[0].UserID != "user-bob" || len(fs.replaceUserCredentialsCalls[0].Inputs) != 2 {
		t.Errorf("batch shape mismatch: %+v", fs.replaceUserCredentialsCalls[0])
	}
	if len(fs.pendingFormsClaimed) != 1 || fs.pendingFormsClaimed[0] != "qkey_abc" {
		t.Errorf("qkey cleanup mismatch (should be claimed-and-cleared exactly once): %+v", fs.pendingFormsClaimed)
	}
	if len(fs.created) != 1 {
		t.Fatalf("expected 1 re-enqueued inbound, got %d", len(fs.created))
	}
	rerun := fs.created[0]
	if rerun.Text != "list my open PRs" {
		t.Errorf("re-enqueue text = %q, want raw_query verbatim", rerun.Text)
	}
	if rerun.TargetAgentID != "agt-1" {
		t.Errorf("re-enqueue target_agent_id = %q, want agt-1", rerun.TargetAgentID)
	}
	if rerun.ExternalChatID != "oc_chat" || rerun.ExternalThreadID != "om_thread" {
		t.Errorf("re-enqueue chat scope mismatch: %+v", rerun)
	}
	if rerun.SenderOpenID != "ou_bob" {
		t.Errorf("re-enqueue SenderOpenID = %q, want ou_bob (must be re-stamped so a follow-up form-card path can still authenticate the submit)", rerun.SenderOpenID)
	}
	if rerun.InitiatorUserID != "user-bob" {
		t.Errorf("re-enqueue InitiatorUserID = %q, want user-bob (must be threaded through so the re-fired run gets a non-empty requested_by_id without round-tripping open_id → union_id)", rerun.InitiatorUserID)
	}
	if got := rerun.Metadata["reenqueued_qkey"]; got != "qkey_abc" {
		t.Errorf("re-enqueue metadata.reenqueued_qkey = %v, want qkey_abc", got)
	}
	// C1: the re-enqueue MUST bust store.CreateInboundIMMessage's gateway
	// dedup short-circuit. The original ExternalMessageID is preserved in
	// metadata for the audit trail, but the field that drives dedup must
	// carry a unique suffix so the re-enqueue creates a fresh inbound row
	// and a fresh agent_run instead of silently re-using the terminated
	// run from the form-card emit turn.
	//
	// Slot version: the slot no longer stores the original
	// external_message_id (it was write-only audit metadata, never read
	// in the production path), so the dedup-bust key is synthesised
	// from the qkey alone — `qkey:<qkey>`. qkey is mint-once unique so
	// this still guarantees CreateInboundIMMessage misses the dedup row.
	if rerun.ExternalMessageID != "qkey:qkey_abc" {
		t.Errorf("re-enqueue ExternalMessageID = %q, want %q (qkey-based dedup-bust)", rerun.ExternalMessageID, "qkey:qkey_abc")
	}
}

// TestHandleCardAction_CredentialFormSubmitUnknownQkey covers the
// expired / already-processed / lost-race path: the user clicked submit
// but the stash is gone. We must not write any credentials and must
// surface an info toast so the user re-sends the message instead of
// waiting. Note this also covers HIGH-1's multi-pod race outcome: the
// losing pod's ClaimAndDeleteFeishuCredentialQkey returns NotFound.
func TestHandleCardAction_CredentialFormSubmitUnknownQkey(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	mgr, err := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_missing", "ou_someone", "oc_chat", map[string]string{
		"credential_github_pat": "ghp_x",
	}))
	if resp == nil || resp.Toast == nil {
		t.Fatal("nil toast")
	}
	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("must not persist credentials when qkey is unknown: %+v", fs.userCredentialsCreated)
	}
	if len(fs.created) != 0 {
		t.Errorf("must not re-enqueue when qkey is unknown")
	}
}

// TestHandleCardAction_CredentialFormSubmitRejectsBlank confirms an
// empty plaintext (client-side bypass of required=true) is rejected
// loudly so the user retries instead of saving a blank credential.
func TestHandleCardAction_CredentialFormSubmitRejectsBlank(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_blank": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_blank",
				InitiatorUserID: "user-1",
				InitiatorOpenID: "ou_user1",
				RawQuery:        "x",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
		},
	}
	mgr, err := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_blank", "ou_user1", "", map[string]string{
		"credential_github_pat": "   ",
	}))
	if resp == nil || resp.Toast == nil {
		t.Fatal("nil toast")
	}
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error", resp.Toast.Type)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("must not persist blank credentials: %+v", fs.userCredentialsCreated)
	}
}

// TestHandleCardAction_CredentialFormSubmitRejectsOperatorMismatch is
// the BLOCKER-1 guard: in a group chat any member can see the form
// card; without the open_id check anyone could click submit and have
// their input written under the original initiator's user_credentials
// row. Stash captures the inbound sender's open_id; if the click came
// from a different open_id we MUST reject and MUST NOT write anything.
// We also assert the qkey was still claimed (consumed exactly once)
// so an attacker can't keep poking the callback to defeat one-shot
// semantics.
func TestHandleCardAction_CredentialFormSubmitRejectsOperatorMismatch(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_mismatch": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_mismatch",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "ou_bob",
				RawQuery:        "do the thing",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ConversationID: "conv-mm",
			SourceAppID:    "cli_app",
			ExternalChatID: "oc_chat",
		},
	}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_mismatch", "ou_charlie", "oc_chat", map[string]string{
		"credential_github_pat": "ghp_evil",
	}))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error (operator mismatch)", resp.Toast.Type)
	}
	// Reject path must thread the red terminal card through the
	// callback response so the orange form retires on the legitimate
	// initiator's screen without a separate PATCH (see
	// ackToastWithCard for the full rationale).
	if resp.Card == nil {
		t.Fatal("operator-mismatch reject: resp.Card = nil, want the red terminal card payload")
	}
	if data, ok := resp.Card.Data.(map[string]any); ok {
		if header, _ := data["header"].(map[string]any); header != nil {
			if got, _ := header["template"].(string); got != "red" {
				t.Errorf("reject card header.template = %q, want \"red\"", got)
			}
		}
	} else {
		t.Errorf("resp.Card.Data is %T, want map[string]any", resp.Card.Data)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Fatalf("must NOT write credentials when operator open_id mismatches stash: %+v", fs.userCredentialsCreated)
	}
	if len(fs.replaceUserCredentialsCalls) != 0 {
		t.Errorf("must NOT call ReplaceUserCredentials on operator mismatch")
	}
	if len(fs.pendingFormsClaimed) != 1 {
		t.Errorf("qkey must still be claimed exactly once on mismatch (one-shot): %+v", fs.pendingFormsClaimed)
	}
	if len(fs.created) != 0 {
		t.Errorf("must NOT re-enqueue inbound on operator mismatch")
	}
}

// TestHandleCardAction_CredentialFormSubmitRejectsEmptyStashOpenID
// pins the migration-fallback safety: stashes written before
// migration 000003 (or by a path that forgot to capture sender_open_id)
// land with an empty initiator_open_id. The submit handler MUST refuse
// such stashes — letting them through would degrade the auth check
// into "anyone can click as long as the stash exists".
func TestHandleCardAction_CredentialFormSubmitRejectsEmptyStashOpenID(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_legacy": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_legacy",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "", // legacy slot, no open_id captured
				RawQuery:        "x",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			SourceAppID:    "cli_app",
			ExternalChatID: "oc_chat",
		},
	}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_legacy", "ou_anyone", "oc_chat", map[string]string{
		"credential_github_pat": "ghp_x",
	}))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error (empty stash open_id)", resp.Toast.Type)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("legacy stash without open_id must not authorize a write")
	}
}

// TestHandleCardAction_CredentialFormSubmitRejectsChatMismatch covers
// HIGH-8: a qkey leaked into a different chat must not be redeemable
// from that chat. We seed the stash with one chat id and submit from
// another (with the right open_id, so only the chat check can save us).
func TestHandleCardAction_CredentialFormSubmitRejectsChatMismatch(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_chat": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_chat",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "ou_bob",
				RawQuery:        "x",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			SourceAppID:    "cli_app",
			ExternalChatID: "oc_chat_A",
		},
	}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_chat", "ou_bob", "oc_chat_B", map[string]string{
		"credential_github_pat": "ghp_x",
	}))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error (chat mismatch)", resp.Toast.Type)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("must not write credentials when submit comes from a different chat")
	}
}

// TestHandleCardAction_CredentialFormSubmitReplacesExistingToast covers
// BLOCKER-2's "fix the dead-end where the user already had this kind":
// the legacy path crashed against the partial unique index; the
// post-fix path replaces the existing row and surfaces a toast telling
// the user we overwrote it.
func TestHandleCardAction_CredentialFormSubmitReplacesExistingToast(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_replace": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_replace",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "ou_bob",
				AgentID:         "agt-1",
				RawQuery:        "x",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ExternalChatID: "oc_chat",
			SourceAppID:    "cli_app",
		},
	}
	// Pre-seed which kinds will be reported as "replaced" by the store.
	fs.replaceUserCredentialsReplaced = map[string]bool{"github_pat": true}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_replace", "ou_bob", "oc_chat", map[string]string{
		"credential_github_pat":      "ghp_new",
		"credential_slack_bot_token": "xoxb_new",
	}))
	if resp.Toast.Type != "success" {
		t.Errorf("toast type = %q, want success (replace is success too)", resp.Toast.Type)
	}
	if !strings.Contains(resp.Toast.Content, "替换 1 项") {
		t.Errorf("toast content = %q, want it to mention the replacement count", resp.Toast.Content)
	}
}

// TestHandleCardAction_CredentialFormSubmitRollsBackOnReplaceError
// covers the BLOCKER-2 partial-failure invariant: a tx-level write
// failure must NOT leak per-kind writes that landed before the error.
// We force ReplaceUserCredentials to error and assert (a) the user-
// facing toast is error, (b) the re-enqueue inbound was NOT issued,
// (c) the qkey was already consumed (the row is gone, the user must
// re-send to retry — same shape as a successful claim that then
// failed downstream).
func TestHandleCardAction_CredentialFormSubmitRollsBackOnReplaceError(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_rollback": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_rollback",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "ou_bob",
				RawQuery:        "x",
				ExpiresAt:       time.Now().UTC().Add(time.Hour),
			},
			ExternalChatID: "oc_chat",
			SourceAppID:    "cli_app",
		},
	}
	fs.replaceUserCredentialsErr = errors.New("simulated unique violation")
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_rollback", "ou_bob", "oc_chat", map[string]string{
		"credential_github_pat":      "ghp_x",
		"credential_slack_bot_token": "xoxb_y",
	}))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error", resp.Toast.Type)
	}
	if len(fs.userCredentialsCreated) != 0 {
		t.Errorf("ReplaceUserCredentials failure must rollback — no per-kind writes should leak: %+v", fs.userCredentialsCreated)
	}
	if len(fs.created) != 0 {
		t.Errorf("must NOT re-enqueue inbound when credentials failed to persist")
	}
}

// TestHandleCardAction_CredentialFormSubmitMissingAgentID covers slots
// written before slot.AgentID landed (or any future regression that
// drops it at stash time). The credentials still persist, but the
// re-enqueue is skipped and the user is asked to re-send so the next
// inbound resolves an agent through the normal routing path.
func TestHandleCardAction_CredentialFormSubmitMissingAgentID(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	fs.pendingForms = map[string]store.ClaimedPendingCredentialForm{
		"qkey_no_agent": {
			Slot: store.PendingCredentialFormSlot{
				Qkey:            "qkey_no_agent",
				InitiatorUserID: "user-bob",
				InitiatorOpenID: "ou_bob",
				// AgentID intentionally empty — pre-fix slot shape.
				RawQuery:  "x",
				ExpiresAt: time.Now().UTC().Add(time.Hour),
			},
			ConversationID: "conv-na",
			ExternalChatID: "oc_chat",
			SourceAppID:    "cli_app",
		},
	}
	mgr, _ := NewManager(Options{
		Store:            fs,
		Secrets:          inboundFakeDecrypter{},
		DefaultSharedBot: DefaultSharedBotConfig{AppID: "cli_shared", AppSecret: "sec"},
	})
	resp := mgr.handleCardAction(context.Background(), "cli_app", buildCredentialFormEvent("qkey_no_agent", "ou_bob", "oc_chat", map[string]string{
		"credential_github_pat": "ghp_x",
	}))
	if resp.Toast.Type != "error" {
		t.Errorf("toast type = %q, want error (missing agent_id)", resp.Toast.Type)
	}
	if !strings.Contains(resp.Toast.Content, "会话路由丢失") {
		t.Errorf("toast content = %q, want it to mention 会话路由丢失", resp.Toast.Content)
	}
	if len(fs.userCredentialsCreated) == 0 {
		t.Errorf("credentials must STILL be persisted even when agent_id is missing")
	}
	if len(fs.created) != 0 {
		t.Errorf("must NOT re-enqueue inbound when agent_id is missing from the slot")
	}
}

// stubPromptForUserChoiceRouter records SubmitPromptForUserChoice
// invocations for the user-choice handleCardAction tests. A returnErr
// drives the "router rejected" toast branch.
type stubPromptForUserChoiceRouter struct {
	mu        sync.Mutex
	calls     []PromptForUserChoiceDecision
	returnErr error
}

func (s *stubPromptForUserChoiceRouter) SubmitPromptForUserChoice(_ context.Context, decision PromptForUserChoiceDecision) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, decision)
	return s.returnErr
}

// buildUserChoiceEvent assembles the CardActionTriggerEvent shape
// routeUserChoiceSubmit reads: action=ask_user_choice_submit + request_id
// in Action.Value, with per-question picks in Action.FormValue keyed
// "q<idx>". The operator open_id is threaded onto the decision.
func buildUserChoiceEvent(requestID, operatorID string, formValues map[string]any) *callback.CardActionTriggerEvent {
	ev := &callback.CardActionTriggerEvent{Event: &callback.CardActionTriggerRequest{}}
	ev.Event.Action = &callback.CallBackAction{
		Value: map[string]interface{}{
			"action":     "ask_user_choice_submit",
			"request_id": requestID,
		},
		FormValue: map[string]interface{}{},
	}
	for k, v := range formValues {
		ev.Event.Action.FormValue[k] = v
	}
	ev.Event.Operator = &callback.Operator{OpenID: operatorID}
	ev.Event.Context = &callback.Context{OpenMessageID: "om_choice_card"}
	return ev
}

// seedUserChoiceSlot inserts a one-question prompt_for_user_choice slot the
// submit tests resolve by request_id.
func seedUserChoiceSlot(fs *inboundFakeStore, requestID, conversationID, agentRunID string) {
	fs.cardsByPromptForUserChoiceReq[requestID] = store.ConversationInflightCards{
		ConversationID:         conversationID,
		HasPromptForUserChoice: true,
		PromptForUserChoice: store.PromptForUserChoiceInflightSlot{
			RequestID:  requestID,
			AgentRunID: agentRunID,
			Questions: []store.PromptForUserChoiceQuestion{{
				Header:   "Pick",
				Question: "choose one",
				Options: []store.PromptForUserChoiceOption{
					{Label: "选项A"},
					{Label: "选项B"},
				},
			}},
		},
	}
}

// TestHandleCardAction_UserChoiceSubmitFullFlow locks in the happy path:
// the user submits a pick, SubmitPromptForUserChoice receives the paired
// answer, the done card is threaded through response.card (the canonical
// post-callback render), and the prompt_for_user_choice slot is cleared.
func TestHandleCardAction_UserChoiceSubmitFullFlow(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	seedUserChoiceSlot(fs, "req-1", "conv-uc", "run-uc")
	router := &stubPromptForUserChoiceRouter{}
	mgr, err := NewManager(Options{
		Store:                     fs,
		Secrets:                   inboundFakeDecrypter{},
		PromptForUserChoiceRouter: router,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp := mgr.handleCardAction(context.Background(), "cli_app",
		buildUserChoiceEvent("req-1", "ou_picker", map[string]any{"q0": "选项A"}))

	if resp == nil || resp.Toast == nil {
		t.Fatal("nil toast response")
	}
	if resp.Toast.Type != "success" {
		t.Errorf("toast type = %q, want success", resp.Toast.Type)
	}
	if !strings.Contains(resp.Toast.Content, "已记录") {
		t.Errorf("toast content = %q, want it to mention 已记录", resp.Toast.Content)
	}
	// The done card must ride the callback response itself (response.card),
	// the same byte-identical ReplaceCard round-trip the legacy inline path
	// used — a PATCH-only flow snaps the client back to "待回答".
	if resp.Card == nil {
		t.Fatal("resp.Card = nil, want the done-card payload on the callback response")
	}
	if resp.Card.Type != "raw" {
		t.Errorf("resp.Card.Type = %q, want \"raw\"", resp.Card.Type)
	}
	if _, ok := resp.Card.Data.(map[string]any); !ok {
		t.Errorf("resp.Card.Data is %T, want map[string]any", resp.Card.Data)
	}
	if len(router.calls) != 1 {
		t.Fatalf("router calls = %d, want 1", len(router.calls))
	}
	dec := router.calls[0]
	if dec.RequestID != "req-1" {
		t.Errorf("decision RequestID = %q, want req-1", dec.RequestID)
	}
	if dec.OperatorID != "ou_picker" {
		t.Errorf("decision OperatorID = %q, want ou_picker", dec.OperatorID)
	}
	if dec.Cancelled {
		t.Errorf("decision Cancelled = true, want false (an answer was supplied)")
	}
	if len(dec.QuestionAnswers) != 1 || dec.QuestionAnswers[0].Answer != "选项A" || dec.QuestionAnswers[0].Header != "Pick" {
		t.Errorf("decision QuestionAnswers = %+v, want one {Header:Pick, Answer:选项A}", dec.QuestionAnswers)
	}
	// The slot must be cleared as a prompt_for_user_choice clear keyed to
	// the slot's agent_run_id (the optimistic-clear guard).
	if len(fs.permissionClears) != 1 {
		t.Fatalf("slot clears = %d, want 1", len(fs.permissionClears))
	}
	if fs.permissionClears[0].Slot != store.InflightSlotPromptForUserChoice {
		t.Errorf("clear slot kind = %q, want prompt_for_user_choice", fs.permissionClears[0].Slot)
	}
	if fs.permissionClears[0].ExpectedAgentRunID != "run-uc" {
		t.Errorf("clear expected agent_run_id = %q, want run-uc", fs.permissionClears[0].ExpectedAgentRunID)
	}
}

// TestHandleCardAction_UserChoiceSubmitAllBlankCancels confirms an
// all-blank submit is forwarded as a Cancelled decision (stop-signal) and
// surfaces an info "已取消" toast — not a half-answered tool_result.
func TestHandleCardAction_UserChoiceSubmitAllBlankCancels(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	seedUserChoiceSlot(fs, "req-blank", "conv-uc", "run-uc")
	router := &stubPromptForUserChoiceRouter{}
	mgr, _ := NewManager(Options{
		Store:                     fs,
		Secrets:                   inboundFakeDecrypter{},
		PromptForUserChoiceRouter: router,
	})

	resp := mgr.handleCardAction(context.Background(), "cli_app",
		buildUserChoiceEvent("req-blank", "ou_picker", map[string]any{"q0": "   "}))

	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if resp.Toast.Content != "已取消" {
		t.Errorf("toast content = %q, want 已取消", resp.Toast.Content)
	}
	if len(router.calls) != 1 {
		t.Fatalf("router calls = %d, want 1", len(router.calls))
	}
	if !router.calls[0].Cancelled {
		t.Errorf("decision Cancelled = false, want true (all answers blank)")
	}
	if len(fs.permissionClears) != 1 || fs.permissionClears[0].Slot != store.InflightSlotPromptForUserChoice {
		t.Errorf("slot clears = %+v, want one prompt_for_user_choice clear", fs.permissionClears)
	}
}

// TestHandleCardAction_UserChoiceSubmitSlotAlreadyCleared covers the
// "another pod / a prior click resolved this" branch: the request_id
// lookup misses. No router call, no clear, info toast.
func TestHandleCardAction_UserChoiceSubmitSlotAlreadyCleared(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	router := &stubPromptForUserChoiceRouter{}
	mgr, _ := NewManager(Options{
		Store:                     fs,
		Secrets:                   inboundFakeDecrypter{},
		PromptForUserChoiceRouter: router,
	})

	resp := mgr.handleCardAction(context.Background(), "cli_app",
		buildUserChoiceEvent("req-missing", "ou_picker", map[string]any{"q0": "选项A"}))

	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if len(router.calls) != 0 {
		t.Errorf("router calls = %d, want 0 on missing slot", len(router.calls))
	}
	if len(fs.permissionClears) != 0 {
		t.Errorf("clears = %d, want 0", len(fs.permissionClears))
	}
}

// TestHandleCardAction_UserChoiceSubmitMissingRequestID confirms a submit
// missing request_id (a malformed / pre-form card) gets a generic retry
// toast instead of hanging — and never touches the router.
func TestHandleCardAction_UserChoiceSubmitMissingRequestID(t *testing.T) {
	t.Parallel()
	fs := newInboundFakeStore()
	router := &stubPromptForUserChoiceRouter{}
	mgr, _ := NewManager(Options{
		Store:                     fs,
		Secrets:                   inboundFakeDecrypter{},
		PromptForUserChoiceRouter: router,
	})

	resp := mgr.handleCardAction(context.Background(), "cli_app",
		buildUserChoiceEvent("", "ou_picker", map[string]any{"q0": "选项A"}))

	if resp.Toast.Type != "info" {
		t.Errorf("toast type = %q, want info", resp.Toast.Type)
	}
	if len(router.calls) != 0 {
		t.Errorf("router calls = %d, want 0 on missing request_id", len(router.calls))
	}
}
