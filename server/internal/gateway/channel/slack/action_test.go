package slack

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// blockActionsPayload is a block_actions interaction for a permission_allow
// button click. The block_id routes it into ActionCallback.BlockActions.
const blockActionsPayload = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1700000000.000200"},
  "actions":[
    {"action_id":"permission_allow","block_id":"b1","type":"button","value":"req-42"}
  ]
}`

// fakeRouter records the CardAction it received and returns a canned ack.
type fakeRouter struct {
	got channel.CardAction
	ack channel.ActionAck
	err error
}

func (f *fakeRouter) RouteAction(_ context.Context, a channel.CardAction) (channel.ActionAck, error) {
	f.got = a
	return f.ack, f.err
}

func channelWithRouter(r channel.ActionRouter) *Channel {
	return New(Config{AppID: "A123", BotToken: "xoxb-test"}, WithActionRouter(r))
}

func TestActionKindFor(t *testing.T) {
	cases := map[string]channel.CardActionKind{
		"permission_allow":       channel.CardActionPermissionAllow,
		"permission_deny":        channel.CardActionPermissionDeny,
		"credential_form_submit": channel.CardActionCredentialSubmit,
		"ask_user_choice_submit": channel.CardActionUserChoiceSubmit,
		"ask_user_choice_pick":   channel.CardActionUserChoicePick,
		"totally_unknown":        channel.CardActionUnknown,
	}
	for in, want := range cases {
		if got := ActionKindFor(in); got != want {
			t.Errorf("ActionKindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeAction_MapsBlockActionFields(t *testing.T) {
	action, err := newTestChannel().decodeAction([]byte(blockActionsPayload))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionPermissionAllow {
		t.Errorf("Kind = %q, want permission_allow", action.Kind)
	}
	if action.Platform != channel.PlatformSlack {
		t.Errorf("Platform = %q, want slack", action.Platform)
	}
	if action.BotID != "A123" {
		t.Errorf("BotID = %q, want A123", action.BotID)
	}
	if action.ExternalMessageID != "1700000000.000200" {
		t.Errorf("ExternalMessageID = %q, want the message ts", action.ExternalMessageID)
	}
	if action.ExternalChatID != "C1" {
		t.Errorf("ExternalChatID = %q, want C1", action.ExternalChatID)
	}
	if action.OperatorID != "U1" {
		t.Errorf("OperatorID = %q, want U1", action.OperatorID)
	}
	if action.Values["action"] != "permission_allow" {
		t.Errorf("Values[action] = %q, want permission_allow", action.Values["action"])
	}
	if action.Values["value"] != "req-42" {
		t.Errorf("Values[value] = %q, want req-42", action.Values["value"])
	}
}

func TestDecodeAction_CarriesSelectedOption(t *testing.T) {
	const pick = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1.2"},
  "actions":[
    {"action_id":"ask_user_choice_pick","block_id":"b1","type":"static_select","selected_option":{"value":"opt-2"}}
  ]
}`
	action, err := newTestChannel().decodeAction([]byte(pick))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionUserChoicePick {
		t.Errorf("Kind = %q, want ask_user_choice_pick", action.Kind)
	}
	if action.Values["selected_option"] != "opt-2" {
		t.Errorf("Values[selected_option] = %q, want opt-2", action.Values["selected_option"])
	}
}

func TestDecodeAction_FallsBackToAPIAppID(t *testing.T) {
	// With no configured AppID the bot id comes from the payload's api_app_id.
	c := New(Config{BotToken: "xoxb-test"})
	action, err := c.decodeAction([]byte(blockActionsPayload))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.BotID != "A123" {
		t.Errorf("BotID = %q, want A123 (api_app_id fallback)", action.BotID)
	}
}

func TestDecodeAction_BotIDFromTeamID(t *testing.T) {
	// A multi-workspace install carries team.id; it wins over the configured
	// app id so the outbound resolver can mint the per-workspace bot token.
	const payload = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "team":{"id":"T_ACME"},
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1.2"},
  "actions":[
    {"action_id":"permission_allow","block_id":"b1","type":"button","value":"req-42"}
  ]
}`
	action, err := newTestChannel().decodeAction([]byte(payload))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.BotID != "T_ACME" {
		t.Errorf("BotID = %q, want T_ACME (team id wins over app id)", action.BotID)
	}
}

func TestDecodeAction_FoldsChoiceStateValues(t *testing.T) {
	// An ask_user_choice_submit click carries the picked options not on the
	// button but in state.values, keyed by the per-question choice_<idx>
	// block_id. decodeAction must fold those into FormValues["q<idx>"] — a
	// single-select as a string, a multi-select as a []any — the shape
	// routeUserChoiceSubmit reads per question index. This is the bug that made
	// Submit "do nothing": the selection never reached the agent.
	const submit = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1.2"},
  "actions":[
    {"action_id":"ask_user_choice_submit","block_id":"choice_submit","type":"button","value":"req-7"}
  ],
  "state":{"values":{
    "choice_0":{"ask_user_choice_pick":{"type":"static_select","selected_option":{"value":"continue previous work"}}},
    "choice_1":{"ask_user_choice_pick":{"type":"multi_static_select","selected_options":[{"value":"A"},{"value":"B"}]}}
  }}
}`
	action, err := newTestChannel().decodeAction([]byte(submit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionUserChoiceSubmit {
		t.Errorf("Kind = %q, want ask_user_choice_submit", action.Kind)
	}
	if action.Values["request_id"] != "req-7" {
		t.Errorf("Values[request_id] = %q, want req-7", action.Values["request_id"])
	}
	if got, ok := action.FormValues["q0"].(string); !ok || got != "continue previous work" {
		t.Errorf("FormValues[q0] = %v, want \"continue previous work\"", action.FormValues["q0"])
	}
	multi, ok := action.FormValues["q1"].([]any)
	if !ok || len(multi) != 2 || multi[0] != "A" || multi[1] != "B" {
		t.Errorf("FormValues[q1] = %v, want [A B]", action.FormValues["q1"])
	}
}

func TestDecodeAction_FoldsCredentialStateValues(t *testing.T) {
	// A credential_form_submit click carries the typed secrets in state.values,
	// keyed by the per-field block_id (the capability name). decodeAction must
	// fold those into FormValues["credential_<kind>"] — the shape
	// routeCredentialFormSubmit reads.
	const submit = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1.2"},
  "actions":[
    {"action_id":"credential_form_submit","block_id":"credential_submit","type":"button","value":"qkey-9"}
  ],
  "state":{"values":{
    "github_token":{"credential_value":{"type":"plain_text_input","value":"ghp_secret"}}
  }}
}`
	action, err := newTestChannel().decodeAction([]byte(submit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionCredentialSubmit {
		t.Errorf("Kind = %q, want credential_form_submit", action.Kind)
	}
	if action.Values["qkey"] != "qkey-9" {
		t.Errorf("Values[qkey] = %q, want qkey-9", action.Values["qkey"])
	}
	if got, ok := action.FormValues["credential_github_token"].(string); !ok || got != "ghp_secret" {
		t.Errorf("FormValues[credential_github_token] = %v, want ghp_secret", action.FormValues["credential_github_token"])
	}
}

func TestDecodeAction_EmptyChoiceStateYieldsNilFormValues(t *testing.T) {
	// A Submit with no pick must leave FormValues nil so routeUserChoiceSubmit
	// records a cancel rather than a phantom answer.
	const submit = `{
  "type":"block_actions",
  "api_app_id":"A123",
  "user":{"id":"U1"},
  "channel":{"id":"C1"},
  "message":{"ts":"1.2"},
  "actions":[
    {"action_id":"ask_user_choice_submit","block_id":"choice_submit","type":"button","value":"req-7"}
  ]
}`
	action, err := newTestChannel().decodeAction([]byte(submit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.FormValues != nil {
		t.Errorf("FormValues = %v, want nil for an empty submit", action.FormValues)
	}
}

func TestHandleAction_NoRouterEchoesReceived(t *testing.T) {
	res, err := newTestChannel().HandleAction(context.Background(), []byte(blockActionsPayload))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if res.Handled {
		t.Error("with no router wired, Handled must be false")
	}
	var resp slackActionResponse
	if err := json.Unmarshal(res.Ack, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if resp.Text != ackReceived {
		t.Errorf("ack text = %q, want the received default", resp.Text)
	}
	if resp.ResponseType != "ephemeral" {
		t.Errorf("ack response_type = %q, want ephemeral", resp.ResponseType)
	}
}

// TestHandleAction_PickIsSilentNoOp guards the bare-pick short-circuit: a
// standalone ask_user_choice_pick (a dropdown change Slack fires before Submit)
// must not reach the router and must produce no ack to render back.
func TestHandleAction_PickIsSilentNoOp(t *testing.T) {
	const pick = `{
  "type":"block_actions","api_app_id":"A123",
  "user":{"id":"U1"},"channel":{"id":"C1"},"message":{"ts":"1.2"},
  "actions":[{"action_id":"ask_user_choice_pick","block_id":"choice_0","type":"static_select","selected_option":{"value":"opt-2"}}]
}`
	router := &fakeRouter{err: errors.New("router must not be called for a bare pick")}
	res, err := channelWithRouter(router).HandleAction(context.Background(), []byte(pick))
	if err != nil {
		t.Fatalf("a bare pick must be a silent no-op, got err: %v", err)
	}
	if res.Handled {
		t.Error("a bare pick must not be marked Handled")
	}
	if len(res.Ack) != 0 {
		t.Errorf("a bare pick must produce no ack, got %s", res.Ack)
	}
}

func TestHandleAction_RoutesThroughRouter(t *testing.T) {
	router := &fakeRouter{ack: channel.ActionAck{ToastKind: "success", ToastContent: "Allowed"}}
	res, err := channelWithRouter(router).HandleAction(context.Background(), []byte(blockActionsPayload))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if !res.Handled {
		t.Error("with a router wired, Handled must be true")
	}
	// The router saw the decoded neutral action.
	if router.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("router saw Kind %q, want permission_allow", router.got.Kind)
	}
	if router.got.Values["value"] != "req-42" {
		t.Errorf("router saw value %q, want req-42", router.got.Values["value"])
	}
	// The ack carries the router's toast.
	var resp slackActionResponse
	if err := json.Unmarshal(res.Ack, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if resp.Text != "Allowed" {
		t.Errorf("ack text = %q, want the router toast", resp.Text)
	}
}

func TestHandleAction_RouterErrorPropagates(t *testing.T) {
	want := errors.New("route boom")
	router := &fakeRouter{err: want}
	_, err := channelWithRouter(router).HandleAction(context.Background(), []byte(blockActionsPayload))
	if err != want {
		t.Fatalf("HandleAction err = %v, want %v", err, want)
	}
}

func TestHandleAction_RejectsNonBlockActions(t *testing.T) {
	const viewSubmission = `{"type":"view_submission","api_app_id":"A123"}`
	if _, err := newTestChannel().HandleAction(context.Background(), []byte(viewSubmission)); err == nil {
		t.Fatal("HandleAction must reject non-block_actions interactions (button-only)")
	}
}

func TestHandleAction_RejectsEmptyActions(t *testing.T) {
	const noActions = `{"type":"block_actions","api_app_id":"A123","actions":[]}`
	if _, err := newTestChannel().HandleAction(context.Background(), []byte(noActions)); err == nil {
		t.Fatal("HandleAction must reject a block_actions payload with no actions")
	}
}

func TestHandleAction_RejectsMalformedPayload(t *testing.T) {
	if _, err := newTestChannel().HandleAction(context.Background(), []byte("not json")); err == nil {
		t.Fatal("HandleAction must reject a malformed payload")
	}
}

func TestRenderSlackAck_ToastBecomesEphemeral(t *testing.T) {
	out, err := renderSlackAck(channel.ActionAck{ToastContent: "done"})
	if err != nil {
		t.Fatalf("renderSlackAck: %v", err)
	}
	var resp slackActionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if resp.ResponseType != "ephemeral" || resp.Text != "done" {
		t.Errorf("toast must render as an ephemeral text reply, got %+v", resp)
	}
	if resp.ReplaceOriginal {
		t.Error("a toast must not replace the original message")
	}
}

func TestRenderSlackAck_ReplaceCardReplacesInPlace(t *testing.T) {
	card := blockCard(t) // a real Block Kit {text, blocks} payload
	out, err := renderSlackAck(channel.ActionAck{ReplaceCard: json.RawMessage(card.Payload)})
	if err != nil {
		t.Fatalf("renderSlackAck: %v", err)
	}
	var resp slackActionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if !resp.ReplaceOriginal {
		t.Error("a replace card must set replace_original")
	}
	if len(resp.Blocks) == 0 {
		t.Error("a replace card must carry the decoded blocks")
	}
}

func TestRenderSlackAck_RejectsMalformedReplaceCard(t *testing.T) {
	if _, err := renderSlackAck(channel.ActionAck{ReplaceCard: json.RawMessage("not json")}); err == nil {
		t.Fatal("renderSlackAck must reject a malformed replace card")
	}
}

// TestRenderSlackAck_ResultRendersReplacement asserts a neutral ActionResultCard
// (the non-Feishu render path) becomes a replace_original reply with Block Kit
// blocks — so the source card is swapped for the verdict/summary card in place.
func TestRenderSlackAck_ResultRendersReplacement(t *testing.T) {
	out, err := renderSlackAck(channel.ActionAck{
		ToastContent: "Allowed", // ignored once Result is set
		Result: &channel.ActionResultCard{
			Kind:     channel.CardActionPermissionAllow,
			Title:    "Demo Agent",
			Approved: true,
		},
	})
	if err != nil {
		t.Fatalf("renderSlackAck: %v", err)
	}
	var resp slackActionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if !resp.ReplaceOriginal {
		t.Error("a neutral Result must set replace_original")
	}
	if len(resp.Blocks) == 0 {
		t.Error("a neutral Result must carry rendered blocks")
	}
	if resp.Text == "" {
		t.Error("a neutral Result must carry a notification fallback")
	}
}

// TestRenderActionResultCard_Variants locks the per-kind result rendering:
// permission verdict (green/red), credential reject vs success, and the
// user-choice summary fallback.
func TestRenderActionResultCard_Variants(t *testing.T) {
	cases := []struct {
		name     string
		in       channel.ActionResultCard
		wantText string // substring expected somewhere in a section block
		blockMin int
	}{
		{
			name:     "permission approved",
			in:       channel.ActionResultCard{Kind: channel.CardActionPermissionAllow, Title: "A", Approved: true},
			wantText: "Approved",
			blockMin: 2,
		},
		{
			name:     "permission denied",
			in:       channel.ActionResultCard{Kind: channel.CardActionPermissionDeny, Title: "A", Approved: false},
			wantText: "Denied",
			blockMin: 2,
		},
		{
			name:     "credential rejected",
			in:       channel.ActionResultCard{Kind: channel.CardActionCredentialSubmit, Title: "A", Rejected: true, RejectReason: "Credentials can only be submitted by the requester"},
			wantText: "Credentials can only be submitted by the requester",
			blockMin: 2,
		},
		{
			name:     "credential success",
			in:       channel.ActionResultCard{Kind: channel.CardActionCredentialSubmit, Title: "A", Summary: "Received, resuming the conversation"},
			wantText: "Received",
			blockMin: 2,
		},
		{
			name:     "user choice summary",
			in:       channel.ActionResultCard{Kind: channel.CardActionUserChoiceSubmit, Title: "A", Summary: "Recorded: Option A"},
			wantText: "Recorded",
			blockMin: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := renderActionResultCard(&tc.in)
			if len(msg.Blocks) < tc.blockMin {
				t.Fatalf("blocks = %d, want >= %d", len(msg.Blocks), tc.blockMin)
			}
			if msg.Blocks[0].Type != "header" {
				t.Errorf("first block = %q, want header", msg.Blocks[0].Type)
			}
			found := false
			for _, b := range msg.Blocks {
				if b.Text != nil && strings.Contains(b.Text.Text, tc.wantText) {
					found = true
				}
			}
			if !found {
				t.Errorf("no section carried %q; msg=%+v", tc.wantText, msg)
			}
			if msg.Text == "" {
				t.Error("missing notification fallback")
			}
		})
	}
}
