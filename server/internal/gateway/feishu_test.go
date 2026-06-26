package gateway

import "testing"

func TestNormalizeFeishuInbound(t *testing.T) {
	var event FeishuMessageEvent
	event.Event.Message.MessageID = "om_123"
	event.Event.Message.ChatID = "oc_456"
	event.Event.Message.ChatType = "group"
	event.Event.Message.ThreadID = "omt_789"
	event.Event.Message.Content = `{"text":"@后端Agent 看一下"}`
	event.Event.Sender.SenderID.OpenID = "ou_123"
	event.Event.Sender.TenantKey = "tenant_1"

	inbound := NormalizeFeishuInbound(event)
	if inbound.Gateway != "feishu" || inbound.Message.ID != "om_123" || inbound.Message.Text != "@后端Agent 看一下" {
		t.Fatalf("unexpected inbound message: %+v", inbound)
	}
	if inbound.Actor.ID != "ou_123" || inbound.ConversationRef.ID != "oc_456" || inbound.ConversationRef.ThreadID != "omt_789" {
		t.Fatalf("unexpected inbound refs: %+v", inbound)
	}
	if inbound.Metadata["chat_type"] != "group" || inbound.Metadata["tenant_key"] != "tenant_1" {
		t.Fatalf("unexpected metadata: %+v", inbound.Metadata)
	}
}

func TestFeishuDeliveryPayload(t *testing.T) {
	payload := FeishuDeliveryPayload(Delivery{ExternalChatID: "oc_456", Text: "hello"})
	if payload.ReceiveIDType != "chat_id" || payload.ReceiveID != "oc_456" || payload.MsgType != "text" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	if payload.Content != `{"text":"hello"}` {
		t.Fatalf("unexpected content: %s", payload.Content)
	}
}
