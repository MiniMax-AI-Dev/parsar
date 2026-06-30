package feishu

import (
	"encoding/json"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// feishuEventEnvelope is the v2.0 `im.message.receive_v1` callback shape,
// limited to the fields the neutral InboundEvent needs. The production
// inbound path (feishuinbound.inboundEventFromSDK) maps the full Lark SDK
// type; PR #1's skeleton decodes the documented JSON directly and reuses the
// SAME text parser (gateway.ParseFeishuMessageContent) so the Text field has
// zero drift from production. Full SDK-fidelity mapping moves here in PR #2.
type feishuEventEnvelope struct {
	Header struct {
		AppID     string `json:"app_id"`
		TenantKey string `json:"tenant_key"`
	} `json:"header"`
	Event struct {
		Sender struct {
			SenderID struct {
				OpenID  string `json:"open_id"`
				UserID  string `json:"user_id"`
				UnionID string `json:"union_id"`
			} `json:"sender_id"`
			SenderType string `json:"sender_type"`
			TenantKey  string `json:"tenant_key"`
		} `json:"sender"`
		Message struct {
			MessageID   string `json:"message_id"`
			RootID      string `json:"root_id"`
			ParentID    string `json:"parent_id"`
			ChatID      string `json:"chat_id"`
			ChatType    string `json:"chat_type"`
			ThreadID    string `json:"thread_id"`
			MessageType string `json:"message_type"`
			Content     string `json:"content"`
			Mentions    []struct {
				Key string `json:"key"`
				ID  struct {
					OpenID string `json:"open_id"`
				} `json:"id"`
			} `json:"mentions"`
		} `json:"message"`
	} `json:"event"`
}

// Normalize converts a verified Feishu event envelope into the neutral
// gateway.InboundEvent. Text extraction is delegated to the production parser
// gateway.ParseFeishuMessageContent so mention stripping and post/image
// handling match the live feishuinbound path.
func (c *Channel) Normalize(verified []byte) (gateway.InboundEvent, error) {
	var env feishuEventEnvelope
	if err := json.Unmarshal(verified, &env); err != nil {
		return gateway.InboundEvent{}, err
	}
	msg := env.Event.Message

	mentionKeys := make([]string, 0, len(msg.Mentions))
	mentionOpenIDs := make([]string, 0, len(msg.Mentions))
	for _, m := range msg.Mentions {
		if k := strings.TrimSpace(m.Key); k != "" {
			mentionKeys = append(mentionKeys, k)
		}
		if id := strings.TrimSpace(m.ID.OpenID); id != "" {
			mentionOpenIDs = append(mentionOpenIDs, id)
		}
	}
	parsed := gateway.ParseFeishuMessageContent(msg.MessageType, msg.Content, mentionKeys)

	tenantKey := env.Event.Sender.TenantKey
	if tenantKey == "" {
		tenantKey = env.Header.TenantKey
	}
	threadID := msg.ThreadID
	if threadID == "" {
		threadID = msg.RootID
	}

	return gateway.InboundEvent{
		Platform:          string(c.Platform()),
		BotID:             env.Header.AppID,
		ExternalMessageID: msg.MessageID,
		ExternalChatID:    msg.ChatID,
		ExternalThreadID:  threadID,
		ExternalRootID:    msg.RootID,
		Sender: gateway.ExternalIdentity{
			// union_id is the cross-app stable identity used by
			// store.FindUserIDByPlatformSubject for account linking; open_id
			// is the per-app local id used for self-message filtering and
			// the credential-form submit pin.
			PlatformUserID: env.Event.Sender.SenderID.UnionID,
			LocalUserID:    env.Event.Sender.SenderID.OpenID,
			TenantKey:      tenantKey,
		},
		Text:             parsed.Text,
		ChatType:         neutralChatType(msg.ChatType),
		SenderIsBot:      isBotSenderType(env.Event.Sender.SenderType),
		MentionedUserIDs: mentionOpenIDs,
		Raw:              json.RawMessage(verified),
		ReplyTo:          msg.ParentID,
	}, nil
}

// neutralChatType maps Feishu's chat_type to the neutral ChatType vocabulary
// (p2p→dm, anything else non-empty→group). Empty stays empty.
func neutralChatType(feishuChatType string) string {
	switch strings.ToLower(strings.TrimSpace(feishuChatType)) {
	case "":
		return ""
	case "p2p":
		return "dm"
	default:
		return "group"
	}
}

// isBotSenderType mirrors gateway.FeishuInboundEvent.IsBotSender: a non-empty
// sender_type other than "user" means an app/bot authored the message.
func isBotSenderType(senderType string) bool {
	t := strings.TrimSpace(strings.ToLower(senderType))
	return t != "" && t != "user"
}
