package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeActionRouter records the decoded action it received and returns a
// canned ack, so tests assert both the decode output and the ack rendering.
type fakeActionRouter struct {
	got channel.CardAction
	ack channel.ActionAck
	err error
}

func (r *fakeActionRouter) RouteAction(_ context.Context, action channel.CardAction) (channel.ActionAck, error) {
	r.got = action
	return r.ack, r.err
}

const permissionActionPayload = `{
	"header":{"app_id":"cli_hdr"},
	"event":{
		"operator":{"open_id":"ou_op"},
		"action":{
			"value":{"action":"permission_allow","permission_request_id":"req_1"},
			"form_value":{}
		},
		"context":{"open_message_id":"om_1","open_chat_id":"oc_1"}
	}
}`

// TestDecodeAction_PermissionDecode locks the neutral CardAction the adapter
// produces from a Feishu permission-button payload.
func TestDecodeAction_PermissionDecode(t *testing.T) {
	c := newTestChannel() // appID cli_test
	got, err := c.decodeAction([]byte(permissionActionPayload))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if got.Kind != channel.CardActionPermissionAllow {
		t.Fatalf("Kind = %q, want permission_allow", got.Kind)
	}
	if got.Platform != channel.PlatformFeishu {
		t.Fatalf("Platform = %q", got.Platform)
	}
	// BotID prefers the channel's configured app over the header.
	if got.BotID != "cli_test" {
		t.Fatalf("BotID = %q, want cli_test", got.BotID)
	}
	if got.ExternalMessageID != "om_1" || got.ExternalChatID != "oc_1" {
		t.Fatalf("ids: msg=%q chat=%q", got.ExternalMessageID, got.ExternalChatID)
	}
	if got.OperatorID != "ou_op" {
		t.Fatalf("OperatorID = %q, want ou_op", got.OperatorID)
	}
	if got.Values["permission_request_id"] != "req_1" {
		t.Fatalf("Values[permission_request_id] = %q, want req_1", got.Values["permission_request_id"])
	}
}

// TestDecodeAction_KindMapping checks every recognised action string maps to
// its neutral kind and an unknown one falls through to CardActionUnknown.
func TestDecodeAction_KindMapping(t *testing.T) {
	cases := map[string]channel.CardActionKind{
		"permission_allow":             channel.CardActionPermissionAllow,
		"permission_deny":              channel.CardActionPermissionDeny,
		"credential_form_submit":       channel.CardActionCredentialSubmit,
		"credential_form_acknowledged": channel.CardActionCredentialAck,
		"ask_user_choice_submit":       channel.CardActionUserChoiceSubmit,
		"ask_user_choice_pick":         channel.CardActionUserChoicePick,
		"some_future_button":           channel.CardActionUnknown,
	}
	c := newTestChannel()
	for action, want := range cases {
		payload := `{"event":{"action":{"value":{"action":"` + action + `"}}}}`
		got, err := c.decodeAction([]byte(payload))
		if err != nil {
			t.Fatalf("decodeAction(%s): %v", action, err)
		}
		if got.Kind != want {
			t.Errorf("action %q → Kind %q, want %q", action, got.Kind, want)
		}
	}
}

// TestDecodeAction_FormValuesCarried verifies credential-form submit values
// pass through untouched (router extracts credential_* keys downstream).
func TestDecodeAction_FormValuesCarried(t *testing.T) {
	payload := `{
		"event":{
			"action":{
				"value":{"action":"credential_form_submit","qkey":"q_9"},
				"form_value":{"credential_openai":"sk-secret","note":"hi"}
			}
		}
	}`
	got, err := newTestChannel().decodeAction([]byte(payload))
	if err != nil {
		t.Fatalf("decodeAction: %v", err)
	}
	if got.Values["qkey"] != "q_9" {
		t.Fatalf("Values[qkey] = %q, want q_9", got.Values["qkey"])
	}
	if got.FormValues["credential_openai"] != "sk-secret" {
		t.Fatalf("FormValues[credential_openai] = %v", got.FormValues["credential_openai"])
	}
}

// TestDecodeAction_BadJSON returns an error rather than a half-built action.
func TestDecodeAction_BadJSON(t *testing.T) {
	if _, err := newTestChannel().decodeAction([]byte(`{not json`)); err == nil {
		t.Fatal("decodeAction must error on malformed JSON")
	}
}

// TestRenderFeishuAck_ToastOnly locks the toast-only response wire shape.
func TestRenderFeishuAck_ToastOnly(t *testing.T) {
	out, err := renderFeishuAck(channel.ActionAck{ToastKind: "success", ToastContent: "Approved"})
	if err != nil {
		t.Fatalf("renderFeishuAck: %v", err)
	}
	var resp feishuActionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ack not valid JSON: %v", err)
	}
	if resp.Toast == nil || resp.Toast.Type != "success" || resp.Toast.Content != "Approved" {
		t.Fatalf("toast = %+v", resp.Toast)
	}
	if resp.Card != nil {
		t.Fatal("toast-only ack must not carry a card")
	}
}

// TestRenderFeishuAck_DefaultsKindAndReplacesCard: empty kind defaults to
// "info"; a ReplaceCard becomes a raw card on the response.
func TestRenderFeishuAck_DefaultsKindAndReplacesCard(t *testing.T) {
	out, err := renderFeishuAck(channel.ActionAck{
		ToastContent: "done",
		ReplaceCard:  json.RawMessage(`{"schema":"2.0"}`),
	})
	if err != nil {
		t.Fatalf("renderFeishuAck: %v", err)
	}
	var resp feishuActionResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("ack not valid JSON: %v", err)
	}
	if resp.Toast == nil || resp.Toast.Type != "info" {
		t.Fatalf("default toast kind = %+v, want info", resp.Toast)
	}
	if resp.Card == nil || resp.Card.Type != "raw" || string(resp.Card.Data) != `{"schema":"2.0"}` {
		t.Fatalf("replace card = %+v", resp.Card)
	}
}

// TestHandleAction_RoutedThroughRouter wires a fake router and asserts the
// decoded action reaches it and its ack is rendered + Handled is true.
func TestHandleAction_RoutedThroughRouter(t *testing.T) {
	router := &fakeActionRouter{ack: channel.ActionAck{ToastKind: "success", ToastContent: "ok"}}
	c := New(Config{AppID: "cli_test"}, WithActionRouter(router))

	res, err := c.HandleAction(context.Background(), []byte(permissionActionPayload))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if !res.Handled {
		t.Fatal("Handled must be true once a router runs")
	}
	if router.got.Kind != channel.CardActionPermissionAllow || router.got.Values["permission_request_id"] != "req_1" {
		t.Fatalf("router received %+v", router.got)
	}
	var resp feishuActionResponse
	if err := json.Unmarshal(res.Ack, &resp); err != nil {
		t.Fatalf("ack not valid JSON: %v", err)
	}
	if resp.Toast == nil || resp.Toast.Content != "ok" {
		t.Fatalf("rendered ack = %+v", resp.Toast)
	}
}

// TestHandleAction_RouterErrorPropagates surfaces a router failure.
func TestHandleAction_RouterErrorPropagates(t *testing.T) {
	router := &fakeActionRouter{err: errors.New("route boom")}
	c := New(Config{AppID: "cli_test"}, WithActionRouter(router))
	if _, err := c.HandleAction(context.Background(), []byte(permissionActionPayload)); err == nil {
		t.Fatal("HandleAction must propagate a router error")
	}
}

// TestHandleAction_BadPayload errors before reaching the router.
func TestHandleAction_BadPayload(t *testing.T) {
	router := &fakeActionRouter{}
	c := New(Config{AppID: "cli_test"}, WithActionRouter(router))
	if _, err := c.HandleAction(context.Background(), []byte(`{nope`)); err == nil {
		t.Fatal("HandleAction must error on malformed payload")
	}
	if router.got.Kind != channel.CardActionUnknown {
		t.Fatal("router must not be called on a decode failure")
	}
}
