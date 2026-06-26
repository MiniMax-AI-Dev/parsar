package gateway

import (
	"encoding/json"
	"strings"
)

type FeishuMessageEvent struct {
	Event struct {
		Message struct {
			MessageID string `json:"message_id"`
			ChatID    string `json:"chat_id"`
			ChatType  string `json:"chat_type"`
			ThreadID  string `json:"thread_id"`
			Content   string `json:"content"`
		} `json:"message"`
		Sender struct {
			SenderID struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
			} `json:"sender_id"`
			TenantKey string `json:"tenant_key"`
		} `json:"sender"`
	} `json:"event"`
}

type feishuTextContent struct {
	Text string `json:"text"`
}

type FeishuSendMessage struct {
	ReceiveIDType string `json:"receive_id_type"`
	ReceiveID     string `json:"receive_id"`
	MsgType       string `json:"msg_type"`
	Content       string `json:"content"`
}

func NormalizeFeishuInbound(event FeishuMessageEvent) InboundMessage {
	message := event.Event.Message
	text := extractFeishuText(message.Content)
	actorID := strings.TrimSpace(event.Event.Sender.SenderID.OpenID)
	if actorID == "" {
		actorID = strings.TrimSpace(event.Event.Sender.SenderID.UserID)
	}
	if actorID == "" {
		actorID = strings.TrimSpace(event.Event.Sender.SenderID.UnionID)
	}
	return InboundMessage{
		Gateway:         "feishu",
		Message:         MessageRef{ID: message.MessageID, Text: text},
		Actor:           ActorRef{ID: actorID},
		ConversationRef: ConversationRef{ID: message.ChatID, ThreadID: message.ThreadID},
		Metadata:        map[string]any{"chat_type": message.ChatType, "tenant_key": event.Event.Sender.TenantKey},
	}
}

func FeishuDeliveryPayload(delivery Delivery) FeishuSendMessage {
	receiveIDType := "chat_id"
	receiveID := delivery.ExternalChatID
	if strings.TrimSpace(delivery.ExternalThreadID) != "" {
		receiveID = delivery.ExternalChatID
	}
	content, _ := json.Marshal(feishuTextContent{Text: delivery.Text})
	return FeishuSendMessage{ReceiveIDType: receiveIDType, ReceiveID: receiveID, MsgType: "text", Content: string(content)}
}

func extractFeishuText(content string) string {
	return ParseFeishuMessageContent("", content, nil).Text
}
