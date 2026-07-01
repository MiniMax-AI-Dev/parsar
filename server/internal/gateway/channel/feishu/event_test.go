package feishu

import (
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// These parity tests pin the neutral facts the N1 enrichment added to
// gateway.InboundEvent against the production Feishu-typed builder
// gateway.FeishuInboundEventFromWebhook. Both consume the SAME webhook JSON,
// so any drift between the neutral Normalize path and the live router input
// fails here. This is the safety net that lets later slices switch the
// router/manager onto the neutral event without behavior change.

// assertNeutralMatchesFeishu feeds one webhook body through both builders and
// asserts every neutral fact equals its Feishu-typed counterpart.
func assertNeutralMatchesFeishu(t *testing.T, body string) {
	t.Helper()
	raw := []byte(body)

	ev, err := newTestChannel().Normalize(raw)
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	fe, err := gateway.FeishuInboundEventFromWebhook(raw)
	if err != nil {
		t.Fatalf("FeishuInboundEventFromWebhook: %v", err)
	}

	if ev.BotID != fe.AppID {
		t.Errorf("BotID = %q, want app_id %q", ev.BotID, fe.AppID)
	}
	if ev.ExternalMessageID != fe.MessageID {
		t.Errorf("ExternalMessageID = %q, want %q", ev.ExternalMessageID, fe.MessageID)
	}
	if ev.ExternalChatID != fe.ChatID {
		t.Errorf("ExternalChatID = %q, want %q", ev.ExternalChatID, fe.ChatID)
	}
	if ev.ExternalRootID != fe.RootID {
		t.Errorf("ExternalRootID = %q, want root_id %q", ev.ExternalRootID, fe.RootID)
	}
	if ev.ReplyTo != fe.ParentID {
		t.Errorf("ReplyTo = %q, want parent_id %q", ev.ReplyTo, fe.ParentID)
	}
	if ev.Sender.PlatformUserID != fe.SenderUnionID {
		t.Errorf("Sender.PlatformUserID = %q, want union_id %q", ev.Sender.PlatformUserID, fe.SenderUnionID)
	}
	if ev.Sender.LocalUserID != fe.SenderOpenID {
		t.Errorf("Sender.LocalUserID = %q, want open_id %q", ev.Sender.LocalUserID, fe.SenderOpenID)
	}
	if ev.Sender.TenantKey != fe.TenantKey {
		t.Errorf("Sender.TenantKey = %q, want %q", ev.Sender.TenantKey, fe.TenantKey)
	}
	if ev.Text != fe.Text {
		t.Errorf("Text = %q, want %q", ev.Text, fe.Text)
	}
	if ev.SenderIsBot != fe.IsBotSender() {
		t.Errorf("SenderIsBot = %v, want IsBotSender() %v", ev.SenderIsBot, fe.IsBotSender())
	}
	// Mention parity: same open_ids in the same order.
	if len(ev.MentionedUserIDs) != len(fe.MentionOpenIDs) {
		t.Fatalf("MentionedUserIDs = %v, want %v", ev.MentionedUserIDs, fe.MentionOpenIDs)
	}
	for i := range ev.MentionedUserIDs {
		if ev.MentionedUserIDs[i] != fe.MentionOpenIDs[i] {
			t.Errorf("MentionedUserIDs[%d] = %q, want %q", i, ev.MentionedUserIDs[i], fe.MentionOpenIDs[i])
		}
	}
	// ThreadKey must be byte-identical — it's the conversation grouping key.
	if ev.ThreadKey() != fe.ThreadKey() {
		t.Errorf("ThreadKey() = %q, want %q", ev.ThreadKey(), fe.ThreadKey())
	}
}

func TestNormalizeParity_GroupMentionThreadReply(t *testing.T) {
	// Group chat, human sender, @-mentions the bot's open_id, reply inside a
	// thread so root_id is set and ThreadKey must derive from root_id.
	assertNeutralMatchesFeishu(t, `{
		"header":{"app_id":"cli_test","tenant_key":"tk_hdr"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_sender","union_id":"on_union","user_id":"uu_1"},"sender_type":"user","tenant_key":"tk_1"},
			"message":{
				"message_id":"om_reply","root_id":"om_root","parent_id":"om_parent",
				"chat_id":"oc_group","chat_type":"group","thread_id":"omt_panel",
				"message_type":"text","content":"{\"text\":\"@_user_1 deploy\"}",
				"mentions":[{"key":"@_user_1","id":{"open_id":"ou_bot","user_id":"uu_bot"}}]
			}
		}
	}`)
}

func TestNormalizeParity_P2PNoMentions(t *testing.T) {
	// Direct message: chat_type p2p → neutral "dm", no mentions, no thread.
	assertNeutralMatchesFeishu(t, `{
		"header":{"app_id":"cli_test"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_dm","union_id":"on_dm"},"sender_type":"user","tenant_key":"tk_dm"},
			"message":{"message_id":"om_dm","chat_id":"oc_dm","chat_type":"p2p","message_type":"text","content":"{\"text\":\"hello\"}"}
		}
	}`)
}

func TestNormalizeParity_BotSender(t *testing.T) {
	// A non-user sender (another app posting a card) → SenderIsBot true.
	assertNeutralMatchesFeishu(t, `{
		"header":{"app_id":"cli_test"},
		"event":{
			"sender":{"sender_id":{"open_id":"ou_app","union_id":"on_app"},"sender_type":"app","tenant_key":"tk_app"},
			"message":{"message_id":"om_card","chat_id":"oc_group2","chat_type":"group","message_type":"text","content":"{\"text\":\"status\"}"}
		}
	}`)
}

func TestNormalizeChatType(t *testing.T) {
	cases := map[string]string{
		"p2p":   "dm",
		"group": "group",
		"":      "",
		"P2P":   "dm",
	}
	for in, want := range cases {
		if got := neutralChatType(in); got != want {
			t.Errorf("neutralChatType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeThreadKeyFallsBackToMessageID(t *testing.T) {
	// Top-level message: no root_id, so ThreadKey must fall back to the
	// message id (mirrors FeishuInboundEvent.ThreadKey).
	ev, err := newTestChannel().Normalize([]byte(`{
		"header":{"app_id":"cli_test"},
		"event":{
			"sender":{"sender_id":{"union_id":"on_x"},"sender_type":"user"},
			"message":{"message_id":"om_top","chat_id":"oc_1","chat_type":"group","message_type":"text","content":"{\"text\":\"hi\"}"}
		}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if ev.ThreadKey() != "om_top" {
		t.Errorf("ThreadKey() = %q, want om_top", ev.ThreadKey())
	}
}
