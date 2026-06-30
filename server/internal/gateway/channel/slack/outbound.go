// Package slack — outbound transport (PR #4b).
//
// Reply / Send / Edit turn neutral channel calls into Slack Web API calls
// (chat.postMessage / chat.update) via slack-go. To keep the transport
// unit-testable without the HTTP client, the adapter talks to a small
// slackSender interface expressed in explicit arguments (channel, thread,
// text, blocks) rather than slack.MsgOption closures; clientSender adapts
// *slack.Client to it, and tests inject a fake.
//
// The bot token is resolved per call through the CredentialResolver so a
// re-installed app rotates without a restart — mirroring the Feishu adapter,
// which keeps exactly one interactive-send path (Send) to avoid the
// double-send class of bug. Edit takes the channel id from the ReplyTarget
// and the message ts from the MessageRef, since Slack identifies a message by
// (channel, ts) — unlike Feishu's globally-unique message_id.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/slack-go/slack"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// slackWireMessage is the Block Kit payload shape the renderers emit; the
// Blocks field decodes the JSON block array into typed slack-go blocks (its
// Blocks.UnmarshalJSON expects exactly such an array).
type slackWireMessage struct {
	Text   string       `json:"text"`
	Blocks slack.Blocks `json:"blocks"`
}

// slackSender is the minimal outbound surface the adapter needs, in explicit
// arguments so it is testable without slack-go's HTTP client. clientSender
// adapts *slack.Client; tests pass a fake.
type slackSender interface {
	post(ctx context.Context, channelID, threadTS, text string, blocks []slack.Block) (ts string, err error)
	update(ctx context.Context, channelID, ts, text string, blocks []slack.Block) error
}

// clientSender adapts *slack.Client to slackSender, translating explicit
// arguments into slack.MsgOption closures.
type clientSender struct{ api *slack.Client }

func (s clientSender) post(ctx context.Context, channelID, threadTS, text string, blocks []slack.Block) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if len(blocks) > 0 {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := s.api.PostMessageContext(ctx, channelID, opts...)
	return ts, err
}

func (s clientSender) update(ctx context.Context, channelID, ts, text string, blocks []slack.Block) error {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if len(blocks) > 0 {
		opts = append(opts, slack.MsgOptionBlocks(blocks...))
	}
	_, _, _, err := s.api.UpdateMessageContext(ctx, channelID, ts, opts...)
	return err
}

// defaultSenderFactory builds a real slack-go-backed sender from a bot token.
func defaultSenderFactory(token string) slackSender {
	return clientSender{api: slack.New(token)}
}

// senderFor resolves the bot token and builds a sender via the injected
// factory. Per-call resolution keeps token rotation hot. The target's
// TenantKey (Slack team_id) is passed as the resolver botID so a
// multi-workspace deployment mints the per-team token; the static/env
// resolver ignores it and returns the one configured token.
func (c *Channel) senderFor(ctx context.Context, target channel.ReplyTarget) (slackSender, error) {
	botID := strings.TrimSpace(target.TenantKey)
	if botID == "" {
		botID = c.appID
	}
	cred, err := c.creds.Resolve(ctx, botID)
	if err != nil {
		return nil, err
	}
	return c.newSender(cred.AppSecret), nil
}

// threadAnchor returns the Slack thread_ts to reply under: the thread root ts
// when present, else an explicit reply-to message ts. Empty means top-level.
func threadAnchor(target channel.ReplyTarget) string {
	if ts := strings.TrimSpace(target.ExternalThreadID); ts != "" {
		return ts
	}
	return strings.TrimSpace(target.ReplyToMessageID)
}

// cardContent decodes a rendered Block Kit card into its fallback text and
// typed blocks. An empty payload yields no blocks (plain-text path).
func cardContent(card channel.Card) (string, []slack.Block, error) {
	if len(card.Payload) == 0 {
		return "", nil, nil
	}
	var wm slackWireMessage
	if err := json.Unmarshal(card.Payload, &wm); err != nil {
		return "", nil, fmt.Errorf("slack channel: decode card payload: %w", err)
	}
	return wm.Text, wm.Blocks.BlockSet, nil
}

// Reply posts a plain-text command acknowledgement, threaded when the target
// carries a thread anchor.
func (c *Channel) Reply(ctx context.Context, target channel.ReplyTarget, text string) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	_, err = sender.post(ctx, target.ExternalChatID, threadAnchor(target), text, nil)
	return err
}

// Send posts a new Block Kit message and returns its ts as the MessageRef id.
// This is the adapter's single interactive-send path.
func (c *Channel) Send(ctx context.Context, target channel.ReplyTarget, card channel.Card) (gateway.MessageRef, error) {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	text, blocks, err := cardContent(card)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	ts, err := sender.post(ctx, target.ExternalChatID, threadAnchor(target), text, blocks)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	return gateway.MessageRef{ID: ts, Text: text}, nil
}

// Edit re-renders an existing message's blocks in place (chat.update) — the
// inflight "executing" → terminal transition pins one (channel, ts). The
// channel comes from the ReplyTarget; the ts from the MessageRef.
func (c *Channel) Edit(ctx context.Context, target channel.ReplyTarget, ref gateway.MessageRef, card channel.Card) error {
	sender, err := c.senderFor(ctx, target)
	if err != nil {
		return err
	}
	ts := strings.TrimSpace(ref.ID)
	if ts == "" {
		return fmt.Errorf("slack channel: edit requires a message ts")
	}
	channelID := strings.TrimSpace(target.ExternalChatID)
	if channelID == "" {
		return fmt.Errorf("slack channel: edit requires a channel id")
	}
	text, blocks, err := cardContent(card)
	if err != nil {
		return err
	}
	return sender.update(ctx, channelID, ts, text, blocks)
}
