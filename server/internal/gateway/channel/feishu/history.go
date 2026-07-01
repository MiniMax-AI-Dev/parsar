// Package feishu — live chat-history projection (im-history-tool).
//
// FetchHistory makes the Feishu adapter a channel.HistoryFetcher: it pulls one
// page of recent chat messages via the tenant client's im/v1/messages list,
// reusing the outbound Transport's cached client + credentials rather than
// owning any HTTP machinery. Feishu has no punishing per-call history limit
// (unlike Slack), so this is the first platform wired end-to-end for the
// on-demand history MCP tool.
package feishu

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// historyLister is the subset of *gateway.FeishuTenantClient FetchHistory
// needs. The Transport's CardSender is that concrete client, so FetchHistory
// type-asserts to this instead of widening the CardSender interface.
type historyLister interface {
	ListMessagesByChatPage(ctx context.Context, appSecret, chatID, pageToken string) ([]gateway.FeishuFetchedMessage, string, error)
}

// feishuHistoryCap is the neutral per-request ceiling reported back to the
// agent. Feishu's list page_size is server-fixed at 50, so one FetchHistory
// returns at most one page; Limit only trims it.
const feishuHistoryCap = 50

// Compile-time assertion: the Feishu adapter is a HistoryFetcher.
var _ channel.HistoryFetcher = (*Channel)(nil)

// FetchHistory returns a bounded, oldest-first page of recent chat messages.
// Feishu lists newest-first; we reverse to the neutral order. When Limit > 0 it
// trims to the newest Limit messages. Cursor is Feishu's page_token for the
// adjacent older page; NextCursor carries the next one ("" when exhausted).
func (c *Channel) FetchHistory(ctx context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	chatID := strings.TrimSpace(req.ExternalChatID)
	if chatID == "" {
		return channel.FetchHistoryResult{}, errors.New("feishu history: chat id required")
	}
	sender, creds, err := c.outbound(ctx)
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}
	lister, ok := sender.(historyLister)
	if !ok {
		return channel.FetchHistoryResult{}, errors.New("feishu history: transport client cannot list messages")
	}
	items, next, err := lister.ListMessagesByChatPage(ctx, creds.AppSecret, chatID, strings.TrimSpace(req.Cursor))
	if err != nil {
		return channel.FetchHistoryResult{}, err
	}

	threadScope := strings.TrimSpace(req.ExternalThreadID)
	msgs := make([]channel.HistoryMessage, 0, len(items))
	for _, it := range items {
		if threadScope != "" && it.ThreadID != threadScope && it.RootID != threadScope && it.MessageID != threadScope {
			continue
		}
		parsed := gateway.ParseFeishuMessageContent(it.MsgType, it.BodyContent, nil)
		msgs = append(msgs, channel.HistoryMessage{
			ExternalMessageID: it.MessageID,
			SenderID:          it.SenderID,
			Text:              parsed.Text,
			ThreadID:          firstNonEmptyHistory(it.ThreadID, it.RootID),
			CreatedAt:         feishuMillisToTime(it.CreateTime),
			FromBot:           strings.EqualFold(it.SenderType, "app"),
		})
	}
	reverseHistory(msgs) // newest-first -> oldest-first
	if req.Limit > 0 && len(msgs) > req.Limit {
		msgs = msgs[len(msgs)-req.Limit:] // keep the newest Limit
	}
	return channel.FetchHistoryResult{Messages: msgs, NextCursor: next, Cap: feishuHistoryCap}, nil
}

// feishuMillisToTime parses Feishu's millisecond-epoch create_time string. An
// unparseable value yields the zero time rather than an error — a missing
// timestamp must not fail the whole page.
func feishuMillisToTime(ms string) time.Time {
	ms = strings.TrimSpace(ms)
	if ms == "" {
		return time.Time{}
	}
	n, err := strconv.ParseInt(ms, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.UnixMilli(n).UTC()
}

func firstNonEmptyHistory(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func reverseHistory(m []channel.HistoryMessage) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}
