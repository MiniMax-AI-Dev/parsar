package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeSender records the calls the adapter makes so tests assert the exact
// request shape (msg_type, receive_id, anchor, content) handed to the
// tenant client.
type fakeSender struct {
	sendReq    *gateway.FeishuMessageSendRequest
	replyID    string
	replyReq   *gateway.FeishuMessageReplyRequest
	patchID    string
	patchBody  string
	appSecret  string
	sendResID  string
	replyResID string
	err        error
}

func (f *fakeSender) SendMessage(_ context.Context, appSecret string, req gateway.FeishuMessageSendRequest) (gateway.FeishuMessageSendResult, error) {
	f.appSecret = appSecret
	r := req
	f.sendReq = &r
	if f.err != nil {
		return gateway.FeishuMessageSendResult{}, f.err
	}
	return gateway.FeishuMessageSendResult{MessageID: f.sendResID}, nil
}

func (f *fakeSender) ReplyMessage(_ context.Context, appSecret string, messageID string, req gateway.FeishuMessageReplyRequest) (gateway.FeishuMessageSendResult, error) {
	f.appSecret = appSecret
	f.replyID = messageID
	r := req
	f.replyReq = &r
	if f.err != nil {
		return gateway.FeishuMessageSendResult{}, f.err
	}
	return gateway.FeishuMessageSendResult{MessageID: f.replyResID}, nil
}

func (f *fakeSender) PatchMessage(_ context.Context, appSecret string, messageID string, content string) error {
	f.appSecret = appSecret
	f.patchID = messageID
	f.patchBody = content
	return f.err
}

// fakeTransport hands the adapter the fake sender + fixed credentials and
// records the bot id the adapter resolved against.
type fakeTransport struct {
	sender   *fakeSender
	creds    gateway.OutboundCredentials
	gotBotID string
	err      error
}

func (t *fakeTransport) OutboundSender(_ context.Context, botID string) (CardSender, gateway.OutboundCredentials, error) {
	t.gotBotID = botID
	if t.err != nil {
		return nil, gateway.OutboundCredentials{}, t.err
	}
	return t.sender, t.creds, nil
}

func wiredChannel(s *fakeSender, creds gateway.OutboundCredentials) (*Channel, *fakeTransport) {
	tr := &fakeTransport{sender: s, creds: creds}
	c := New(Config{AppID: "cli_test"}, WithTransport(tr))
	return c, tr
}

// TestSend_NewCardToChat: no thread anchor → SendMessage to chat_id with the
// interactive card payload; returns the upstream message id.
func TestSend_NewCardToChat(t *testing.T) {
	s := &fakeSender{sendResID: "om_new"}
	c, tr := wiredChannel(s, gateway.OutboundCredentials{AppID: "cli_test", AppSecret: "secret"})

	ref, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "oc_1"}, channel.Card{Payload: []byte(`{"card":1}`)})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ref.ID != "om_new" {
		t.Fatalf("ref.ID = %q, want om_new", ref.ID)
	}
	if tr.gotBotID != "cli_test" {
		t.Fatalf("resolved botID = %q, want cli_test", tr.gotBotID)
	}
	if s.appSecret != "secret" {
		t.Fatalf("appSecret = %q, want secret", s.appSecret)
	}
	if s.replyReq != nil {
		t.Fatal("Send to chat must not call ReplyMessage")
	}
	if s.sendReq == nil {
		t.Fatal("Send to chat must call SendMessage")
	}
	if s.sendReq.ReceiveIDType != "chat_id" || s.sendReq.ReceiveID != "oc_1" {
		t.Fatalf("send target = %+v", s.sendReq)
	}
	if s.sendReq.MsgType != msgTypeInteractive || s.sendReq.Content != `{"card":1}` {
		t.Fatalf("send body = %+v", s.sendReq)
	}
}

// TestSend_InThread: a thread anchor routes to ReplyMessage with
// ReplyInThread=true (the single interactive-send path's thread branch).
func TestSend_InThread(t *testing.T) {
	s := &fakeSender{replyResID: "om_reply"}
	c, _ := wiredChannel(s, gateway.OutboundCredentials{AppSecret: "secret"})

	ref, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "oc_1", ExternalThreadID: "om_root"}, channel.Card{Payload: []byte(`{"card":2}`)})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ref.ID != "om_reply" {
		t.Fatalf("ref.ID = %q, want om_reply", ref.ID)
	}
	if s.sendReq != nil {
		t.Fatal("Send in-thread must not call SendMessage")
	}
	if s.replyID != "om_root" {
		t.Fatalf("reply anchor = %q, want om_root", s.replyID)
	}
	if !s.replyReq.ReplyInThread || s.replyReq.MsgType != msgTypeInteractive || s.replyReq.Content != `{"card":2}` {
		t.Fatalf("reply body = %+v", s.replyReq)
	}
}

// TestReply_TextToReplyToMessage: a plain-text ack to an explicit
// ReplyToMessageID (not in-thread).
func TestReply_TextToReplyToMessage(t *testing.T) {
	s := &fakeSender{}
	c, _ := wiredChannel(s, gateway.OutboundCredentials{AppSecret: "secret"})

	if err := c.Reply(context.Background(), channel.ReplyTarget{ReplyToMessageID: "om_cmd"}, "done"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if s.replyReq == nil {
		t.Fatal("Reply must call ReplyMessage when ReplyToMessageID set")
	}
	if s.replyID != "om_cmd" {
		t.Fatalf("reply anchor = %q, want om_cmd", s.replyID)
	}
	if s.replyReq.ReplyInThread {
		t.Fatal("reply to a specific message must not set ReplyInThread")
	}
	if s.replyReq.MsgType != msgTypeText {
		t.Fatalf("MsgType = %q, want text", s.replyReq.MsgType)
	}
	var body map[string]string
	if err := json.Unmarshal([]byte(s.replyReq.Content), &body); err != nil {
		t.Fatalf("reply content not valid JSON: %v", err)
	}
	if body["text"] != "done" {
		t.Fatalf("reply text = %q, want done", body["text"])
	}
}

// TestReply_TextToChatFallback: no anchor at all → SendMessage text to chat.
func TestReply_TextToChatFallback(t *testing.T) {
	s := &fakeSender{}
	c, _ := wiredChannel(s, gateway.OutboundCredentials{AppSecret: "secret"})

	if err := c.Reply(context.Background(), channel.ReplyTarget{ExternalChatID: "oc_9"}, "hi"); err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if s.replyReq != nil {
		t.Fatal("Reply with no anchor must not call ReplyMessage")
	}
	if s.sendReq == nil || s.sendReq.ReceiveID != "oc_9" || s.sendReq.MsgType != msgTypeText {
		t.Fatalf("send req = %+v", s.sendReq)
	}
}

// TestEdit_Patches: Edit PATCHes the card payload onto the given message id.
func TestEdit_Patches(t *testing.T) {
	s := &fakeSender{}
	c, _ := wiredChannel(s, gateway.OutboundCredentials{AppSecret: "secret"})

	if err := c.Edit(context.Background(), channel.ReplyTarget{}, gateway.MessageRef{ID: "om_edit"}, channel.Card{Payload: []byte(`{"card":3}`)}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if s.patchID != "om_edit" || s.patchBody != `{"card":3}` {
		t.Fatalf("patch id=%q body=%q", s.patchID, s.patchBody)
	}
}

// TestEdit_RequiresMessageID: an empty ref id is a programmer error, not a
// transport call.
func TestEdit_RequiresMessageID(t *testing.T) {
	s := &fakeSender{}
	c, _ := wiredChannel(s, gateway.OutboundCredentials{AppSecret: "secret"})

	if err := c.Edit(context.Background(), channel.ReplyTarget{}, gateway.MessageRef{}, channel.Card{Payload: []byte(`{}`)}); err == nil {
		t.Fatal("Edit with empty message id must error")
	}
	if s.patchID != "" {
		t.Fatal("Edit must not call PatchMessage with an empty id")
	}
}

// TestSend_TransportError propagates a resolution failure unchanged.
func TestSend_TransportError(t *testing.T) {
	tr := &fakeTransport{err: errors.New("resolve boom")}
	c := New(Config{AppID: "cli_test"}, WithTransport(tr))
	if _, err := c.Send(context.Background(), channel.ReplyTarget{ExternalChatID: "oc_1"}, channel.Card{}); err == nil {
		t.Fatal("Send must propagate transport resolution error")
	}
}
