package feishu

import (
	"context"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// historySender is a CardSender that also implements historyLister, so the
// sender.(historyLister) assertion in FetchHistory succeeds. It records the
// list args and replays a canned page.
type historySender struct {
	fakeSender
	gotAppSecret string
	gotChatID    string
	gotPageToken string
	items        []gateway.FeishuFetchedMessage
	next         string
	err          error
}

func (h *historySender) ListMessagesByChatPage(_ context.Context, appSecret, chatID, pageToken string) ([]gateway.FeishuFetchedMessage, string, error) {
	h.gotAppSecret = appSecret
	h.gotChatID = chatID
	h.gotPageToken = pageToken
	if h.err != nil {
		return nil, "", h.err
	}
	return h.items, h.next, nil
}

// wiredHistory builds a Channel whose transport hands back a history-capable
// sender, mirroring wiredChannel but for the FetchHistory path.
func wiredHistory(s *historySender, creds gateway.OutboundCredentials) *Channel {
	return New(Config{AppID: "cli_test"}, WithTransport(historyTransport{sender: s, creds: creds}))
}

// historyTransport returns the history-capable sender as the CardSender so the
// adapter can assert it to historyLister.
type historyTransport struct {
	sender *historySender
	creds  gateway.OutboundCredentials
}

func (t historyTransport) OutboundSender(_ context.Context, _ string) (CardSender, gateway.OutboundCredentials, error) {
	return t.sender, t.creds, nil
}

// TestFetchHistory_ReversesAndProjects locks the core projection: Feishu's
// newest-first page is reversed to oldest-first, text is extracted, FromBot
// comes from sender_type=="app", timestamps parse from millis, and the cursor
// + cap pass through.
func TestFetchHistory_ReversesAndProjects(t *testing.T) {
	s := &historySender{
		next: "page_2",
		items: []gateway.FeishuFetchedMessage{
			{MessageID: "om_new", MsgType: "text", BodyContent: `{"text":"newest"}`, SenderID: "u_bot", SenderType: "app", CreateTime: "1700000002000"},
			{MessageID: "om_old", MsgType: "text", BodyContent: `{"text":"oldest"}`, SenderID: "u_1", SenderType: "user", CreateTime: "1700000001000"},
		},
	}
	c := wiredHistory(s, gateway.OutboundCredentials{AppID: "cli_test", AppSecret: "secret"})

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "oc_1"})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if s.gotAppSecret != "secret" || s.gotChatID != "oc_1" {
		t.Fatalf("list args: appSecret=%q chatID=%q", s.gotAppSecret, s.gotChatID)
	}
	if res.Cap != feishuHistoryCap {
		t.Fatalf("Cap = %d, want %d", res.Cap, feishuHistoryCap)
	}
	if res.NextCursor != "page_2" {
		t.Fatalf("NextCursor = %q, want page_2", res.NextCursor)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("len(messages) = %d, want 2", len(res.Messages))
	}
	// Reversed: oldest first.
	if res.Messages[0].ExternalMessageID != "om_old" || res.Messages[1].ExternalMessageID != "om_new" {
		t.Fatalf("order = [%s, %s], want [om_old, om_new]", res.Messages[0].ExternalMessageID, res.Messages[1].ExternalMessageID)
	}
	if res.Messages[0].Text != "oldest" || res.Messages[1].Text != "newest" {
		t.Fatalf("texts = [%q, %q]", res.Messages[0].Text, res.Messages[1].Text)
	}
	if res.Messages[0].FromBot {
		t.Fatal("user message must not be FromBot")
	}
	if !res.Messages[1].FromBot {
		t.Fatal("app message must be FromBot")
	}
	if res.Messages[1].CreatedAt.UnixMilli() != 1700000002000 {
		t.Fatalf("CreatedAt = %v, want millis 1700000002000", res.Messages[1].CreatedAt)
	}
}

// TestFetchHistory_ThreadScope keeps only messages matching the requested
// thread (via ThreadID, RootID, or the message id itself).
func TestFetchHistory_ThreadScope(t *testing.T) {
	s := &historySender{
		items: []gateway.FeishuFetchedMessage{
			{MessageID: "om_a", MsgType: "text", BodyContent: `{"text":"in-thread by thread_id"}`, ThreadID: "om_root"},
			{MessageID: "om_b", MsgType: "text", BodyContent: `{"text":"other thread"}`, ThreadID: "om_elsewhere"},
			{MessageID: "om_root", MsgType: "text", BodyContent: `{"text":"the root itself"}`},
			{MessageID: "om_c", MsgType: "text", BodyContent: `{"text":"in-thread by root_id"}`, RootID: "om_root"},
		},
	}
	c := wiredHistory(s, gateway.OutboundCredentials{AppSecret: "secret"})

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "oc_1", ExternalThreadID: "om_root"})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	got := map[string]bool{}
	for _, m := range res.Messages {
		got[m.ExternalMessageID] = true
	}
	if got["om_b"] {
		t.Fatal("om_b belongs to another thread and must be filtered out")
	}
	for _, want := range []string{"om_a", "om_root", "om_c"} {
		if !got[want] {
			t.Fatalf("thread message %s missing from result", want)
		}
	}
}

// TestFetchHistory_LimitKeepsNewest trims to the newest Limit after reversal,
// so the retained window is the tail (most recent) of the oldest-first slice.
func TestFetchHistory_LimitKeepsNewest(t *testing.T) {
	s := &historySender{
		items: []gateway.FeishuFetchedMessage{
			{MessageID: "om_3", MsgType: "text", BodyContent: `{"text":"3"}`, CreateTime: "3000"},
			{MessageID: "om_2", MsgType: "text", BodyContent: `{"text":"2"}`, CreateTime: "2000"},
			{MessageID: "om_1", MsgType: "text", BodyContent: `{"text":"1"}`, CreateTime: "1000"},
		},
	}
	c := wiredHistory(s, gateway.OutboundCredentials{AppSecret: "secret"})

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "oc_1", Limit: 2})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Messages))
	}
	// Newest two, oldest-first: om_2 then om_3.
	if res.Messages[0].ExternalMessageID != "om_2" || res.Messages[1].ExternalMessageID != "om_3" {
		t.Fatalf("kept = [%s, %s], want [om_2, om_3]", res.Messages[0].ExternalMessageID, res.Messages[1].ExternalMessageID)
	}
}

// TestFetchHistory_RequiresChatID rejects an empty chat id before any transport
// call.
func TestFetchHistory_RequiresChatID(t *testing.T) {
	s := &historySender{}
	c := wiredHistory(s, gateway.OutboundCredentials{AppSecret: "secret"})
	if _, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{}); err == nil {
		t.Fatal("FetchHistory with empty chat id must error")
	}
	if s.gotChatID != "" {
		t.Fatal("must not call the lister when chat id is empty")
	}
}

// TestFetchHistory_ListError propagates the lister's error unchanged.
func TestFetchHistory_ListError(t *testing.T) {
	s := &historySender{err: errors.New("list boom")}
	c := wiredHistory(s, gateway.OutboundCredentials{AppSecret: "secret"})
	if _, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "oc_1"}); err == nil {
		t.Fatal("FetchHistory must propagate the lister error")
	}
}
