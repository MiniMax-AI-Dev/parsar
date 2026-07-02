package slack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/slack-go/slack"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeHistoryLister records the routing args FetchHistory resolved and
// replays a canned page. The two methods cover conversations.history (whole
// channel) and conversations.replies (a single thread).
type fakeHistoryLister struct {
	historyMsgs []slack.Message
	historyNext string
	historyErr  error
	historyArgs struct {
		channelID string
		cursor    string
		limit     int
	}

	repliesMsgs []slack.Message
	repliesNext string
	repliesErr  error
	repliesArgs struct {
		channelID string
		threadTS  string
		cursor    string
		limit     int
	}
}

func (f *fakeHistoryLister) history(_ context.Context, channelID, cursor string, limit int) ([]slack.Message, string, error) {
	f.historyArgs.channelID = channelID
	f.historyArgs.cursor = cursor
	f.historyArgs.limit = limit
	if f.historyErr != nil {
		return nil, "", f.historyErr
	}
	return f.historyMsgs, f.historyNext, nil
}

func (f *fakeHistoryLister) replies(_ context.Context, channelID, threadTS, cursor string, limit int) ([]slack.Message, string, error) {
	f.repliesArgs.channelID = channelID
	f.repliesArgs.threadTS = threadTS
	f.repliesArgs.cursor = cursor
	f.repliesArgs.limit = limit
	if f.repliesErr != nil {
		return nil, "", f.repliesErr
	}
	return f.repliesMsgs, f.repliesNext, nil
}

func newHistoryChannel(t *testing.T, l slackHistoryLister) *Channel {
	t.Helper()
	return New(Config{AppID: "A123", BotToken: "xoxb-test"}, withHistoryLister(l))
}

// TestFetchHistory_WholeChannel calls conversations.history (ExternalThreadID
// is empty), forwards the cursor, and projects the response oldest-first.
func TestFetchHistory_WholeChannel(t *testing.T) {
	l := &fakeHistoryLister{
		// Slack returns newest-first; the fetcher reverses to oldest-first.
		historyMsgs: []slack.Message{
			{Msg: slack.Msg{Timestamp: "1700000002.000200", User: "u_bot", Text: "newest", SubType: "bot_message"}},
			{Msg: slack.Msg{Timestamp: "1700000001.000100", User: "u1", Text: "oldest"}},
		},
		historyNext: "page_2",
	}
	c := newHistoryChannel(t, l)

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID: "C123",
		SourceAppID:    "A123",
		Limit:          5,
		Cursor:         "page_1",
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.historyArgs.channelID != "C123" || l.historyArgs.cursor != "page_1" || l.historyArgs.limit != 5 {
		t.Fatalf("history routing lost: %+v", l.historyArgs)
	}
	if l.repliesArgs.threadTS != "" {
		t.Fatalf("whole-channel branch must not call replies (got threadTS=%q)", l.repliesArgs.threadTS)
	}
	if res.Cap != slackHistoryCap {
		t.Fatalf("Cap = %d, want %d", res.Cap, slackHistoryCap)
	}
	if res.NextCursor != "page_2" {
		t.Fatalf("NextCursor = %q, want page_2", res.NextCursor)
	}
	if len(res.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Messages))
	}
	// Newest-first reversed to oldest-first.
	if res.Messages[0].Text != "oldest" || res.Messages[1].Text != "newest" {
		t.Fatalf("order = [%q, %q], want [oldest, newest]", res.Messages[0].Text, res.Messages[1].Text)
	}
	if res.Messages[0].ExternalMessageID != "1700000001.000100" {
		t.Fatalf("ExternalMessageID = %q, want oldest ts", res.Messages[0].ExternalMessageID)
	}
	if res.Messages[1].FromBot != true {
		t.Fatalf("bot_message subtype must mark FromBot=true (msg=%+v)", res.Messages[1])
	}
	if res.Messages[0].FromBot {
		t.Fatalf("user message must not be FromBot")
	}
}

// TestFetchHistory_ThreadScope calls conversations.replies when the agent asks
// for thread-scoped history. The thread_ts is forwarded verbatim.
func TestFetchHistory_ThreadScope(t *testing.T) {
	l := &fakeHistoryLister{
		repliesMsgs: []slack.Message{
			{Msg: slack.Msg{Timestamp: "1700000050.000050", ThreadTimestamp: "1700000040.000040", User: "u1", Text: "reply"}},
		},
	}
	c := newHistoryChannel(t, l)

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID:   "C123",
		ExternalThreadID: "1700000040.000040",
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.repliesArgs.channelID != "C123" || l.repliesArgs.threadTS != "1700000040.000040" || l.repliesArgs.limit != 10 {
		t.Fatalf("replies routing = %+v", l.repliesArgs)
	}
	if l.historyArgs.channelID != "" {
		t.Fatalf("thread branch must not call history (got channelID=%q)", l.historyArgs.channelID)
	}
	if len(res.Messages) != 1 || res.Messages[0].ThreadID != "1700000040.000040" {
		t.Fatalf("thread message projection = %+v", res.Messages)
	}
}

// TestFetchHistory_RateLimit translates a slack-go *RateLimitedError into the
// neutral *channel.RateLimitedError so imhistory.Gate can back off and retry.
func TestFetchHistory_RateLimit(t *testing.T) {
	l := &fakeHistoryLister{historyErr: &slack.RateLimitedError{RetryAfter: 3 * time.Second}}
	c := newHistoryChannel(t, l)

	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "C123"})
	if err == nil {
		t.Fatal("FetchHistory must propagate rate limit")
	}
	var rl *channel.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %T, want *channel.RateLimitedError", err)
	}
	if rl.Platform != channel.PlatformSlack {
		t.Fatalf("Platform = %q, want %q", rl.Platform, channel.PlatformSlack)
	}
	if rl.RetryAfter != 3*time.Second {
		t.Fatalf("RetryAfter = %s, want 3s", rl.RetryAfter)
	}
}

// TestFetchHistory_RequiresChatID rejects an empty chat id before the lister
// runs.
func TestFetchHistory_RequiresChatID(t *testing.T) {
	l := &fakeHistoryLister{}
	c := newHistoryChannel(t, l)
	if _, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{}); err == nil {
		t.Fatal("FetchHistory with empty chat id must error")
	}
	if l.historyArgs.channelID != "" || l.repliesArgs.channelID != "" {
		t.Fatal("must not call the lister when chat id is empty")
	}
}

// TestFetchHistory_HidesMetadataSubtypes drops channel_join / topic / etc.
// from the projected page so the agent only sees authored content.
func TestFetchHistory_HidesMetadataSubtypes(t *testing.T) {
	l := &fakeHistoryLister{
		historyMsgs: []slack.Message{
			{Msg: slack.Msg{Timestamp: "1.0", User: "u1", Text: "hi", SubType: "channel_join"}},
			{Msg: slack.Msg{Timestamp: "2.0", User: "u1", Text: "real msg"}},
		},
	}
	c := newHistoryChannel(t, l)
	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "C123"})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "real msg" {
		t.Fatalf("messages = %+v, want only the authored one", res.Messages)
	}
}

// TestFetchHistory_ClampLimit silently clamps the agent's Limit to the
// platform's per-page cap.
func TestFetchHistory_ClampLimit(t *testing.T) {
	l := &fakeHistoryLister{}
	c := newHistoryChannel(t, l)
	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID: "C123",
		Limit:          9999,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.historyArgs.limit != slackHistoryCap {
		t.Fatalf("limit = %d, want clamp to %d", l.historyArgs.limit, slackHistoryCap)
	}
}
