package teams

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeHistoryLister records the routing args FetchHistory resolved and
// replays a canned page. The two methods cover the Graph endpoints the
// fetcher branches on: /teams/{}/channels/{}/messages (whole channel) and
// /teams/{}/channels/{}/messages/{}/replies (a single thread).
type fakeHistoryLister struct {
	channelMsgs   []graphChatMessage
	channelNext   string
	channelErr    error
	channelArgs   struct{ teamID, channelID string; top int }

	repliesMsgs   []graphChatMessage
	repliesNext   string
	repliesErr    error
	repliesArgs   struct{ teamID, channelID, messageID string; top int }
}

func (f *fakeHistoryLister) channelMessages(_ context.Context, teamID, channelID string, top int) ([]graphChatMessage, string, error) {
	f.channelArgs.teamID, f.channelArgs.channelID, f.channelArgs.top = teamID, channelID, top
	if f.channelErr != nil {
		return nil, "", f.channelErr
	}
	return f.channelMsgs, f.channelNext, nil
}

func (f *fakeHistoryLister) channelMessageReplies(_ context.Context, teamID, channelID, messageID string, top int) ([]graphChatMessage, string, error) {
	f.repliesArgs.teamID, f.repliesArgs.channelID, f.repliesArgs.messageID, f.repliesArgs.top = teamID, channelID, messageID, top
	if f.repliesErr != nil {
		return nil, "", f.repliesErr
	}
	return f.repliesMsgs, f.repliesNext, nil
}

func newHistoryChannel(t *testing.T, l teamsHistoryLister) *Channel {
	t.Helper()
	store := NewMemoryConversationStore()
	store.Put("conv-1", ConversationRef{TeamID: "team-9", GraphChannelID: "chan-19:chan-7"})
	return New(Config{AppID: "app-123"},
		WithConversationStore(store),
		WithHistoryLister(l),
	)
}

// jsonMsg parses a graphChatMessage fixture from JSON. graphChatMessage.From
// is an anonymous *struct carrying json tags, so the exact type cannot be
// rebuilt in a struct-literal from a different file in this package — Go's
// type identity compares tags. JSON round-trip is the cheapest way to land a
// value whose From points at the right nested fields.
func jsonMsg(t *testing.T, raw string) graphChatMessage {
	t.Helper()
	var m graphChatMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("parse graphChatMessage fixture: %v (raw=%q)", err, raw)
	}
	return m
}

// TestFetchHistory_WholeChannel calls /teams/{}/channels/{}/messages when
// ExternalThreadID is empty, projects the Graph chatMessage into the neutral
// shape, and reverses newest-first to oldest-first.
func TestFetchHistory_WholeChannel(t *testing.T) {
	newest := jsonMsg(t, `{"id":"msg_new","from":{"application":{"id":"app-bot","displayName":"bot"}}}`)
	newest.CreatedDateTime = time.Unix(1700000002, 0)
	oldest := jsonMsg(t, `{"id":"msg_old","from":{"user":{"id":"u1","displayName":"alice"}}}`)
	oldest.CreatedDateTime = time.Unix(1700000001, 0)
	l := &fakeHistoryLister{
		// Graph returns newest-first; the fetcher reverses.
		channelMsgs: []graphChatMessage{newest, oldest},
		channelNext: "next-page",
	}
	c := newHistoryChannel(t, l)

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID: "conv-1",
		SourceAppID:    "app-123",
		Limit:          3,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.channelArgs.teamID != "team-9" || l.channelArgs.channelID != "chan-19:chan-7" {
		t.Fatalf("routing tuple lost: %+v", l.channelArgs)
	}
	if l.repliesArgs.messageID != "" {
		t.Fatalf("whole-channel branch must not call replies (got messageID=%q)", l.repliesArgs.messageID)
	}
	if res.Cap != teamsHistoryCap {
		t.Fatalf("Cap = %d, want %d", res.Cap, teamsHistoryCap)
	}
	if res.NextCursor != "next-page" {
		t.Fatalf("NextCursor = %q, want next-page", res.NextCursor)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Messages))
	}
	// Reversed: oldest first.
	if res.Messages[0].ExternalMessageID != "msg_old" || res.Messages[1].ExternalMessageID != "msg_new" {
		t.Fatalf("order = [%s, %s], want [msg_old, msg_new]", res.Messages[0].ExternalMessageID, res.Messages[1].ExternalMessageID)
	}
	if !res.Messages[1].FromBot {
		t.Fatal("app author must mark FromBot=true")
	}
	if res.Messages[0].FromBot {
		t.Fatal("user author must not be FromBot")
	}
	if res.Messages[0].SenderID != "u1" || res.Messages[0].SenderName != "alice" {
		t.Fatalf("sender = %+v, want u1/alice", res.Messages[0])
	}
}

// TestFetchHistory_ThreadScope calls the replies endpoint with the
// ExternalThreadID as the message id (Teams calls this replyToId).
func TestFetchHistory_ThreadScope(t *testing.T) {
	l := &fakeHistoryLister{
		repliesMsgs: []graphChatMessage{
			{ID: "msg_reply", ReplyToID: "msg_root", CreatedDateTime: time.Unix(1700000050, 0)},
		},
	}
	c := newHistoryChannel(t, l)

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID:   "conv-1",
		ExternalThreadID: "msg_root",
		Limit:            5,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.repliesArgs.messageID != "msg_root" || l.repliesArgs.top != 5 {
		t.Fatalf("replies routing = %+v", l.repliesArgs)
	}
	if l.channelArgs.top != 0 {
		t.Fatalf("thread branch must not call channel messages (got top=%d)", l.channelArgs.top)
	}
	if len(res.Messages) != 1 || res.Messages[0].ThreadID != "msg_root" {
		t.Fatalf("thread message projection = %+v", res.Messages)
	}
}

// TestFetchHistory_RateLimit surfaces a 429 as a *channel.RateLimitedError.
// The production lister wraps Graph's 429 in this neutral type; we feed the
// fake lister a hand-built error to confirm the fetcher does NOT swallow it.
func TestFetchHistory_RateLimit(t *testing.T) {
	l := &fakeHistoryLister{
		channelErr: &channel.RateLimitedError{
			Platform:   channel.PlatformTeams,
			RetryAfter: 5 * time.Second,
			Err:        errors.New("graph 429"),
		},
	}
	c := newHistoryChannel(t, l)

	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "conv-1"})
	if err == nil {
		t.Fatal("FetchHistory must propagate rate limit")
	}
	var rl *channel.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %T, want *channel.RateLimitedError", err)
	}
	if rl.Platform != channel.PlatformTeams {
		t.Fatalf("Platform = %q, want %q", rl.Platform, channel.PlatformTeams)
	}
	if rl.RetryAfter != 5*time.Second {
		t.Fatalf("RetryAfter = %s, want 5s", rl.RetryAfter)
	}
}

// TestFetchHistory_RequiresConversationRef fails when the runner never
// primed the conversation store (no inbound has happened yet).
func TestFetchHistory_RequiresConversationRef(t *testing.T) {
	l := &fakeHistoryLister{}
	c := New(Config{AppID: "app-123"},
		WithConversationStore(NewMemoryConversationStore()),
		WithHistoryLister(l),
	)
	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "conv-unprimed"})
	if err == nil {
		t.Fatal("FetchHistory without a primed conversation ref must error")
	}
	if l.channelArgs.top != 0 {
		t.Fatal("must not call the lister when the conversation ref is missing")
	}
}

// TestFetchHistory_RejectsPersonalChat surfaces the case where the inbound
// was a personal / groupChat (no team-id) — the fetcher cannot address Graph
// history and reports an error so the handler falls back to the never-fail
// empty page.
func TestFetchHistory_RejectsPersonalChat(t *testing.T) {
	l := &fakeHistoryLister{}
	store := NewMemoryConversationStore()
	store.Put("conv-personal", ConversationRef{TeamID: "", GraphChannelID: "19:abc@thread.v2"})
	c := New(Config{AppID: "app-123"},
		WithConversationStore(store),
		WithHistoryLister(l),
	)
	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "conv-personal"})
	if err == nil {
		t.Fatal("personal/groupChat conversation has no team-id and must error")
	}
	if l.channelArgs.top != 0 {
		t.Fatal("must not call the lister for an unaddressable conversation")
	}
}

// TestFetchHistory_ClampLimit silently clamps the agent's Limit to the
// Graph per-page ceiling (50).
func TestFetchHistory_ClampLimit(t *testing.T) {
	l := &fakeHistoryLister{}
	c := newHistoryChannel(t, l)
	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID: "conv-1",
		Limit:          9999,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.channelArgs.top != teamsHistoryCap {
		t.Fatalf("top = %d, want clamp to %d", l.channelArgs.top, teamsHistoryCap)
	}
}

// TestFetchHistory_StripsHTMLContent projects an html body into a plain-text
// snippet so the agent sees a readable string in the history page.
func TestFetchHistory_StripsHTMLContent(t *testing.T) {
	l := &fakeHistoryLister{
		channelMsgs: []graphChatMessage{
			{ID: "m1", Body: struct {
				Content     string `json:"content"`
				ContentType string `json:"contentType"`
			}{Content: "<p>hello <b>world</b></p>", ContentType: "html"}},
		},
	}
	c := newHistoryChannel(t, l)
	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "conv-1"})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if got := res.Messages[0].Text; got != "hello  world" {
		t.Fatalf("Text = %q, want %q", got, "hello  world")
	}
}
