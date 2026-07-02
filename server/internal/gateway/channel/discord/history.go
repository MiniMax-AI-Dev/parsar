// Package discord — live chat-history projection.
//
// FetchHistory makes the Discord adapter a channel.HistoryFetcher: it pulls one
// page of recent chat messages via the Discord REST API
// (ChannelMessages(channelID, limit, beforeID, ...)) and maps the response into
// the neutral HistoryMessage shape. The implementation rides the same
// discordgo session factory the outbound transport already uses; a thin
// discordHistoryLister seam keeps the I/O out of the unit tests.
//
// The two routing modes the fetcher branches on:
//
//   - ExternalThreadID == "" → top-level channel history. The Discord REST
//     endpoint paginates with the Before parameter (a message snowflake id);
//     snowflakes are monotonic so "before X" is also "older than X".
//   - ExternalThreadID != "" → the thread channel itself. Discord threads are
//     channels, so ExternalThreadID takes the place of ExternalChatID and
//     ExternalChatID is ignored; the agent passes a thread channel id
//     verbatim. (Discord does not have a separate thread-replies endpoint —
//     listing a thread channel IS the reply listing.)
//
// Both branches translate a discordgo *RateLimitError into the neutral
// *channel.RateLimitedError so imhistory.Gate can block-and-retry on the
// platform's suggested RetryAfter. The agent never sees a 429.
package discord

import (
	"context"
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// discordHistoryLister is the subset of *discordgo.Session FetchHistory needs.
// The default factory (defaultHistoryListerFactory) wraps a real session;
// tests pass a fake via withHistoryLister.
type discordHistoryLister interface {
	messages(ctx context.Context, channelID, beforeID string, limit int) (msgs []*discordgo.Message, err error)
}

// discordHistoryListerClient is the concrete history-lister shape.
type discordHistoryListerClient struct{ api *discordgo.Session }

func (s discordHistoryListerClient) messages(ctx context.Context, channelID, beforeID string, limit int) ([]*discordgo.Message, error) {
	return s.api.ChannelMessages(channelID, limit, beforeID, "", "", discordgo.WithContext(ctx))
}

// defaultHistoryListerFactory adapts a resolved bot token to a real history
// lister. Mirrors defaultSenderFactory in outbound.go so the same bot can
// ship messages and pull history through the same session.
func defaultHistoryListerFactory(token string) discordHistoryLister {
	// discordgo.New never errors for a static bot token (it only parses the
	// token shape), so the error is intentionally discarded; an invalid token
	// surfaces at the first REST call as a 401.
	api, _ := discordgo.New("Bot " + token)
	return discordHistoryListerClient{api: api}
}

// discordHistoryCap is the neutral per-request ceiling reported back to the
// agent. Discord's ChannelMessages caps at 100 messages per page; the agent's
// Limit argument is silently clamped.
const discordHistoryCap = 100

// Compile-time assertion: the Discord adapter is a HistoryFetcher.
var _ channel.HistoryFetcher = (*Channel)(nil)

// FetchHistory returns a bounded, oldest-first page of recent chat messages.
// The thread case (ExternalThreadID != "") reuses ExternalThreadID as the
// channel id — Discord threads ARE channels. The whole-chat case paginates
// with the Before cursor.
//
// SourceAppID is the bound workspace-bot's Discord app id; the bot token is
// resolved per call through c.creds, so a rotated vault secret takes effect
// without recreating the channel.
func (c *Channel) FetchHistory(ctx context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	target := strings.TrimSpace(req.ExternalChatID)
	threadScope := strings.TrimSpace(req.ExternalThreadID)
	if threadScope != "" {
		// Discord threads are channels themselves — prefer the thread channel id
		// when the agent asked for thread-scoped history.
		target = threadScope
	}
	if target == "" {
		return channel.FetchHistoryResult{}, errors.New("discord history: channel id required")
	}

	lister, err := c.historyListerFor(ctx, req.SourceAppID)
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}

	limit := req.Limit
	if limit <= 0 || limit > discordHistoryCap {
		limit = discordHistoryCap
	}

	rawMsgs, err := lister.messages(ctx, target, strings.TrimSpace(req.Cursor), limit)
	if err != nil {
		if rl, ok := err.(*discordgo.RateLimitError); ok && rl.RateLimit != nil {
			return channel.FetchHistoryResult{}, &channel.RateLimitedError{
				Platform:   channel.PlatformDiscord,
				RetryAfter: rl.RateLimit.RetryAfter,
				Err:        err,
			}
		}
		return channel.FetchHistoryResult{}, err
	}

	// Discord returns newest-first; reverse to the neutral oldest-first order.
	// Each message's snowflake id is also a usable Before cursor for the next
	// (older) page; carry the smallest id seen so the caller can keep paging.
	msgs := make([]channel.HistoryMessage, 0, len(rawMsgs))
	var oldestID string
	for _, m := range rawMsgs {
		if m == nil || isDiscordHiddenMessage(m) {
			continue
		}
		msgs = append(msgs, discordMessageToHistory(m))
		if oldestID == "" || (m.ID != "" && m.ID < oldestID) {
			oldestID = m.ID
		}
	}
	reverseHistory(msgs)
	if req.Limit > 0 && len(msgs) > req.Limit {
		msgs = msgs[len(msgs)-req.Limit:]
	}
	return channel.FetchHistoryResult{Messages: msgs, NextCursor: oldestID, Cap: discordHistoryCap}, nil
}

// historyListerFor returns the configured history lister, or builds a real
// one from the resolved bot token. Mirrors senderFor in outbound.go: per-call
// credential resolve, no caching.
func (c *Channel) historyListerFor(ctx context.Context, sourceAppID string) (discordHistoryLister, error) {
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

// discordMessageToHistory maps a single *discordgo.Message into the neutral
// HistoryMessage shape. FromBot is the cheapest heuristic (Author.Bot); the
// bot's own echoes are not useful to the agent's history regardless of who
// sent them.
func discordMessageToHistory(m *discordgo.Message) channel.HistoryMessage {
	var senderID, senderName string
	if m.Author != nil {
		senderID = m.Author.ID
		senderName = m.Author.Username
	}
	return channel.HistoryMessage{
		ExternalMessageID: m.ID,
		SenderID:          senderID,
		SenderName:        senderName,
		Text:              m.Content,
		CreatedAt:         m.Timestamp.UTC(),
		FromBot:           m.Author != nil && m.Author.Bot,
	}
}

// isDiscordHiddenMessage filters system messages that are metadata, not
// authored content the agent should see in a "recent chat" page.
func isDiscordHiddenMessage(m *discordgo.Message) bool {
	switch m.Type {
	case discordgo.MessageTypeChannelNameChange,
		discordgo.MessageTypeChannelIconChange,
		discordgo.MessageTypeChannelPinnedMessage,
		discordgo.MessageTypeGuildMemberJoin,
		discordgo.MessageTypeCall,
		discordgo.MessageTypeThreadCreated,
		discordgo.MessageTypeThreadStarterMessage:
		return true
	}
	return false
}

func reverseHistory(m []channel.HistoryMessage) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}

// withHistoryLister injects a fake history lister so FetchHistory is
// unit-testable without discordgo's HTTP client. Lives next to the other
// Option helpers for symmetry, but is unexported — only test files in this
// package use it.
func withHistoryLister(l discordHistoryLister) Option {
	return func(c *Channel) { c.historyLister = l }
}