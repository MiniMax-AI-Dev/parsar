// Package slack — live chat-history projection.
//
// FetchHistory makes the Slack adapter a channel.HistoryFetcher: it pulls one
// page of recent chat messages via the Slack Web API
// (conversations.history / conversations.replies) and maps the response into
// the neutral HistoryMessage shape. The implementation rides the same
// slack-go client factory the outbound transport already uses; a thin
// slackHistoryLister seam keeps the I/O out of the unit tests.
//
// The two routing modes the fetcher branches on:
//
//   - ExternalThreadID == "" → conversations.history. The returned page is
//     channel-wide, oldest-first. Slack's cap is 15 per page (Tier-3).
//   - ExternalThreadID != "" → conversations.replies with ts=threadID. A
//     platform-native thread_ts (the root message's ts). Same 15-per-page
//     cap; the agent's thread_id argument is the platform-native id verbatim.
//
// Both branches translate a slack-go *RateLimitedError into the neutral
// *channel.RateLimitedError so imhistory.Gate can block-and-retry on the
// platform's suggested RetryAfter. The agent never sees a 429.
package slack

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/slack-go/slack"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// slackHistoryLister is the subset of *slack.Client FetchHistory needs. The
// default factory (defaultHistoryListerFactory) wraps a real client; tests
// pass a fake via withHistoryLister.
type slackHistoryLister interface {
	history(ctx context.Context, channelID, cursor string, limit int) (msgs []slack.Message, nextCursor string, err error)
	replies(ctx context.Context, channelID, threadTS, cursor string, limit int) (msgs []slack.Message, nextCursor string, err error)
}

// slackHistoryListerClient is the concrete history-lister shape. defaultHistoryListerFactory
// adapts *slack.Client to it.
type slackHistoryListerClient struct{ api *slack.Client }

func (s slackHistoryListerClient) history(ctx context.Context, channelID, cursor string, limit int) ([]slack.Message, string, error) {
	params := &slack.GetConversationHistoryParameters{ChannelID: channelID, Cursor: cursor, Limit: limit}
	resp, err := s.api.GetConversationHistoryContext(ctx, params)
	if err != nil {
		return nil, "", err
	}
	if resp == nil {
		return nil, "", nil
	}
	return resp.Messages, resp.ResponseMetaData.NextCursor, nil
}

func (s slackHistoryListerClient) replies(ctx context.Context, channelID, threadTS, cursor string, limit int) ([]slack.Message, string, error) {
	params := &slack.GetConversationRepliesParameters{ChannelID: channelID, Timestamp: threadTS, Cursor: cursor, Limit: limit}
	msgs, _, next, err := s.api.GetConversationRepliesContext(ctx, params)
	return msgs, next, err
}

// defaultHistoryListerFactory adapts a resolved bot token to a real history
// lister. Mirrors defaultSenderFactory in outbound.go so the same Slack app
// can ship messages and pull history through the same *slack.Client instance
// family.
func defaultHistoryListerFactory(token string) slackHistoryLister {
	return slackHistoryListerClient{api: slack.New(token)}
}

// slackHistoryCap is the neutral per-request ceiling reported back to the
// agent. Slack's conversations.history / conversations.replies cap at 15
// messages per page; the agent's Limit argument is silently clamped.
const slackHistoryCap = 15

// Compile-time assertion: the Slack adapter is a HistoryFetcher.
var _ channel.HistoryFetcher = (*Channel)(nil)

// FetchHistory returns a bounded, oldest-first page of recent chat messages.
// The thread case (ExternalThreadID != "") calls conversations.replies; the
// whole-chat case calls conversations.history. Both branches share the same
// normalize / reverse / clamp pipeline.
//
// SourceAppID is the bound workspace-bot's Slack app id; the bot token is
// resolved per call through c.creds, so a rotated vault secret takes effect
// without recreating the channel.
func (c *Channel) FetchHistory(ctx context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	channelID := strings.TrimSpace(req.ExternalChatID)
	if channelID == "" {
		return channel.FetchHistoryResult{}, errors.New("slack history: channel id required")
	}

	lister, err := c.historyListerFor(ctx, req.SourceAppID)
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}

	limit := req.Limit
	if limit <= 0 || limit > slackHistoryCap {
		limit = slackHistoryCap
	}
	threadTS := strings.TrimSpace(req.ExternalThreadID)

	var (
		rawMsgs    []slack.Message
		nextCursor string
	)
	if threadTS == "" {
		rawMsgs, nextCursor, err = lister.history(ctx, channelID, strings.TrimSpace(req.Cursor), limit)
	} else {
		rawMsgs, nextCursor, err = lister.replies(ctx, channelID, threadTS, strings.TrimSpace(req.Cursor), limit)
	}
	if err != nil {
		if rl, ok := err.(*slack.RateLimitedError); ok {
			return channel.FetchHistoryResult{}, &channel.RateLimitedError{
				Platform:   channel.PlatformSlack,
				RetryAfter: rl.RetryAfter,
				Err:        err,
			}
		}
		return channel.FetchHistoryResult{}, err
	}

	// Slack returns newest-first; reverse to the neutral oldest-first order.
	msgs := make([]channel.HistoryMessage, 0, len(rawMsgs))
	for _, m := range rawMsgs {
		if isSlackHiddenMessage(m) {
			continue
		}
		msgs = append(msgs, slackMessageToHistory(m))
	}
	reverseHistory(msgs)
	if req.Limit > 0 && len(msgs) > req.Limit {
		msgs = msgs[len(msgs)-req.Limit:]
	}
	return channel.FetchHistoryResult{Messages: msgs, NextCursor: nextCursor, Cap: slackHistoryCap}, nil
}

// historyListerFor returns the configured history lister, or builds a real
// one from the resolved bot token. Keeps the seam uniform with senderFor in
// outbound.go: per-call credential resolve, no caching.
func (c *Channel) historyListerFor(ctx context.Context, sourceAppID string) (slackHistoryLister, error) {
	if c.historyLister != nil {
		return c.historyLister, nil
	}
	botID := strings.TrimSpace(sourceAppID)
	if botID == "" {
		botID = c.appID
	}
	cred, err := c.creds.Resolve(ctx, botID)
	if err != nil {
		return nil, err
	}
	return defaultHistoryListerFactory(cred.AppSecret), nil
}

// slackMessageToHistory maps a single slack.Msg into the neutral
// HistoryMessage shape. SubType-typed rows that are not user-visible (channel
// joins, topic changes, etc.) are filtered by isSlackHiddenMessage so the
// agent sees only authored messages. FromBot is the simplest heuristic
// (SubType=bot_message OR a non-empty BotID); a stricter version would key
// against the workspace's bound app id, but the bot's own echoes are not
// useful to the agent's history regardless of who sent them.
func slackMessageToHistory(m slack.Message) channel.HistoryMessage {
	return channel.HistoryMessage{
		ExternalMessageID: m.Timestamp,
		SenderID:          m.User,
		SenderName:        m.Username,
		Text:              m.Text,
		ThreadID:          m.ThreadTimestamp,
		CreatedAt:         slackTsToTime(m.Timestamp),
		FromBot:           m.BotID != "" || m.SubType == "bot_message",
	}
}

// isSlackHiddenMessage filters message subtypes that are metadata, not
// authored content the agent should see in a "recent chat" page.
func isSlackHiddenMessage(m slack.Message) bool {
	switch m.SubType {
	case "channel_join", "channel_leave", "channel_topic", "channel_purpose",
		"channel_name", "channel_archive", "channel_unarchive",
		"pinned_item", "unpinned_item":
		return true
	}
	return false
}

// slackTsToTime parses Slack's "<unix>.<micros>" timestamp into a UTC time.
// A blank or unparseable ts yields the zero time — a missing timestamp must
// not fail the whole page.
func slackTsToTime(ts string) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return time.Time{}
	}
	head, _, _ := strings.Cut(ts, ".")
	secs, err := parseInt64(head)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}

// parseInt64 is a tiny wrapper that avoids pulling strconv into this file's
// import set beyond what slack-go's own client already drags in.
func parseInt64(s string) (int64, error) {
	var n int64
	var sign int64 = 1
	for i, r := range s {
		c := r - '0'
		if c > 9 {
			if i == 0 && r == '-' {
				sign = -1
				continue
			}
			return 0, errors.New("not a number")
		}
		n = n*10 + int64(c)
	}
	return n * sign, nil
}

func reverseHistory(m []channel.HistoryMessage) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}
