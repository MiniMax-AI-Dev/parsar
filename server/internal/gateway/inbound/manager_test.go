package inbound

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

func TestPromptForUserChoiceSecretSummariesAndCustomAnswers(t *testing.T) {
	answers := []PromptForUserChoiceQuestionAnswer{
		{Header: "Token", Answer: "super-secret", IsSecret: true},
		{Header: "Environment", Answer: "staging"},
	}
	summary := summarizePromptForUserChoiceAnswers(answers)
	if strings.Contains(summary, "super-secret") || !strings.Contains(summary, "Token:[REDACTED]") {
		t.Fatalf("secret summary = %q", summary)
	}
	if got := extractPromptForUserChoiceFormAnswer(map[string]any{
		"q0": []any{"unit", "e2e"}, "q0_other": "smoke",
	}, 0, true); got != "unit、e2e、smoke" {
		t.Fatalf("multi-select custom answer = %q", got)
	}
	if got := extractPromptForUserChoiceFormAnswer(map[string]any{
		"q0": "stored", "q0_other": "typed-secret",
	}, 0, false); got != "typed-secret" {
		t.Fatalf("single-select custom answer = %q", got)
	}
}

func strptr(s string) *string { return &s }

func TestNormalizeDomainKeepsWebSocketScheme(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty", raw: "", want: ""},
		{name: "full https", raw: " https://open.feishu.cn/ ", want: "https://open.feishu.cn"},
		{name: "full http", raw: "http://127.0.0.1:8080/", want: "http://127.0.0.1:8080"},
		{name: "bare host", raw: "open.feishu.cn", want: "https://open.feishu.cn"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDomain(tc.raw); got != tc.want {
				t.Fatalf("normalizeDomain(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestInboundEventFromSDKCarriesThreadAndMentionMetadata(t *testing.T) {
	mentionKey := "<at user_id=\"ou_bot\">Parsar</at>"
	received := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{
					OpenId:  strptr("ou_sender"),
					UserId:  strptr("u_sender"),
					UnionId: strptr("on_sender"),
				},
				TenantKey: strptr("tenant-key"),
			},
			Message: &larkim.EventMessage{
				MessageId:   strptr("om_child"),
				RootId:      strptr("om_root"),
				ParentId:    strptr("om_parent"),
				ChatId:      strptr("oc_chat"),
				ChatType:    strptr("group"),
				ThreadId:    strptr("thread-1"),
				MessageType: strptr("text"),
				Content:     strptr(`{"text":"<at user_id=\"ou_bot\">Parsar</at> please help"}`),
				Mentions: []*larkim.MentionEvent{
					{
						Key: strptr(mentionKey),
						Id:  &larkim.UserId{OpenId: strptr("ou_bot")},
					},
				},
			},
		},
	}

	got := inboundEventFromSDK("cli_ws", received)

	if got.AppID != "cli_ws" || got.MessageID != "om_child" {
		t.Fatalf("unexpected ids: %+v", got)
	}
	if got.RootID != "om_root" || got.ParentID != "om_parent" || got.ReplyAnchorMessageID() != "om_child" {
		t.Fatalf("thread ids not preserved or anchor regressed (anchor must be the inbound's own MessageID to avoid Feishu thread-reply fanout): root=%q parent=%q anchor=%q", got.RootID, got.ParentID, got.ReplyAnchorMessageID())
	}
	if got.Text != "please help" {
		t.Fatalf("mention key was not stripped from text: %q", got.Text)
	}
	if len(got.MentionOpenIDs) != 1 || got.MentionOpenIDs[0] != "ou_bot" {
		t.Fatalf("mention open ids = %+v", got.MentionOpenIDs)
	}
	if len(got.MentionKeys) != 1 || got.MentionKeys[0] != mentionKey {
		t.Fatalf("mention keys = %+v", got.MentionKeys)
	}
	keys, _ := got.Metadata["mention_keys"].([]string)
	if len(keys) != 1 || keys[0] != mentionKey {
		t.Fatalf("metadata mention_keys = %+v", got.Metadata["mention_keys"])
	}
}

func TestManager_DefaultSharedBotListUsesEnvCredentialsWithoutAgentConnector(t *testing.T) {
	var (
		mu             sync.Mutex
		sawTokenAppID  string
		sawTokenSecret string
		sentReplyPath  string
		sentReplyBody  []byte
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			sawTokenAppID = body["app_id"]
			sawTokenSecret = body["app_secret"]
			mu.Unlock()
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-default","expire":7200}`)
		case strings.HasSuffix(r.URL.Path, "/im/v1/messages/om_list/reply"):
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			sentReplyPath = r.URL.Path
			sentReplyBody = body
			mu.Unlock()
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_reply"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	st := newInboundFakeStore()
	st.users["on_sender"] = "user-feishu"
	st.sharedAgents = []store.FeishuSharedBotAgent{
		{
			AgentID:       "agent-backend",
			WorkspaceName: "Platform",
			WorkspaceSlug: "platform",
			AgentName:     "Backend Agent",
			AgentSlug:     "backend-agent",
		},
	}
	manager, err := NewManager(Options{
		Store:          st,
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	event := &larkim.P2MessageReceiveV1{Event: &larkim.P2MessageReceiveV1Data{
		Sender: &larkim.EventSender{
			SenderId:  &larkim.UserId{OpenId: strptr("ou_sender"), UserId: strptr("u_sender"), UnionId: strptr("on_sender")},
			TenantKey: strptr("tenant-key"),
		},
		Message: &larkim.EventMessage{
			MessageId:   strptr("om_list"),
			ChatId:      strptr("oc_chat"),
			ChatType:    strptr("p2p"),
			MessageType: strptr("text"),
			Content:     strptr(`{"text":"/list"}`),
		},
	}}
	if err := manager.handleMessage(context.Background(), "cli_default", event); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if sawTokenAppID != "cli_default" || sawTokenSecret != "env-default-secret" {
		t.Fatalf("token exchange credentials = app_id=%q secret=%q, want default env bot", sawTokenAppID, sawTokenSecret)
	}
	if sentReplyPath != "/open-apis/im/v1/messages/om_list/reply" {
		t.Fatalf("reply path = %q", sentReplyPath)
	}
	if !strings.Contains(string(sentReplyBody), "Backend Agent") {
		t.Fatalf("/list reply body did not include selectable agent: %s", string(sentReplyBody))
	}
	if st.getAgentByAppIDCalls != 0 {
		t.Fatalf("default shared bot should not require agents.config lookup, got %d GetAgentByFeishuAppID calls", st.getAgentByAppIDCalls)
	}
	if st.secretPayloadCalls != 0 {
		t.Fatalf("default shared bot should not read app_secret from vault, got %d GetSecretPayload calls", st.secretPayloadCalls)
	}
	if st.sharedListCalls != 1 || st.lastSharedSenderUserID != "user-feishu" {
		t.Fatalf("shared /list calls = %d sender=%q, want 1/user-feishu", st.sharedListCalls, st.lastSharedSenderUserID)
	}
}

type inboundFakeStore struct {
	mu sync.Mutex

	websocketAgents []store.FeishuAgentRoute
	agentsByAppID   map[string]store.FeishuAgentRoute
	agentsByID      map[string]store.FeishuAgentRoute
	sharedAgents    []store.FeishuSharedBotAgent
	selections      map[string]string
	users           map[string]string
	secrets         map[string]store.SecretPayload
	created         []store.CreateInboundIMMessageInput
	// threadHistory controls HasFeishuThreadInboundHistory's return.
	// Default false (no prior history). Tests covering thread continuation set this
	// to true to assert the mention gate lets follow-ups through without
	// an explicit @mention.
	threadHistory bool

	getAgentByAppIDCalls    int
	secretPayloadCalls      int
	sharedListCalls         int
	lastSharedSenderUserID  string
	lastSharedExcludeAgent  string
	lastSharedRequestedSize int32

	// P3 permission-card callback state. Tests populate cardsByPermReq
	// to make FindConversationByPermissionRequestID return a slot;
	// the slice records every ClearConversationInflightSlot call so
	// tests can assert the verdict reached the slot.
	cardsByPermReq                map[string]store.ConversationInflightCards
	cardsByPromptForUserChoiceReq map[string]store.ConversationInflightCards
	inflightByConv                map[string]store.ConversationInflightCards
	pendingAskByChat              map[string]inboundFakeChatAsk
	permissionClears              []inboundFakePermissionClear

	// P4 typing-reaction state. handleMessage fires a goroutine that
	// calls RecordFeishuInboundReaction after the Feishu API returns;
	// none of the tests in this package exercise the live Feishu call
	// today, so the goroutine never lands and this slice stays empty.
	// We capture per-call inputs anyway so a future test that injects
	// a fake Feishu client can assert the persistence step.
	recordedReactions []store.RecordFeishuInboundReactionInput

	// ADR-004 credential-form auto-retry state. pendingForms is the
	// slot stash keyed by qkey (the production schema lives at
	// conversations.metadata.gateway_inflight.pending_credential_form
	// — see store/pending_credential_form.go); submit handler claims
	// from here. userCredentialsCreated captures every write the
	// handler issues so tests can assert exact inputs;
	// pendingFormsClaimed records the qkeys the atomic-claim
	// consumed; pendingFormsClearedByConv records the inbound
	// path's stale-draft cleanup; replaceUserCredentialsCalls
	// captures the tx-wrapped multi-kind write the post-review
	// submit handler uses.
	pendingForms                map[string]store.ClaimedPendingCredentialForm
	pendingFormsClaimed         []string
	pendingFormsClearedByConv   []string
	userCredentialsCreated      []store.CreateUserCredentialInput
	replaceUserCredentialsCalls []replaceUserCredentialsCall
	// replaceUserCredentialsErr forces ReplaceUserCredentials to fail
	// so tests can exercise the partial-failure rollback path without
	// standing up a real DB tx.
	replaceUserCredentialsErr error
	// replaceUserCredentialsReplaced lets tests pre-seed which kinds the
	// store reports as having replaced an existing credential. The fake
	// matches on Kind and stamps Replaced=true in the result so the
	// submit handler's "Replaced N" branch can be exercised.
	replaceUserCredentialsReplaced map[string]bool
	conversations                  map[string]store.ConversationRead

	// agentNameByConversation feeds ResolveAgentNameForConversation
	// for the title-fallback path used by NoticeCard / credential
	// patch / permission result patches. Tests that don't care leave
	// the map nil and the stub returns "" (the production fallback to
	// FeishuCardTitle in the builder).
	agentNameByConversation map[string]string
}

// replaceUserCredentialsCall captures one ReplaceUserCredentials
// invocation so tests can assert on the inputs (user_id + the full
// slice of credential inputs).
type replaceUserCredentialsCall struct {
	UserID string
	Inputs []store.CreateUserCredentialInput
}

func newInboundFakeStore() *inboundFakeStore {
	return &inboundFakeStore{
		agentsByAppID:                 make(map[string]store.FeishuAgentRoute),
		agentsByID:                    make(map[string]store.FeishuAgentRoute),
		selections:                    make(map[string]string),
		users:                         make(map[string]string),
		secrets:                       make(map[string]store.SecretPayload),
		cardsByPermReq:                make(map[string]store.ConversationInflightCards),
		cardsByPromptForUserChoiceReq: make(map[string]store.ConversationInflightCards),
		inflightByConv:                make(map[string]store.ConversationInflightCards),
		pendingAskByChat:              make(map[string]inboundFakeChatAsk),
	}
}

func (f *inboundFakeStore) ListFeishuWebSocketAgents(context.Context) ([]store.FeishuAgentRoute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]store.FeishuAgentRoute(nil), f.websocketAgents...), nil
}

func (f *inboundFakeStore) GetAgentByFeishuAppID(_ context.Context, appID string) (store.FeishuAgentRoute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getAgentByAppIDCalls++
	route, ok := f.agentsByAppID[strings.TrimSpace(appID)]
	if !ok {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return route, nil
}

func (f *inboundFakeStore) GetAgentByID(_ context.Context, agentID string) (store.FeishuAgentRoute, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	route, ok := f.agentsByID[strings.TrimSpace(agentID)]
	if !ok {
		return store.FeishuAgentRoute{}, store.ErrUnknownFeishuAgent
	}
	return route, nil
}

func (f *inboundFakeStore) ListFeishuSharedBotAgents(_ context.Context, senderUserID string, excludeAgentID string, limit int32) ([]store.FeishuSharedBotAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sharedListCalls++
	f.lastSharedSenderUserID = senderUserID
	f.lastSharedExcludeAgent = excludeAgentID
	f.lastSharedRequestedSize = limit
	return append([]store.FeishuSharedBotAgent(nil), f.sharedAgents...), nil
}

func (f *inboundFakeStore) UpsertGatewaySessionSelection(_ context.Context, input store.GatewaySessionSelectionInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.selections[selectionTestKey(input.Platform, input.ExternalID, input.ExternalThreadID)] = input.AgentID
	return nil
}

func (f *inboundFakeStore) GetGatewaySessionSelection(_ context.Context, platform, externalID, externalThreadID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	selected, ok := f.selections[selectionTestKey(platform, externalID, externalThreadID)]
	if !ok {
		return "", store.ErrUnknownGatewaySessionSelection
	}
	return selected, nil
}

func (f *inboundFakeStore) ClearGatewaySessionSelection(_ context.Context, platform, externalID, externalThreadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.selections, selectionTestKey(platform, externalID, externalThreadID))
	return nil
}

func (f *inboundFakeStore) FindUserIDByPlatformSubject(_ context.Context, _ string, subject string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	userID, ok := f.users[strings.TrimSpace(subject)]
	if !ok {
		return "", store.ErrUnknownPlatformUser
	}
	return userID, nil
}

func (f *inboundFakeStore) IsActiveWorkspaceMember(context.Context, string, string) (bool, error) {
	return false, nil
}

func (f *inboundFakeStore) GetWorkspaceVisibility(context.Context, string) (string, error) {
	return "private", nil
}

func (f *inboundFakeStore) ListActiveWorkspaceOwnerNames(context.Context, string, int32) ([]string, error) {
	return nil, nil
}

func (f *inboundFakeStore) GetSecretPayload(_ context.Context, _, secretID string) (store.SecretPayload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.secretPayloadCalls++
	payload, ok := f.secrets[strings.TrimSpace(secretID)]
	if !ok {
		return store.SecretPayload{}, errors.New("secret not found")
	}
	return payload, nil
}

func (f *inboundFakeStore) CreateInboundIMMessage(_ context.Context, input store.CreateInboundIMMessageInput) (store.CreateInboundIMMessageResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, input)
	return store.CreateInboundIMMessageResult{MessageID: "msg-created", WorkspaceID: "ws-created"}, nil
}

// --- P3 permission-card callback stubs ---
// inboundFakeStore implements just enough of the inflight Storer
// slice for handleCardAction unit tests. Tests that don't exercise
// the callback path get default-zero responses (ErrUnknownConversation
// on the lookup) which matches the "slot already cleared" outcome.

func (f *inboundFakeStore) FindConversationByPermissionRequestID(_ context.Context, permissionRequestID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.cardsByPermReq[permissionRequestID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *inboundFakeStore) FindConversationByPromptForUserChoiceRequestID(_ context.Context, requestID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.cardsByPromptForUserChoiceReq[requestID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *inboundFakeStore) GetConversationInflightCards(_ context.Context, conversationID string) (store.ConversationInflightCards, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.inflightByConv == nil {
		return store.ConversationInflightCards{}, store.ErrUnknownConversation
	}
	if got, ok := f.inflightByConv[conversationID]; ok {
		return got, nil
	}
	return store.ConversationInflightCards{}, store.ErrUnknownConversation
}

func (f *inboundFakeStore) FindPendingAskByChat(_ context.Context, gateway, externalChatID string) (string, store.PromptForUserChoiceInflightSlot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pendingAskByChat == nil {
		return "", store.PromptForUserChoiceInflightSlot{}, store.ErrUnknownConversation
	}
	key := gateway + "|" + externalChatID
	if hit, ok := f.pendingAskByChat[key]; ok {
		return hit.convID, hit.slot, nil
	}
	return "", store.PromptForUserChoiceInflightSlot{}, store.ErrUnknownConversation
}

func (f *inboundFakeStore) ClearConversationInflightSlot(_ context.Context, conversationID string, slot store.InflightSlotKind, expectedAgentRunID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.permissionClears = append(f.permissionClears, inboundFakePermissionClear{ConversationID: conversationID, Slot: slot, ExpectedAgentRunID: expectedAgentRunID})
	return nil
}

func (f *inboundFakeStore) RecordFeishuInboundReaction(_ context.Context, input store.RecordFeishuInboundReactionInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recordedReactions = append(f.recordedReactions, input)
	return nil
}

// ADR-004 credential-form auto-retry: the fake records what the handler
// asked for but does not maintain a full slot lifecycle. Tests that
// exercise the submit path set up specific expectations via direct
// field manipulation (see handle_card_action_test.go).
func (f *inboundFakeStore) ClearPendingCredentialFormSlotByConversation(_ context.Context, conversationID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pendingFormsClearedByConv = append(f.pendingFormsClearedByConv, conversationID)
	return nil
}

// ClaimPendingCredentialFormSlot emulates the production CTE
// "UPDATE … RETURNING" primitive: the first caller for a qkey
// returns the claimed slot + records the consume; a sibling caller
// racing the same key sees ErrPendingCredentialFormNotFound.
// Implemented under the same mutex so concurrent test goroutines
// hit the same race semantics the production atomic claim provides.
func (f *inboundFakeStore) ClaimPendingCredentialFormSlot(_ context.Context, qkey string) (store.ClaimedPendingCredentialForm, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	got, ok := f.pendingForms[qkey]
	if !ok {
		return store.ClaimedPendingCredentialForm{}, store.ErrPendingCredentialFormNotFound
	}
	delete(f.pendingForms, qkey)
	f.pendingFormsClaimed = append(f.pendingFormsClaimed, qkey)
	return got, nil
}

func (f *inboundFakeStore) CreateUserCredential(_ context.Context, input store.CreateUserCredentialInput) (store.UserCredentialRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCredentialsCreated = append(f.userCredentialsCreated, input)
	return store.UserCredentialRead{
		ID:     "uc-" + input.Kind,
		UserID: input.UserID,
		Kind:   input.Kind,
	}, nil
}

// ReplaceUserCredentials emulates the tx-wrapped multi-kind write the
// submit handler now uses. We capture the inputs + return per-slot
// Replaced markers seeded by replaceUserCredentialsReplaced so tests
// can exercise both "fresh writes" and "replaced existing" toast
// branches. Setting replaceUserCredentialsErr forces the whole batch
// to fail (and stamps nothing into userCredentialsCreated) — exactly
// what a tx-wrapped real store does on partial conflict.
func (f *inboundFakeStore) ReplaceUserCredentials(_ context.Context, userID string, inputs []store.CreateUserCredentialInput) ([]store.ReplaceUserCredentialResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replaceUserCredentialsCalls = append(f.replaceUserCredentialsCalls, replaceUserCredentialsCall{
		UserID: userID,
		Inputs: append([]store.CreateUserCredentialInput(nil), inputs...),
	})
	if f.replaceUserCredentialsErr != nil {
		return nil, f.replaceUserCredentialsErr
	}
	for _, in := range inputs {
		// Mirror what a successful tx would have observed: a row landed
		// for every kind. We append into userCredentialsCreated so
		// existing assertions that check writes (legacy tests) keep
		// working unchanged.
		f.userCredentialsCreated = append(f.userCredentialsCreated, in)
	}
	out := make([]store.ReplaceUserCredentialResult, 0, len(inputs))
	for _, in := range inputs {
		out = append(out, store.ReplaceUserCredentialResult{
			Kind:     in.Kind,
			Replaced: f.replaceUserCredentialsReplaced[in.Kind],
			Credential: store.UserCredentialRead{
				ID:     "uc-" + in.Kind,
				UserID: userID,
				Kind:   in.Kind,
			},
		})
	}
	return out, nil
}

func (f *inboundFakeStore) GetConversation(_ context.Context, conversationID string) (store.ConversationRead, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if got, ok := f.conversations[conversationID]; ok {
		return got, nil
	}
	return store.ConversationRead{}, store.ErrUnknownConversation
}

// FindConversationByExternalRef + CancelAllInflightForConversation are
// stubs that satisfy the Storer interface so existing test setups
// don't need to wire the /cancel command path. Tests that exercise
// /cancel can override these fields directly via the same lock.
func (f *inboundFakeStore) FindConversationByExternalRef(_ context.Context, _, _, _ string) (string, error) {
	return "", store.ErrUnknownConversation
}

func (f *inboundFakeStore) CancelAllInflightForConversation(_ context.Context, _, _ string) ([]store.SupersededRun, error) {
	return nil, nil
}

// HasFeishuThreadInboundHistory returns the value programmed by the test
// (default false). Tests that exercise the "thread continuation skips @-mention" rule
// flip this field via the same mutex used elsewhere.
func (f *inboundFakeStore) HasFeishuThreadInboundHistory(_ context.Context, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threadHistory, nil
}

func (f *inboundFakeStore) HasThreadInboundHistory(_ context.Context, _, _, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.threadHistory, nil
}

// ResolveAgentNameForConversation is the no-op stub for the title-
// fallback path used by patch + immediate-reply notice paths.
// Tests that care can populate inboundFakeStore.agentNameByConversation;
// the rest get an empty name and rely on the FeishuCardTitle fallback
// in the builders.
func (f *inboundFakeStore) ResolveAgentNameForConversation(_ context.Context, conversationID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.agentNameByConversation == nil {
		return "", nil
	}
	return f.agentNameByConversation[conversationID], nil
}

type inboundFakeChatAsk struct {
	convID string
	slot   store.PromptForUserChoiceInflightSlot
}

type inboundFakePermissionClear struct {
	ConversationID     string
	Slot               store.InflightSlotKind
	ExpectedAgentRunID string
}

type inboundFakeDecrypter struct{}

func (inboundFakeDecrypter) Decrypt(envelopeJSON []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(envelopeJSON, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Encrypt round-trips the payload as JSON so the fake stays symmetric
// with Decrypt — production secrets.Service wraps an AES-GCM envelope
// here but the manager only reads bytes in/out, so JSON suffices for
// unit tests.
func (inboundFakeDecrypter) Encrypt(payload map[string]any) ([]byte, error) {
	return json.Marshal(payload)
}

func selectionTestKey(platform, externalID, externalThreadID string) string {
	return strings.Join([]string{platform, externalID, externalThreadID}, "|")
}

var _ Storer = (*inboundFakeStore)(nil)
var _ SecretDecrypter = inboundFakeDecrypter{}

// TestIsGroupMessageWithoutBotMention exercises the websocket-side mention
// gate. The HTTP webhook path has an end-to-end equivalent in
// dev/routes_test.go; this table-driven test focuses on the decision
// matrix in isolation so a regression here surfaces fast.
func TestIsGroupMessageWithoutBotMention(t *testing.T) {
	cfgWithBot := []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_x","bot_open_id":"ou_bot_self"}}}`)
	cfgWithoutBot := []byte(`{"connectors":{"feishu":{"enabled":true,"app_id":"cli_x"}}}`)

	cases := []struct {
		name          string
		config        []byte
		event         gateway.FeishuInboundEvent
		threadHistory bool
		wantDropped   bool
	}{
		{
			name:   "p2p always passes",
			config: cfgWithoutBot,
			event: gateway.FeishuInboundEvent{
				ChatType: "p2p",
				ChatID:   "oc_dm",
			},
			wantDropped: false,
		},
		{
			name:   "group mention of bot passes",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:       "group",
				ChatID:         "oc_g",
				MentionOpenIDs: []string{"ou_bot_self"},
			},
			wantDropped: false,
		},
		{
			name:   "group mention of another user drops",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:       "group",
				ChatID:         "oc_g",
				MentionOpenIDs: []string{"ou_alice"},
			},
			wantDropped: true,
		},
		{
			name:   "group plain message without mention drops",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType: "group",
				ChatID:   "oc_g",
			},
			wantDropped: true,
		},
		{
			name:   "group thread follow-up with prior history passes",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:  "group",
				ChatID:    "oc_g",
				MessageID: "om_reply_inside_thread",
				RootID:    "om_thread_root",
			},
			threadHistory: true,
			wantDropped:   false,
		},
		{
			name:   "group thread first message without history drops",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:  "group",
				ChatID:    "oc_g",
				MessageID: "om_reply_inside_thread",
				RootID:    "om_thread_root",
			},
			threadHistory: false,
			wantDropped:   true,
		},
		{
			name:   "group without bot_open_id drops every group message",
			config: cfgWithoutBot,
			event: gateway.FeishuInboundEvent{
				ChatType:       "group",
				ChatID:         "oc_g",
				MentionOpenIDs: []string{"ou_someone"},
			},
			wantDropped: true,
		},
		{
			name:   "bot sender in group passes without explicit mention",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:   "group",
				ChatID:     "oc_g",
				SenderType: "app",
			},
			wantDropped: false,
		},
		{
			name:   "bot sender in group passes even when mentioning someone else",
			config: cfgWithBot,
			event: gateway.FeishuInboundEvent{
				ChatType:       "group",
				ChatID:         "oc_g",
				SenderType:     "app",
				MentionOpenIDs: []string{"ou_alice"},
			},
			wantDropped: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &inboundFakeStore{threadHistory: tc.threadHistory}
			got := isGroupMessageWithoutBotMention(context.Background(), store, tc.config, tc.event)
			if got != tc.wantDropped {
				t.Fatalf("isGroupMessageWithoutBotMention() = %v, want %v", got, tc.wantDropped)
			}
		})
	}
}

// TestEnrichInboundAttachments_DownloadsAndEncodes drives a real
// Manager + httptest Feishu server through one image_key download and
// asserts the resulting metadata round-trips through DecodeMessageAttachments
// — the same path GetAgentRunInvocation uses to populate PromptInput
// downstream. This is the closest we can get to an end-to-end shape
// test without standing up a real Postgres.
func TestEnrichInboundAttachments_DownloadsAndEncodes(t *testing.T) {
	const wantBody = "\x89PNG\r\n\x1a\nFAKE-PIXEL-DATA"
	var (
		mu        sync.Mutex
		gotPaths  []string
		gotAppID  string
		gotSecret string
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			gotAppID = body["app_id"]
			gotSecret = body["app_secret"]
			mu.Unlock()
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-img","expire":7200}`)
		case strings.Contains(r.URL.Path, "/resources/"):
			mu.Lock()
			gotPaths = append(gotPaths, r.URL.Path+"?"+r.URL.RawQuery)
			mu.Unlock()
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, wantBody)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	manager, err := NewManager(Options{
		Store:          newInboundFakeStore(),
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Use the default-shared-bot route so resolveImmediateReplyCredentials
	// picks up the env-default credentials we configured above —
	// avoids dragging the per-Agent secret vault path into a shape
	// test that's specifically about the download → encode flow.
	host, _ := manager.defaultSharedRouteAndConfig()
	agent := routeFromStore(host)
	// The default bot route uses the configured AppID directly so the
	// resolver picks the env-default credentials path. Stuff inbound
	// with the same AppID so isDefaultSharedBotApp() agrees.
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_img_msg",
		ChatID:    "oc_chat",
		ChatType:  "p2p",
		Metadata: map[string]any{
			"mention_keys": []string{},
			"image_keys":   []string{"img_v3_001", "img_v3_002"},
		},
	}

	manager.enrichInboundAttachments(context.Background(), agent, &inbound)

	mu.Lock()
	if gotAppID != "cli_default" || gotSecret != "env-default-secret" {
		t.Fatalf("token exchange creds = app_id=%q secret=%q", gotAppID, gotSecret)
	}
	if len(gotPaths) != 2 {
		t.Fatalf("expected 2 resource downloads, got %d: %v", len(gotPaths), gotPaths)
	}
	for _, p := range gotPaths {
		if !strings.Contains(p, "/om_img_msg/resources/img_v3_") || !strings.Contains(p, "type=image") {
			t.Errorf("unexpected resource path %q", p)
		}
	}
	mu.Unlock()

	if _, present := inbound.Metadata["image_keys"]; present {
		t.Errorf("image_keys not cleared after enrichment: %v", inbound.Metadata)
	}
	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 2 {
		t.Fatalf("decoded attachments = %d, want 2: %#v", len(attachments), attachments)
	}
	for i, att := range attachments {
		if att.Kind != "image" {
			t.Errorf("attachment %d kind = %q, want image", i, att.Kind)
		}
		if att.MIME != "image/png" {
			t.Errorf("attachment %d mime = %q, want image/png", i, att.MIME)
		}
		if att.Size != len(wantBody) {
			t.Errorf("attachment %d size = %d, want %d", i, att.Size, len(wantBody))
		}
		decoded, err := base64.StdEncoding.DecodeString(att.DataBase64)
		if err != nil {
			t.Errorf("attachment %d base64 decode: %v", i, err)
			continue
		}
		if string(decoded) != wantBody {
			t.Errorf("attachment %d data mismatch: %q", i, decoded)
		}
	}
}

// TestEnrichInboundAttachments_NoImageKeysIsNoop guards the cheap
// fast-path: a text-only inbound must NOT hit the upstream Feishu API
// (no token exchange, no resource fetch) just because the helper was
// invoked. Saves both latency and tenant_access_token rate-limit
// budget on the common case.
func TestEnrichInboundAttachments_NoImageKeysIsNoop(t *testing.T) {
	var calls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, "unexpected upstream call", http.StatusTeapot)
	}))
	t.Cleanup(upstream.Close)

	manager, err := NewManager(Options{
		Store:          newInboundFakeStore(),
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host, _ := manager.defaultSharedRouteAndConfig()
	agent := routeFromStore(host)
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_text_only",
		Metadata: map[string]any{
			"mention_keys": []string{},
		},
	}
	manager.enrichInboundAttachments(context.Background(), agent, &inbound)
	if calls != 0 {
		t.Fatalf("enrichInboundAttachments must not hit upstream for text-only inbound, saw %d calls", calls)
	}
	if _, present := inbound.Metadata["attachments"]; present {
		t.Errorf("attachments should not be set when there are no image_keys: %v", inbound.Metadata)
	}
}

// TestEnrichInboundAttachments_DownloadFailureSkipsButContinues confirms
// the helper is best-effort: one failing image_key logs and skips,
// but a second successful key still lands in metadata. The user's
// text alone still drives the run.
func TestEnrichInboundAttachments_DownloadFailureSkipsButContinues(t *testing.T) {
	var seenKeys []string
	var mu sync.Mutex
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-img","expire":7200}`)
		case strings.Contains(r.URL.Path, "/resources/"):
			mu.Lock()
			seenKeys = append(seenKeys, r.URL.Path)
			mu.Unlock()
			if strings.Contains(r.URL.Path, "boom") {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "OK-BYTES")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(upstream.Close)

	manager, err := NewManager(Options{
		Store:          newInboundFakeStore(),
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host, _ := manager.defaultSharedRouteAndConfig()
	agent := routeFromStore(host)
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_mixed",
		Metadata: map[string]any{
			"image_keys": []string{"img_ok", "img_boom"},
		},
	}
	manager.enrichInboundAttachments(context.Background(), agent, &inbound)

	mu.Lock()
	defer mu.Unlock()
	if len(seenKeys) != 2 {
		t.Fatalf("expected 2 download attempts, got %d: %v", len(seenKeys), seenKeys)
	}
	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 1 || string(mustB64(attachments[0].DataBase64)) != "OK-BYTES" {
		t.Fatalf("expected the one successful attachment to survive, got %#v", attachments)
	}
}

func mustB64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// newQuoteChainManager builds a Manager wired to a fake Feishu API that
// serves a graph of messages keyed by message_id. parent_id on each
// entry chains the walk. Returns the manager, the agent to pass into
// helpers, and a counter of GET hits so tests can assert depth caps.
func newQuoteChainManager(t *testing.T, msgs map[string]map[string]string) (*Manager, gateway.FeishuRouteAgent, *int32, *httptest.Server) {
	t.Helper()
	var getCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-quote","expire":7200}`)
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/resources/") {
			// quoteChainImageBody is the canonical fake payload — small
			// enough that tests don't pay memory cost but distinguishable
			// from PNG signatures of the legacy enrich tests.
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "QUOTE-CHAIN-PIXEL-BYTES")
			return
		}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/") {
			atomic.AddInt32(&getCalls, 1)
			id := strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/")
			entry, ok := msgs[id]
			if !ok {
				_, _ = io.WriteString(w, `{"code":230020,"msg":"not found","data":{}}`)
				return
			}
			payload := map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"items": []map[string]any{
						{
							"message_id": id,
							"msg_type":   entry["msg_type"],
							"parent_id":  entry["parent_id"],
							"body": map[string]any{
								"content": entry["content"],
							},
						},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(payload)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	manager, err := NewManager(Options{
		Store:          newInboundFakeStore(),
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host, _ := manager.defaultSharedRouteAndConfig()
	agent := routeFromStore(host)
	return manager, agent, &getCalls, upstream
}

func TestQuotedChainText_PostParent(t *testing.T) {
	postBody := `{"zh_cn":{"title":"alert","content":[[{"tag":"text","text":"P0 panic on weaver-npc"}]]}}`
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_parent": {
			"msg_type": "post",
			"content":  postBody,
		},
	})

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_parent",
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Quoted message]") || !strings.Contains(got, "P0 panic on weaver-npc") || !strings.Contains(got, "[/Quoted message]") {
		t.Errorf("quoted text malformed: %q", got)
	}
	if *calls != 1 {
		t.Errorf("expected 1 GET, got %d", *calls)
	}
}

func TestQuotedChainText_NoQuoteSkips(t *testing.T) {
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_lone",
		ChatID:    "oc_chat",
		ChatType:  "group",
		// no ParentID / no RootID
	}
	if got := manager.quotedChainText(context.Background(), agent, &inbound); got != "" {
		t.Errorf("expected empty (no parent), got %q", got)
	}
	if *calls != 0 {
		t.Errorf("expected 0 GET (skip when no parent), got %d", *calls)
	}
}

func TestQuotedChainText_NoParentSkips(t *testing.T) {
	// RootID alone (i.e. a non-reply message inside a thread) must NOT
	// trigger a chain walk. Pre-fix the manager fell back to RootID and
	// burned a Feishu GET on every thread continuation.
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_root": {
			"msg_type": "text",
			"content":  `{"text":"root only"}`,
		},
	})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		RootID:    "om_root",
	}
	if got := manager.quotedChainText(context.Background(), agent, &inbound); got != "" {
		t.Errorf("RootID alone must not trigger chain walk, got %q", got)
	}
	if *calls != 0 {
		t.Errorf("expected 0 GETs when ParentID is empty, got %d", *calls)
	}
}

func TestQuotedChainText_InteractiveAncestorRendered(t *testing.T) {
	// user → text P1 → interactive card P2 → text P3. All three must
	// appear in the rendered prefix: the card body lands verbatim
	// (raw-JSON fallback for interactive matches the P2P inbound path).
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_p1": {
			"msg_type":  "text",
			"content":   `{"text":"closest"}`,
			"parent_id": "om_p2",
		},
		"om_p2": {
			"msg_type":  "interactive",
			"content":   `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"alert details"}]}}`,
			"parent_id": "om_p3",
		},
		"om_p3": {
			"msg_type": "text",
			"content":  `{"text":"deepest"}`,
		},
	})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_p1",
	}
	got := manager.quotedChainText(context.Background(), agent, &inbound)
	for _, want := range []string{"closest", "deepest", "alert details"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in chain: %q", want, got)
		}
	}
	if *calls != 3 {
		t.Errorf("expected 3 GETs (walk full chain), got %d", *calls)
	}
}

func TestQuotedChainText_SizeCap(t *testing.T) {
	// A 5-hop chain of big posts must be truncated, not unbounded.
	big := strings.Repeat("X", 20*1024)
	chain := map[string]map[string]string{}
	for i := 1; i <= 5; i++ {
		entry := map[string]string{
			"msg_type": "text",
			"content":  fmt.Sprintf(`{"text":"%s-%d"}`, big, i),
		}
		if i < 5 {
			entry["parent_id"] = fmt.Sprintf("om_l%d", i+1)
		}
		chain[fmt.Sprintf("om_l%d", i)] = entry
	}
	manager, agent, _, _ := newQuoteChainManager(t, chain)
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_l1",
	}
	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if len(got) > inboundQuoteChainMaxBytes+200 {
		t.Errorf("prefix not truncated: len=%d", len(got))
	}
	if !strings.Contains(got, "earlier quoted context truncated") {
		t.Errorf("missing truncation marker: %q", got[max(0, len(got)-200):])
	}
}

func TestQuotedChainText_InteractiveParentRenderedAsRawJSON(t *testing.T) {
	// Lone interactive parent: the card body lands verbatim inside the
	// quoted-message envelope. Matches the P2P inbound path so a card
	// reached via reply produces the same prompt shape as a card sent
	// directly to the bot.
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_card": {
			"msg_type": "interactive",
			"content":  `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"alert details"}]}}`,
		},
	})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_card",
	}
	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Quoted message]") {
		t.Errorf("expected quoted-message envelope, got %q", got)
	}
	if !strings.Contains(got, "alert details") {
		t.Errorf("card body missing from prompt: %q", got)
	}
	if *calls != 1 {
		t.Errorf("expected 1 GET, got %d", *calls)
	}
}

func TestQuotedChainText_RecursesUpChain(t *testing.T) {
	// om_user → om_p1 (text "level1") → om_p2 (text "level2") → om_p3 (text "level3")
	manager, agent, calls, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_p1": {
			"msg_type":  "text",
			"content":   `{"text":"level1"}`,
			"parent_id": "om_p2",
		},
		"om_p2": {
			"msg_type":  "text",
			"content":   `{"text":"level2"}`,
			"parent_id": "om_p3",
		},
		"om_p3": {
			"msg_type": "text",
			"content":  `{"text":"level3"}`,
		},
	})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_p1",
	}
	got := manager.quotedChainText(context.Background(), agent, &inbound)
	// Deepest ancestor must appear first (the prefix), most recent last.
	idx1 := strings.Index(got, "level1")
	idx2 := strings.Index(got, "level2")
	idx3 := strings.Index(got, "level3")
	if idx1 < 0 || idx2 < 0 || idx3 < 0 {
		t.Fatalf("missing chain levels: %q", got)
	}
	if !(idx3 < idx2 && idx2 < idx1) {
		t.Errorf("chain order wrong; want deepest first. got %q", got)
	}
	if *calls != 3 {
		t.Errorf("expected 3 GETs for 3-deep chain, got %d", *calls)
	}
}

func TestQuotedChainText_DepthCap(t *testing.T) {
	// 7-deep chain — only the first 5 hops should be fetched
	// (inboundQuoteChainMaxDepth = 5). The 6th GET must not fire.
	chain := map[string]map[string]string{}
	for i := 1; i <= 7; i++ {
		entry := map[string]string{
			"msg_type": "text",
			"content":  fmt.Sprintf(`{"text":"L%d"}`, i),
		}
		if i < 7 {
			entry["parent_id"] = fmt.Sprintf("om_l%d", i+1)
		}
		chain[fmt.Sprintf("om_l%d", i)] = entry
	}
	manager, agent, calls, _ := newQuoteChainManager(t, chain)
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_l1",
	}
	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if *calls != 5 {
		t.Errorf("expected exactly 5 GETs at depth cap, got %d", *calls)
	}
	// L6/L7 must never appear; L1..L5 must.
	for _, want := range []string{"L1", "L2", "L3", "L4", "L5"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in capped chain: %q", want, got)
		}
	}
	for _, dont := range []string{"L6", "L7"} {
		if strings.Contains(got, dont) {
			t.Errorf("unexpected %s past depth cap: %q", dont, got)
		}
	}
}

func TestQuotedChainText_GetFailureDegrades(t *testing.T) {
	// Parent message_id doesn't exist in the fake graph → upstream
	// returns 230020 → quotedChainText must return "" (degrade silently).
	manager, agent, _, _ := newQuoteChainManager(t, map[string]map[string]string{})
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_deleted",
	}
	if got := manager.quotedChainText(context.Background(), agent, &inbound); got != "" {
		t.Errorf("missing parent must degrade to empty quoted text, got %q", got)
	}
}

// TestQuotedChainText_ImageParentDownloadsAndPlacesPlaceholder is the
// core fix for the "reply to a screenshot" case: the parent hop is a
// pure image message. We must (a) NOT skip it, (b) emit an [image:N]
// placeholder so the LLM knows which attachment belongs to the parent,
// and (c) download the bytes against the PARENT's message_id (Feishu
// validates the file_key ↔ message_id binding).
func TestQuotedChainText_ImageParentDownloadsAndPlacesPlaceholder(t *testing.T) {
	manager, agent, _, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_parent_img": {
			"msg_type": "image",
			"content":  `{"image_key":"img_p1"}`,
		},
	})

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_parent_img",
		Metadata:  map[string]any{},
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Quoted message]") {
		t.Errorf("missing [Quoted message] wrapper: %q", got)
	}
	if !strings.Contains(got, "[image:1]") {
		t.Errorf("missing [image:1] placeholder: %q", got)
	}

	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment from parent image, got %d: %#v", len(attachments), attachments)
	}
	if attachments[0].Kind != "image" || attachments[0].MIME != "image/png" {
		t.Errorf("attachment shape wrong: %#v", attachments[0])
	}
}

// TestQuotedChainText_PostWithImgEmbedsPlaceholder covers the post-with-
// embedded-img case: the parent is a rich-text post that has both text
// and one or more <img> nodes. text and [image:N] must coexist inside
// the same [Quoted message] block.
func TestQuotedChainText_PostWithImgEmbedsPlaceholder(t *testing.T) {
	postWithImg := `{"zh_cn":{"title":"t","content":[
		[{"tag":"text","text":"check this screenshot"},{"tag":"img","image_key":"img_pa"}]
	]}}`
	manager, agent, _, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_parent_post": {
			"msg_type": "post",
			"content":  postWithImg,
		},
	})

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_parent_post",
		Metadata:  map[string]any{},
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "check this screenshot") {
		t.Errorf("missing post text: %q", got)
	}
	if !strings.Contains(got, "[image:1]") {
		t.Errorf("missing [image:1] placeholder: %q", got)
	}

	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 1 {
		t.Fatalf("expected 1 attachment from post img, got %d", len(attachments))
	}
}

// TestQuotedChainText_CapRespectsExistingAttachments protects the per-
// message inboundAttachmentTotalCap from being blown by a chain that
// would otherwise hand the LLM more images than we agreed to ship. The
// inbound already carries inboundAttachmentTotalCap own-images; the
// chain hop must contribute 0 actual downloads + emit an "omitted: over
// cap" marker so the LLM at least knows the parent had an image we
// dropped.
func TestQuotedChainText_CapRespectsExistingAttachments(t *testing.T) {
	manager, agent, _, _ := newQuoteChainManager(t, map[string]map[string]string{
		"om_parent_img": {
			"msg_type": "image",
			"content":  `{"image_key":"img_p_skip"}`,
		},
	})

	// Pre-fill the inbound with cap-worth of attachments so the chain
	// walker has 0 remaining slots.
	preexisting := make([]store.MessageAttachment, inboundAttachmentTotalCap)
	for i := range preexisting {
		preexisting[i] = store.MessageAttachment{Kind: "image", MIME: "image/png", DataBase64: "AAA"}
	}
	encoded := store.EncodeMessageAttachments(preexisting)
	metadata := map[string]any{
		"attachments": anySliceFromMaps(encoded),
	}
	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_parent_img",
		Metadata:  metadata,
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "over per-message cap") {
		t.Errorf("expected over-cap marker in rendered text, got %q", got)
	}
	// The "[image:N]" placeholder must NOT appear because the chain image
	// was dropped — only the omitted marker is correct here.
	if strings.Contains(got, "[image:") {
		t.Errorf("placeholder must not appear when chain image is dropped over cap: %q", got)
	}

	// Attachments slice must remain at the pre-existing cap-fill — no
	// chain image was actually downloaded.
	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != inboundAttachmentTotalCap {
		t.Fatalf("attachment count drifted past cap: got %d, want %d", len(attachments), inboundAttachmentTotalCap)
	}
}

// fakeMergeForwardMsg captures everything the merge_forward tests need
// to stub a single message: the type, the body, optional inline
// sub-messages (carried in GET items[1..]), optional upper_message_id
// for chat-list reverse-lookup, and chat membership for the fallback.
type fakeMergeForwardMsg struct {
	msgType  string
	content  string
	parentID string
	upperID  string
	chatID   string
	subs     []string // child message_ids inlined in GetMessage items[1..]
}

// newMergeForwardManager wires a Manager to a fake Feishu API capable
// of serving both GetMessage (with optional inline sub-items) and
// ListMessagesByChatPage (one chat → newest-first array, single page).
// Returns the manager + agent + a hit counter for the chat-list endpoint.
func newMergeForwardManager(t *testing.T, msgs map[string]fakeMergeForwardMsg, chats map[string][]string) (*Manager, gateway.FeishuRouteAgent, *int32, *int32) {
	t.Helper()
	var getCalls, listCalls int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-mf","expire":7200}`)
			return
		}
		// Resource download: /open-apis/im/v1/messages/{id}/resources/{key}?type=image
		// Must come before the GetMessage matcher — both paths share the
		// /im/v1/messages/ prefix and we don't want the message handler
		// to TrimPrefix and treat "om_x/resources/img_y" as a message id.
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/resources/") {
			w.Header().Set("Content-Type", "image/png")
			_, _ = io.WriteString(w, "MERGE-FORWARD-PIXEL-BYTES")
			return
		}
		// GET single message: /open-apis/im/v1/messages/{id}
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/") {
			atomic.AddInt32(&getCalls, 1)
			id := strings.TrimPrefix(r.URL.Path, "/open-apis/im/v1/messages/")
			m, ok := msgs[id]
			if !ok {
				_, _ = io.WriteString(w, `{"code":230020,"msg":"not found","data":{}}`)
				return
			}
			items := []map[string]any{{
				"message_id":       id,
				"msg_type":         m.msgType,
				"parent_id":        m.parentID,
				"upper_message_id": m.upperID,
				"chat_id":          m.chatID,
				"body":             map[string]any{"content": m.content},
			}}
			for _, subID := range m.subs {
				sub, ok := msgs[subID]
				if !ok {
					continue
				}
				items = append(items, map[string]any{
					"message_id":       subID,
					"msg_type":         sub.msgType,
					"parent_id":        sub.parentID,
					"upper_message_id": sub.upperID,
					"chat_id":          sub.chatID,
					"body":             map[string]any{"content": sub.content},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "msg": "ok",
				"data": map[string]any{"items": items},
			})
			return
		}
		// LIST by chat: /open-apis/im/v1/messages?container_id=oc_xxx&...
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages") {
			atomic.AddInt32(&listCalls, 1)
			chatID := r.URL.Query().Get("container_id")
			ids := chats[chatID]
			items := make([]map[string]any, 0, len(ids))
			for _, id := range ids {
				m, ok := msgs[id]
				if !ok {
					continue
				}
				items = append(items, map[string]any{
					"message_id":       id,
					"msg_type":         m.msgType,
					"parent_id":        m.parentID,
					"upper_message_id": m.upperID,
					"chat_id":          m.chatID,
					"body":             map[string]any{"content": m.content},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"has_more":   false,
					"page_token": "",
					"items":      items,
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(upstream.Close)

	manager, err := NewManager(Options{
		Store:          newInboundFakeStore(),
		Secrets:        inboundFakeDecrypter{},
		OpenAPIBaseURL: upstream.URL,
		DefaultSharedBot: DefaultSharedBotConfig{
			AppID:     "cli_default",
			AppSecret: "env-default-secret",
			BotOpenID: "ou_default_bot",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	host, _ := manager.defaultSharedRouteAndConfig()
	return manager, routeFromStore(host), &getCalls, &listCalls
}

func TestQuotedChainText_MergeForwardInlineSubItems(t *testing.T) {
	manager, agent, _, listCalls := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				chatID:  "oc_chat",
				subs:    []string{"om_c1", "om_c2"},
			},
			"om_c1": {msgType: "text", content: `{"text":"first line"}`, upperID: "om_mf"},
			"om_c2": {msgType: "text", content: `{"text":"second line"}`, upperID: "om_mf"},
		},
		// No chat list needed — inline path satisfies.
		nil,
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Conversation history]") || !strings.Contains(got, "[/Conversation history]") {
		t.Errorf("expected Conversation history envelope, got %q", got)
	}
	if !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Errorf("expected both children in transcript, got %q", got)
	}
	if !strings.Contains(got, "[Quoted message]") {
		t.Errorf("expected outer Quoted message envelope, got %q", got)
	}
	if atomic.LoadInt32(listCalls) != 0 {
		t.Errorf("inline path must not hit chat-list fallback, list_calls=%d", atomic.LoadInt32(listCalls))
	}
}

func TestQuotedChainText_MergeForwardChatListFallback(t *testing.T) {
	manager, agent, _, listCalls := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				chatID:  "oc_chat",
				// No inline subs — forces fallback.
			},
			"om_c1":        {msgType: "text", content: `{"text":"fallback line"}`, upperID: "om_mf"},
			"om_unrelated": {msgType: "text", content: `{"text":"noise"}`, upperID: "om_other_mf"},
		},
		map[string][]string{
			"oc_chat": {"om_c1", "om_unrelated"},
		},
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "fallback line") {
		t.Errorf("fallback child missing in transcript, got %q", got)
	}
	if strings.Contains(got, "noise") {
		t.Errorf("unrelated chat message must NOT appear, got %q", got)
	}
	if atomic.LoadInt32(listCalls) == 0 {
		t.Error("fallback path expected to call ListMessagesByChatPage at least once")
	}
}

func TestQuotedChainText_MergeForwardNoChildrenPlaceholder(t *testing.T) {
	manager, agent, _, _ := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				chatID:  "oc_chat",
				// No inline subs and chat-list lookup will return nothing.
			},
		},
		map[string][]string{"oc_chat": {}}, // empty chat
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Conversation history]") {
		t.Errorf("expected placeholder when children unresolvable, got %q", got)
	}
	// Placeholder body has no transcript lines, so the inner separators
	// should NOT appear.
	if strings.Contains(got, "\n---\n") {
		t.Errorf("placeholder must not include line separators, got %q", got)
	}
}

func TestQuotedChainText_MergeForwardNoChatIDDegrades(t *testing.T) {
	manager, agent, _, listCalls := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				// chat_id empty — fallback can't even try
			},
		},
		nil,
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Conversation history]") {
		t.Errorf("expected placeholder, got %q", got)
	}
	if atomic.LoadInt32(listCalls) != 0 {
		t.Errorf("must NOT call list when chat_id is empty, list_calls=%d", atomic.LoadInt32(listCalls))
	}
}

// TestQuotedChainText_MergeForwardImageSubMessages is the regression
// for the topic-card screenshot case: parent is a merge_forward whose
// children are pure-image messages. Before this fix, expandMergeForward
// dropped image children entirely and the LLM saw only "[Conversation history]"
// (or worse, the upstream "Merged and Forwarded Message" placeholder).
// Now each child's image_key must be downloaded against its OWN
// message_id and surface as an attachment, with an [image:N] placeholder
// in the rendered text.
func TestQuotedChainText_MergeForwardImageSubMessages(t *testing.T) {
	manager, agent, _, _ := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				chatID:  "oc_chat",
				subs:    []string{"om_img_a", "om_img_b"},
			},
			"om_img_a": {msgType: "image", content: `{"image_key":"img_a_key"}`, upperID: "om_mf"},
			"om_img_b": {msgType: "image", content: `{"image_key":"img_b_key"}`, upperID: "om_mf"},
		},
		nil,
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
		Metadata:  map[string]any{},
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "[Quoted message]") {
		t.Errorf("missing quote wrapper: %q", got)
	}
	if !strings.Contains(got, "[Conversation history]") {
		t.Errorf("missing merge_forward wrapper: %q", got)
	}
	if !strings.Contains(got, "[image:1]") || !strings.Contains(got, "[image:2]") {
		t.Errorf("missing [image:1] / [image:2] placeholders: %q", got)
	}

	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments from merge_forward image children, got %d: %#v", len(attachments), attachments)
	}
	for i, att := range attachments {
		if att.Kind != "image" || att.MIME != "image/png" {
			t.Errorf("attachment[%d] shape wrong: %#v", i, att)
		}
		if att.Size == 0 || att.DataBase64 == "" {
			t.Errorf("attachment[%d] empty payload", i)
		}
	}
}

// TestQuotedChainText_MergeForwardMixedTextAndImageChildren covers the
// realistic shape: children include a text-only line, an image, and a
// post-with-embedded-img. The rendered transcript must keep the text
// lines AND surface the images as attachments.
func TestQuotedChainText_MergeForwardMixedTextAndImageChildren(t *testing.T) {
	manager, agent, _, _ := newMergeForwardManager(t,
		map[string]fakeMergeForwardMsg{
			"om_mf": {
				msgType: "merge_forward",
				content: "Merged and Forwarded Message",
				chatID:  "oc_chat",
				subs:    []string{"om_c_text", "om_c_img", "om_c_post"},
			},
			"om_c_text": {msgType: "text", content: `{"text":"first line"}`, upperID: "om_mf"},
			"om_c_img":  {msgType: "image", content: `{"image_key":"img_c"}`, upperID: "om_mf"},
			"om_c_post": {
				msgType: "post",
				content: `{"zh_cn":{"title":"t","content":[
					[{"tag":"text","text":"with screenshot"},{"tag":"img","image_key":"img_d"}]
				]}}`,
				upperID: "om_mf",
			},
		},
		nil,
	)

	inbound := gateway.FeishuInboundEvent{
		AppID:     "cli_default",
		MessageID: "om_user",
		ChatID:    "oc_chat",
		ChatType:  "group",
		ParentID:  "om_mf",
		Metadata:  map[string]any{},
	}

	got := manager.quotedChainText(context.Background(), agent, &inbound)
	if !strings.Contains(got, "first line") {
		t.Errorf("missing text child: %q", got)
	}
	if !strings.Contains(got, "with screenshot") {
		t.Errorf("missing post child text: %q", got)
	}
	if !strings.Contains(got, "[image:1]") || !strings.Contains(got, "[image:2]") {
		t.Errorf("missing both [image:N] placeholders: %q", got)
	}

	attachments := store.DecodeMessageAttachments(inbound.Metadata)
	if len(attachments) != 2 {
		t.Fatalf("expected 2 attachments (image child + post-with-img child), got %d", len(attachments))
	}
}

// fakeConnectorReader is a minimal ConnectorReader for Reconcile tests:
// it returns a fixed list of Feishu workspace connectors.
type fakeConnectorReader struct {
	feishu []store.WorkspaceConnectorRead
}

func (f fakeConnectorReader) ListWorkspaceConnectorsByPlatform(_ context.Context, platform string) ([]store.WorkspaceConnectorRead, error) {
	if !strings.EqualFold(platform, "feishu") {
		return nil, nil
	}
	return append([]store.WorkspaceConnectorRead(nil), f.feishu...), nil
}

func (f fakeConnectorReader) GetWorkspaceConnectorByAppID(_ context.Context, _, appID string) (store.WorkspaceConnectorRead, error) {
	for _, c := range f.feishu {
		if strings.EqualFold(strings.TrimSpace(c.AppID), strings.TrimSpace(appID)) {
			return c, nil
		}
	}
	return store.WorkspaceConnectorRead{}, errors.New("connector not found")
}

// TestManager_ReconcileDefaultSharedSkippedWhenConnectorClaimsAppID pins the
// dual-connection dedup: when a workspace_im_connectors Feishu connector owns
// an app_id, the env-backed default shared bot must NOT open a second
// websocket for the SAME app_id (Feishu routes each event to only one
// long-connection, so a duplicate socket races the connector for inbound and
// can silently drop group @-mentions). When no connector claims the app_id,
// the env default shared bot still starts as before.
func TestManager_ReconcileDefaultSharedSkippedWhenConnectorClaimsAppID(t *testing.T) {
	const appID = "cli_shared"

	newMgr := func(t *testing.T, withConnector bool) *Manager {
		t.Helper()
		st := newInboundFakeStore()
		// Secret backing the workspace connector's app_secret_ref so its
		// client can actually start (the dedup itself does not depend on
		// this — the app_id is claimed before startClient runs — but a
		// started connector client makes the positive case realistic).
		st.secrets["ref-shared"] = store.SecretPayload{
			EncryptedPayload: []byte(`{"app_secret":"conn-secret"}`),
		}
		opts := Options{
			Store:   st,
			Secrets: inboundFakeDecrypter{},
			// Point the SDK at a dead local host so the background ws
			// dial fails fast instead of reaching the real Feishu edge.
			Domain: "http://127.0.0.1:1",
			DefaultSharedBot: DefaultSharedBotConfig{
				AppID:     appID,
				AppSecret: "env-secret",
			},
		}
		if withConnector {
			opts.Connectors = fakeConnectorReader{feishu: []store.WorkspaceConnectorRead{{
				ID:            "conn-1",
				WorkspaceID:   "ws-1",
				WorkspaceName: "Platform",
				Platform:      "feishu",
				AppID:         appID,
				Enabled:       true,
				Config: map[string]any{
					"event_mode":     "websocket",
					"app_secret_ref": "ref-shared",
					"bot_open_id":    "ou_conn_bot",
				},
			}}}
		}
		m, err := NewManager(opts)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	hasClient := func(m *Manager, key string) bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		_, ok := m.clients[key]
		return ok
	}

	t.Run("connector claims app_id -> default shared skipped", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		m := newMgr(t, true)
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if hasClient(m, defaultClientKey(appID)) {
			t.Fatalf("default shared bot opened a duplicate socket for app_id %q already owned by a workspace connector", appID)
		}
		if !hasClient(m, clientKey("ws-1", appID)) {
			t.Fatalf("workspace connector client for app_id %q was not started", appID)
		}
	})

	t.Run("no connector -> default shared starts", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		m := newMgr(t, false)
		if err := m.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		if !hasClient(m, defaultClientKey(appID)) {
			t.Fatalf("default shared bot did not start for unclaimed app_id %q", appID)
		}
	})
}
