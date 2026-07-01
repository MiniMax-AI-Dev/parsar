package inbound

import (
	"context"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
)

// These parity tests pin the neutral policy helpers gateway.IsSelfSender /
// gateway.ShouldSkipGroupWithoutMention against the production Feishu-typed
// filters isSelfMessage / isGroupMessageWithoutBotMention they will replace.
// For equivalent inputs the decisions must be identical, so a later slice can
// switch handleMessage onto the neutral helpers with no behavior change.

// cfgWithBot builds the connector-config jsonb the legacy filters decode to
// find the bot's open_id. Empty open → a config present but without a bot id.
func cfgWithBot(botOpenID string) []byte {
	return []byte(`{"connectors":{"feishu":{"bot_open_id":"` + botOpenID + `"}}}`)
}

// neutralFromFeishu derives the neutral InboundEvent from a Feishu-typed
// event using the same field mapping the adapter's Normalize performs. The
// adapter↔FeishuInboundEventFromWebhook parity is pinned separately in
// channel/feishu/event_test.go; here we only exercise the policy decision.
func neutralFromFeishu(fe gateway.FeishuInboundEvent) gateway.InboundEvent {
	ct := "group"
	switch strings.ToLower(strings.TrimSpace(fe.ChatType)) {
	case "":
		ct = ""
	case "p2p":
		ct = "dm"
	}
	return gateway.InboundEvent{
		ExternalMessageID: fe.MessageID,
		ExternalChatID:    fe.ChatID,
		ExternalRootID:    fe.RootID,
		ChatType:          ct,
		SenderIsBot:       fe.IsBotSender(),
		MentionedUserIDs:  fe.MentionOpenIDs,
		Sender:            gateway.ExternalIdentity{LocalUserID: fe.SenderOpenID},
	}
}

// fakeThreadHist satisfies both the legacy feishuThreadHistoryLookup
// (HasFeishuThreadInboundHistory) and the neutral gateway.ThreadHistoryLookup
// (HasThreadInboundHistory) so one double feeds both code paths.
type fakeThreadHist struct {
	has bool
	err error
}

func (f fakeThreadHist) HasFeishuThreadInboundHistory(_ context.Context, _, _ string) (bool, error) {
	return f.has, f.err
}

func (f fakeThreadHist) HasThreadInboundHistory(_ context.Context, _, _ string) (bool, error) {
	return f.has, f.err
}

func TestIsSelfSenderParity(t *testing.T) {
	cases := []struct {
		name      string
		botOpenID string
		senderOID string
	}{
		{"self", "ou_bot", "ou_bot"},
		{"other", "ou_bot", "ou_user"},
		{"empty sender", "ou_bot", ""},
		{"empty bot", "", "ou_user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := isSelfMessage(cfgWithBot(tc.botOpenID), tc.senderOID)
			ev := gateway.InboundEvent{Sender: gateway.ExternalIdentity{LocalUserID: tc.senderOID}}
			neu := gateway.IsSelfSender(ev, tc.botOpenID)
			if old != neu {
				t.Fatalf("self decision drift: legacy=%v neutral=%v (bot=%q sender=%q)",
					old, neu, tc.botOpenID, tc.senderOID)
			}
		})
	}
}

func TestShouldSkipGroupWithoutMentionParity(t *testing.T) {
	cases := []struct {
		name      string
		botOpenID string
		fe        gateway.FeishuInboundEvent
		hist      fakeThreadHist
	}{
		{
			name:      "p2p never skipped",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "p2p", MessageID: "om1", ChatID: "oc1"},
		},
		{
			name:      "bot sender already targeted",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", SenderType: "app", MessageID: "om2", ChatID: "oc1"},
		},
		{
			name:      "mention hits bot",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", MessageID: "om3", ChatID: "oc1", MentionOpenIDs: []string{"ou_other", "ou_bot"}},
		},
		{
			name:      "mention misses bot",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", MessageID: "om4", ChatID: "oc1", MentionOpenIDs: []string{"ou_other"}},
		},
		{
			name:      "mention present but bot id unknown",
			botOpenID: "",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", MessageID: "om5", ChatID: "oc1", MentionOpenIDs: []string{"ou_other"}},
		},
		{
			name:      "no mention, thread has history",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", MessageID: "om6", RootID: "om_root", ChatID: "oc1"},
			hist:      fakeThreadHist{has: true},
		},
		{
			name:      "no mention, no history",
			botOpenID: "ou_bot",
			fe:        gateway.FeishuInboundEvent{ChatType: "group", MessageID: "om7", RootID: "om_root", ChatID: "oc1"},
			hist:      fakeThreadHist{has: false},
		},
	}
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			old := isGroupMessageWithoutBotMention(ctx, tc.hist, cfgWithBot(tc.botOpenID), tc.fe)
			neu := gateway.ShouldSkipGroupWithoutMention(ctx, tc.hist, neutralFromFeishu(tc.fe), tc.botOpenID)
			if old != neu {
				t.Fatalf("group-mention decision drift: legacy=%v neutral=%v", old, neu)
			}
		})
	}
}
