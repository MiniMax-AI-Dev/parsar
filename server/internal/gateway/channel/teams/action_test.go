package teams

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// permissionSubmit is an Action.Submit for the Allow button: it rides a message
// activity whose `value` merges the submit data (action + request id).
const permissionSubmit = `{
  "type":"message","id":"sub-1",
  "from":{"id":"29:user-a","aadObjectId":"aad-alice"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-9"},
  "value":{"action":"permission_allow","permission_request_id":"req-42"}
}`

// choiceSubmit is a prompt_for_user_choice submit: each ChoiceSet's value is
// folded into `value` under its input id (q0 single, q1 multi comma-joined).
const choiceSubmit = `{
  "type":"message","id":"sub-2",
  "from":{"id":"29:user-b"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-9"},
  "value":{"action":"ask_user_choice_submit","request_id":"rq-7","q0":"Yes","q1":"a,b,c"}
}`

// credentialSubmit is a credential-form submit: the Input.Text value lands under
// its "credential_<kind>" id, and the minted qkey rides as a routing value.
const credentialSubmit = `{
  "type":"message","id":"sub-3",
  "from":{"id":"29:user-c"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-9"},
  "value":{"action":"credential_form_submit","qkey":"qk-1","credential_openai":"sk-secret"}
}`

func TestIsCardAction(t *testing.T) {
	if !IsCardAction([]byte(permissionSubmit)) {
		t.Error("a value carrying an action must be a card action")
	}
	if IsCardAction([]byte(dmActivity)) {
		t.Error("a plain message (no value) must not be a card action")
	}
	if IsCardAction([]byte(`{"type":"message","value":{"foo":"bar"}}`)) {
		t.Error("a value with no action key must not be a card action")
	}
	if IsCardAction([]byte("not json")) {
		t.Error("malformed payload must not be a card action")
	}
}

func TestActionKindFor(t *testing.T) {
	cases := map[string]channel.CardActionKind{
		"permission_allow":       channel.CardActionPermissionAllow,
		"permission_deny":        channel.CardActionPermissionDeny,
		"credential_form_submit": channel.CardActionCredentialSubmit,
		"ask_user_choice_submit": channel.CardActionUserChoiceSubmit,
		"nope":                   channel.CardActionUnknown,
	}
	for in, want := range cases {
		if got := ActionKindFor(in); got != want {
			t.Errorf("ActionKindFor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDecodeAction_Permission(t *testing.T) {
	c := newTestChannel()
	action, err := c.decodeAction([]byte(permissionSubmit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionPermissionAllow {
		t.Errorf("Kind = %q, want permission_allow", action.Kind)
	}
	if action.Platform != channel.PlatformTeams {
		t.Errorf("Platform = %q, want teams", action.Platform)
	}
	if action.Values["permission_request_id"] != "req-42" {
		t.Errorf("permission_request_id = %q, want req-42", action.Values["permission_request_id"])
	}
	if action.ExternalChatID != "conv-9" {
		t.Errorf("ExternalChatID = %q, want conv-9", action.ExternalChatID)
	}
	if action.BotID != "28:app-123" {
		t.Errorf("BotID = %q, want 28:app-123", action.BotID)
	}
	if action.OperatorID != "aad-alice" {
		t.Errorf("OperatorID = %q, want aad-alice (aadObjectId preferred)", action.OperatorID)
	}
}

func TestDecodeAction_ChoiceFoldsFormValues(t *testing.T) {
	c := newTestChannel()
	action, err := c.decodeAction([]byte(choiceSubmit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Values["request_id"] != "rq-7" {
		t.Errorf("request_id = %q, want rq-7", action.Values["request_id"])
	}
	if action.FormValues["q0"] != "Yes" {
		t.Errorf("q0 = %v, want single-select string Yes", action.FormValues["q0"])
	}
	want := []any{"a", "b", "c"}
	if !reflect.DeepEqual(action.FormValues["q1"], want) {
		t.Errorf("q1 = %v, want multi-select %v", action.FormValues["q1"], want)
	}
}

func TestDecodeAction_CredentialFoldsFormValues(t *testing.T) {
	c := newTestChannel()
	action, err := c.decodeAction([]byte(credentialSubmit))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if action.Kind != channel.CardActionCredentialSubmit {
		t.Errorf("Kind = %q, want credential_form_submit", action.Kind)
	}
	if action.Values["qkey"] != "qk-1" {
		t.Errorf("qkey = %q, want qk-1", action.Values["qkey"])
	}
	if action.FormValues["credential_openai"] != "sk-secret" {
		t.Errorf("credential_openai = %v, want sk-secret", action.FormValues["credential_openai"])
	}
}

func TestChoiceAnswer(t *testing.T) {
	if got := choiceAnswer("Yes"); got != "Yes" {
		t.Errorf("single = %v, want string Yes", got)
	}
	want := []any{"a", "b"}
	if got := choiceAnswer("a, b"); !reflect.DeepEqual(got, want) {
		t.Errorf("multi = %v, want %v", got, want)
	}
}

func TestAsString(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hi", "hi"},
		{nil, ""},
		{float64(42), "42"},
		{float64(1.5), "1.5"},
		{true, "true"},
	}
	for _, tc := range cases {
		if got := asString(tc.in); got != tc.want {
			t.Errorf("asString(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeActionRouter records the routed CardAction and returns a canned ack.
type fakeActionRouter struct {
	got channel.CardAction
	ack channel.ActionAck
}

func (f *fakeActionRouter) RouteAction(_ context.Context, a channel.CardAction) (channel.ActionAck, error) {
	f.got = a
	return f.ack, nil
}

// TestHandleAction_RoutesThroughRouter: a wired router sees the neutral action
// and its rendered ack comes back with Handled=true.
func TestHandleAction_RoutesThroughRouter(t *testing.T) {
	router := &fakeActionRouter{ack: channel.ActionAck{Result: &channel.ActionResultCard{
		Kind: channel.CardActionPermissionAllow, Title: "Demo Agent", Approved: true,
	}}}
	c := New(Config{AppID: "app-123"}, WithActionRouter(router))
	res, err := c.HandleAction(context.Background(), []byte(permissionSubmit))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if !res.Handled {
		t.Error("Handled must be true when a router is wired")
	}
	if router.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("router saw kind %q, want permission_allow", router.got.Kind)
	}
	if len(res.Ack) == 0 {
		t.Fatal("Ack empty, want a rendered card")
	}
	var wire teamsWireMessage
	if err := json.Unmarshal(res.Ack, &wire); err != nil {
		t.Fatalf("Ack not a teamsWireMessage: %v", err)
	}
	if len(wire.Card) == 0 {
		t.Error("a neutral Result must render into a card")
	}
}

// TestHandleAction_NoRouterEchoesReceived: a router-less adapter still returns a
// non-empty "received" ack with Handled=false so a click never hangs.
func TestHandleAction_NoRouterEchoesReceived(t *testing.T) {
	c := newTestChannel() // no WithActionRouter
	res, err := c.HandleAction(context.Background(), []byte(permissionSubmit))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if res.Handled {
		t.Error("Handled must be false with no router")
	}
	if len(res.Ack) == 0 {
		t.Fatal("Ack empty, want the neutral received echo")
	}
}

func TestHandleAction_RejectsMalformed(t *testing.T) {
	c := newTestChannel()
	if _, err := c.HandleAction(context.Background(), []byte(`{"type":"message"}`)); err == nil {
		t.Fatal("an activity with no value must error")
	}
}
