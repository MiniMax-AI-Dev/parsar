package discord

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

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

func channelWithRouter(r channel.ActionRouter, opts ...Option) *Channel {
	return New(Config{AppID: "A123", BotToken: "bot-test"},
		append([]Option{WithActionRouter(r)}, opts...)...)
}

// buttonInteraction is a message-component interaction (type 3) for a
// permission_allow button click; the custom_id packs action + request id.
const buttonInteraction = `{
  "type": 3,
  "guild_id": "guild-9",
  "channel_id": "chan-1",
  "message": {"id": "msg-100"},
  "member": {"user": {"id": "op-1"}},
  "data": {"custom_id": "permission_allow:req-42", "component_type": 2}
}`

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

func TestSplitCustomID(t *testing.T) {
	cases := []struct{ in, action, value string }{
		{"permission_allow:req-42", "permission_allow", "req-42"},
		{"ask_user_choice_pick:0", "ask_user_choice_pick", "0"},
		{"permission_deny", "permission_deny", ""},
		{"a:b:c", "a", "b:c"}, // only the first ':' splits
	}
	for _, c := range cases {
		action, value := splitCustomID(c.in)
		if action != c.action || value != c.value {
			t.Errorf("splitCustomID(%q) = (%q,%q), want (%q,%q)", c.in, action, value, c.action, c.value)
		}
	}
}

func TestDecodeAction_MapsButtonFields(t *testing.T) {
	action, _, err := newTestChannel().decodeAction([]byte(buttonInteraction))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionPermissionAllow {
		t.Errorf("Kind = %q, want permission_allow", action.Kind)
	}
	if action.Platform != channel.PlatformDiscord {
		t.Errorf("Platform = %q, want discord", action.Platform)
	}
	if action.BotID != "guild-9" {
		t.Errorf("BotID = %q, want guild-9 (guild_id)", action.BotID)
	}
	if action.ExternalMessageID != "msg-100" {
		t.Errorf("ExternalMessageID = %q, want msg-100", action.ExternalMessageID)
	}
	if action.ExternalChatID != "chan-1" {
		t.Errorf("ExternalChatID = %q, want chan-1", action.ExternalChatID)
	}
	if action.OperatorID != "op-1" {
		t.Errorf("OperatorID = %q, want op-1 (member.user.id)", action.OperatorID)
	}
	if action.Values["value"] != "req-42" || action.Values["permission_request_id"] != "req-42" {
		t.Errorf("Values = %v, want value+permission_request_id = req-42", action.Values)
	}
}

func TestDecodeAction_DMUsesAppIDAndUserOperator(t *testing.T) {
	dm := `{"type":3,"channel_id":"dm-1","message":{"id":"m1"},"user":{"id":"u-9"},"data":{"custom_id":"permission_deny:r1"}}`
	action, _, err := newTestChannel().decodeAction([]byte(dm))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.BotID != "A123" {
		t.Errorf("BotID = %q, want app id fallback A123 (no guild)", action.BotID)
	}
	if action.OperatorID != "u-9" {
		t.Errorf("OperatorID = %q, want u-9 (top-level user in a DM)", action.OperatorID)
	}
}

func TestDecodeAction_RejectsUnsupportedType(t *testing.T) {
	// type 1 = PING; not a component/modal interaction.
	if _, _, err := newTestChannel().decodeAction([]byte(`{"type":1}`)); err == nil {
		t.Fatal("decodeAction must reject a non-component/modal interaction type")
	}
}

func TestDecodeAction_ModalFoldsCredentialValues(t *testing.T) {
	modal := `{
	  "type": 5,
	  "guild_id": "g1",
	  "channel_id": "c1",
	  "message": {"id": "m1"},
	  "member": {"user": {"id": "op"}},
	  "data": {
	    "custom_id": "credential_form_submit:qkey-7",
	    "components": [
	      {"components": [{"custom_id": "openai", "value": "sk-123"}]},
	      {"components": [{"custom_id": "blank", "value": "  "}]}
	    ]
	  }
	}`
	action, _, err := newTestChannel().decodeAction([]byte(modal))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionCredentialSubmit {
		t.Fatalf("Kind = %q, want credential_form_submit", action.Kind)
	}
	if action.Values["qkey"] != "qkey-7" {
		t.Errorf("Values[qkey] = %q, want qkey-7", action.Values["qkey"])
	}
	if got := action.FormValues["credential_openai"]; got != "sk-123" {
		t.Errorf("FormValues[credential_openai] = %v, want sk-123", got)
	}
	if _, ok := action.FormValues["credential_blank"]; ok {
		t.Error("a blank field must be dropped from FormValues")
	}
}

// TestHandleAction_PickThenSubmitFoldsChoices exercises the Discord-specific
// pick accumulation: each select fires its own interaction, and the Submit click
// drains the recorded picks into FormValues since Discord does not echo state.
func TestHandleAction_PickThenSubmitFoldsChoices(t *testing.T) {
	router := &fakeRouter{}
	c := channelWithRouter(router, WithPickStore(NewMemoryPickStore()))
	ctx := context.Background()

	pickQ0 := `{"type":3,"guild_id":"g1","channel_id":"c1","message":{"id":"m1"},"data":{"custom_id":"ask_user_choice_pick:0","values":["prod"]}}`
	pickQ1 := `{"type":3,"guild_id":"g1","channel_id":"c1","message":{"id":"m1"},"data":{"custom_id":"ask_user_choice_pick:1","values":["a","b"]}}`
	submit := `{"type":3,"guild_id":"g1","channel_id":"c1","message":{"id":"m1"},"member":{"user":{"id":"op"}},"data":{"custom_id":"ask_user_choice_submit:form-1"}}`

	// A bare pick is a silent ack (not routed) but is recorded.
	res, err := c.HandleAction(ctx, []byte(pickQ0))
	if err != nil {
		t.Fatalf("HandleAction(pickQ0): %v", err)
	}
	if res.Handled {
		t.Error("a bare pick must not be routed (Handled=false)")
	}
	if _, err := c.HandleAction(ctx, []byte(pickQ1)); err != nil {
		t.Fatalf("HandleAction(pickQ1): %v", err)
	}
	if _, err := c.HandleAction(ctx, []byte(submit)); err != nil {
		t.Fatalf("HandleAction(submit): %v", err)
	}

	if router.got.Kind != channel.CardActionUserChoiceSubmit {
		t.Fatalf("routed Kind = %q, want ask_user_choice_submit", router.got.Kind)
	}
	if router.got.Values["request_id"] != "form-1" {
		t.Errorf("request_id = %q, want form-1", router.got.Values["request_id"])
	}
	if got := router.got.FormValues["q0"]; got != "prod" {
		t.Errorf("FormValues[q0] = %v, want single string prod", got)
	}
	q1, ok := router.got.FormValues["q1"].([]any)
	if !ok || len(q1) != 2 {
		t.Fatalf("FormValues[q1] = %v, want []any of 2", router.got.FormValues["q1"])
	}
}

func TestHandleAction_NoRouterEchoesReceived(t *testing.T) {
	res, err := newTestChannel().HandleAction(context.Background(), []byte(buttonInteraction))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if res.Handled {
		t.Error("with no router wired, Handled must be false")
	}
	var msg deMessage
	if err := json.Unmarshal(res.Ack, &msg); err != nil {
		t.Fatalf("ack not deMessage JSON: %v", err)
	}
	if msg.Content != ackReceived {
		t.Errorf("ack content = %q, want %q", msg.Content, ackReceived)
	}
}

func TestHandleAction_RoutesAndRendersResultCard(t *testing.T) {
	router := &fakeRouter{ack: channel.ActionAck{Result: &channel.ActionResultCard{
		Kind: channel.CardActionPermissionAllow, Title: "Agent", Approved: true,
	}}}
	res, err := channelWithRouter(router).HandleAction(context.Background(), []byte(buttonInteraction))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if !res.Handled {
		t.Error("a routed action must be Handled")
	}
	if router.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("routed Kind = %q, want permission_allow", router.got.Kind)
	}
	var msg deMessage
	if err := json.Unmarshal(res.Ack, &msg); err != nil {
		t.Fatalf("ack not deMessage JSON: %v", err)
	}
	if len(msg.Embeds) == 0 {
		t.Fatal("result card ack must carry an embed")
	}
}

func TestHandleAction_PropagatesRouterError(t *testing.T) {
	want := errors.New("router down")
	_, err := channelWithRouter(&fakeRouter{err: want}).HandleAction(context.Background(), []byte(buttonInteraction))
	if err != want {
		t.Fatalf("HandleAction err = %v, want %v", err, want)
	}
}
