package feishu

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// Compile-time + runtime assertion that the Feishu adapter satisfies the
// neutral Channel contract.
var _ channel.Channel = (*Channel)(nil)

func newTestChannel() *Channel {
	return New(Config{
		AppID:       "cli_test",
		VerifyToken: "verify-token",
	})
}

func TestPlatform(t *testing.T) {
	if got := newTestChannel().Platform(); got != channel.PlatformFeishu {
		t.Fatalf("Platform() = %q, want %q", got, channel.PlatformFeishu)
	}
}

func TestCapabilitiesDeriveStreamPatches(t *testing.T) {
	c := newTestChannel()
	caps := c.Capabilities()
	if !caps.Edit || !caps.BlockStreaming {
		t.Fatalf("Feishu must declare Edit+BlockStreaming, got %+v", caps)
	}
	if got := caps.DerivedStream(); got != channel.StreamPatches {
		t.Fatalf("DerivedStream() = %v, want StreamPatches", got)
	}
	if got := c.Stream(); got != channel.StreamPatches {
		t.Fatalf("Stream() = %v, want StreamPatches", got)
	}
}

func TestVerifyURLChallenge(t *testing.T) {
	c := newTestChannel()
	body := []byte(`{"token":"verify-token","type":"url_verification","challenge":"chal-123"}`)
	r := httptest.NewRequest("POST", "/webhook", nil)

	verified, challenge, err := c.Verify(r, body)
	if err != nil {
		t.Fatal(err)
	}
	if challenge != "chal-123" {
		t.Fatalf("challenge = %q, want chal-123", challenge)
	}
	if len(verified) == 0 {
		t.Fatal("verified body should be non-empty for a challenge")
	}
}

func TestVerifyTokenMismatch(t *testing.T) {
	c := newTestChannel()
	body := []byte(`{"token":"wrong","event":{}}`)
	r := httptest.NewRequest("POST", "/webhook", nil)
	if _, _, err := c.Verify(r, body); err == nil {
		t.Fatal("expected error on verification token mismatch")
	}
}

func TestNormalizeReusesProductionParser(t *testing.T) {
	c := newTestChannel()
	// A plain im.message.receive_v1 text event.
	verified := []byte(`{
		"header":{"app_id":"cli_test","tenant_key":"tk_1"},
		"event":{
			"sender":{"sender_id":{"union_id":"on_union_1"},"sender_type":"user","tenant_key":"tk_1"},
			"message":{"message_id":"om_1","chat_id":"oc_1","message_type":"text","content":"{\"text\":\"hello\"}"}
		}
	}`)
	ev, err := c.Normalize(verified)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Platform != string(channel.PlatformFeishu) {
		t.Fatalf("Platform = %q", ev.Platform)
	}
	if ev.ExternalMessageID != "om_1" || ev.ExternalChatID != "oc_1" {
		t.Fatalf("ids: msg=%q chat=%q", ev.ExternalMessageID, ev.ExternalChatID)
	}
	if ev.Sender.PlatformUserID != "on_union_1" || ev.Sender.TenantKey != "tk_1" {
		t.Fatalf("sender: %+v", ev.Sender)
	}
	if ev.Text != "hello" {
		t.Fatalf("Text = %q, want hello", ev.Text)
	}
}

func TestOutboundWithoutTransportReturnsErrNoTransport(t *testing.T) {
	c := newTestChannel() // no WithTransport
	if err := c.Reply(context.Background(), channel.ReplyTarget{}, "hi"); err != ErrNoTransport {
		t.Fatalf("Reply err = %v, want ErrNoTransport", err)
	}
	if _, err := c.Send(context.Background(), channel.ReplyTarget{}, channel.Card{}); err != ErrNoTransport {
		t.Fatalf("Send err = %v, want ErrNoTransport", err)
	}
	if err := c.Edit(context.Background(), channel.ReplyTarget{}, gateway.MessageRef{ID: "om_x"}, channel.Card{}); err != ErrNoTransport {
		t.Fatalf("Edit err = %v, want ErrNoTransport", err)
	}
}

func TestHandleActionWithoutRouterEchoesReceived(t *testing.T) {
	c := newTestChannel() // no WithActionRouter
	res, err := c.HandleAction(context.Background(), []byte(`{"event":{"action":{"value":{"action":"permission_allow"}}}}`))
	if err != nil {
		t.Fatalf("HandleAction: %v", err)
	}
	if res.Handled {
		t.Fatal("Handled must be false without an ActionRouter")
	}
	if len(res.Ack) == 0 {
		t.Fatal("HandleAction must still echo an ack toast without a router")
	}
}
