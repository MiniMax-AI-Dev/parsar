package discord

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// fakeHistoryLister records the routing args FetchHistory resolved and
// replays a canned page. Discord has only one lister method — messages() —
// because a Discord thread IS a channel, so the thread case reuses the
// channel id (see FetchHistory).
type fakeHistoryLister struct {
	msgs []*discordgo.Message
	err  error
	args struct {
		channelID string
		beforeID  string
		limit     int
	}
}

func (f *fakeHistoryLister) messages(_ context.Context, channelID, beforeID string, limit int) ([]*discordgo.Message, error) {
	f.args.channelID = channelID
	f.args.beforeID = beforeID
	f.args.limit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.msgs, nil
}

func newHistoryChannel(t *testing.T, l discordHistoryLister) *Channel {
	t.Helper()
	return New(Config{AppID: "999", BotToken: "tok"}, withHistoryLister(l))
}

// TestFetchHistory_WholeChannel calls ChannelMessages with ExternalChatID
// when ExternalThreadID is empty, paginates via the Before cursor, and
// projects oldest-first.
func TestFetchHistory_WholeChannel(t *testing.T) {
	// Discord returns newest-first; the fetcher reverses. Newest snowflake
	// has the highest id; oldest has the lowest.
	l := &fakeHistoryLister{
		msgs: []*discordgo.Message{
			{ID: "200", Author: &discordgo.User{ID: "u1", Username: "alice"}, Content: "newest", Timestamp: time.Unix(1700000002, 0)},
			{ID: "100", Author: &discordgo.User{ID: "u_bot", Username: "bot", Bot: true}, Content: "oldest", Timestamp: time.Unix(1700000001, 0)},
		},
	}
	c := newHistoryChannel(t, l)

	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID: "C123",
		SourceAppID:    "999",
		Limit:          5,
		Cursor:         "150",
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.args.channelID != "C123" || l.args.beforeID != "150" || l.args.limit != 5 {
		t.Fatalf("routing lost: %+v", l.args)
	}
	if res.Cap != discordHistoryCap {
		t.Fatalf("Cap = %d, want %d", res.Cap, discordHistoryCap)
	}
	// The oldest message's snowflake is the next Before cursor.
	if res.NextCursor != "100" {
		t.Fatalf("NextCursor = %q, want %q (oldest seen id)", res.NextCursor, "100")
	}
	if len(res.Messages) != 2 {
		t.Fatalf("len = %d, want 2", len(res.Messages))
	}
	// Reversed to oldest-first.
	if res.Messages[0].Text != "oldest" || res.Messages[1].Text != "newest" {
		t.Fatalf("order = [%q, %q], want [oldest, newest]", res.Messages[0].Text, res.Messages[1].Text)
	}
	if !res.Messages[0].FromBot {
		t.Fatalf("bot author must mark FromBot=true (msg=%+v)", res.Messages[0])
	}
	if res.Messages[1].FromBot {
		t.Fatalf("user message must not be FromBot")
	}
}

// TestFetchHistory_ThreadScope routes ExternalThreadID as the channel id
// because Discord threads ARE channels; the ExternalChatID is ignored.
func TestFetchHistory_ThreadScope(t *testing.T) {
	l := &fakeHistoryLister{}
	c := newHistoryChannel(t, l)

	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{
		ExternalChatID:   "C_PARENT",
		ExternalThreadID: "T_THREAD",
		Limit:            3,
	})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if l.args.channelID != "T_THREAD" {
		t.Fatalf("thread routing: channelID = %q, want T_THREAD (thread id replaces chat id)", l.args.channelID)
	}
}

// TestFetchHistory_RateLimit translates a discordgo *RateLimitError into the
// neutral *channel.RateLimitedError so imhistory.Gate can back off and retry.
func TestFetchHistory_RateLimit(t *testing.T) {
	l := &fakeHistoryLister{err: &discordgo.RateLimitError{
		RateLimit: &discordgo.RateLimit{
			TooManyRequests: &discordgo.TooManyRequests{RetryAfter: 2 * time.Second},
			URL:             "https://discord.com/api/v9/channels/123/messages",
		},
	}}
	c := newHistoryChannel(t, l)

	_, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "C123"})
	if err == nil {
		t.Fatal("FetchHistory must propagate rate limit")
	}
	var rl *channel.RateLimitedError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %T, want *channel.RateLimitedError", err)
	}
	if rl.Platform != channel.PlatformDiscord {
		t.Fatalf("Platform = %q, want %q", rl.Platform, channel.PlatformDiscord)
	}
	if rl.RetryAfter != 2*time.Second {
		t.Fatalf("RetryAfter = %s, want 2s", rl.RetryAfter)
	}
}

// TestFetchHistory_RequiresChannelID rejects an empty chat id (and empty
// thread id) before the lister runs.
func TestFetchHistory_RequiresChannelID(t *testing.T) {
	l := &fakeHistoryLister{}
	c := newHistoryChannel(t, l)
	if _, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{}); err == nil {
		t.Fatal("FetchHistory with empty target must error")
	}
	if l.args.channelID != "" {
		t.Fatal("must not call the lister when target is empty")
	}
}

// TestFetchHistory_HidesSystemTypes drops channel_name_change / thread_created
// / pinned_message / etc. from the projected page.
func TestFetchHistory_HidesSystemTypes(t *testing.T) {
	l := &fakeHistoryLister{
		msgs: []*discordgo.Message{
			{ID: "1", Type: discordgo.MessageTypeChannelNameChange, Content: "old→new"},
			{ID: "2", Author: &discordgo.User{ID: "u1", Username: "alice"}, Content: "real"},
		},
	}
	c := newHistoryChannel(t, l)
	res, err := c.FetchHistory(context.Background(), channel.FetchHistoryRequest{ExternalChatID: "C123"})
	if err != nil {
		t.Fatalf("FetchHistory: %v", err)
	}
	if len(res.Messages) != 1 || res.Messages[0].Text != "real" {
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
	if l.args.limit != discordHistoryCap {
		t.Fatalf("limit = %d, want clamp to %d", l.args.limit, discordHistoryCap)
	}
}
