// Package feishu — outbound transport (PR #3a.2).
//
// Reply / Edit / Send turn neutral channel calls into Feishu IM API calls.
// They are thin wrappers over the tenant client's SendMessage / ReplyMessage
// / PatchMessage: the heavy machinery (client pool, tenant_access_token
// cache, per-bot app_secret resolution) is injected via Transport so the
// adapter never duplicates it.
//
// Reference calibration: OpenClaw keeps the send wrapper inside the channel
// plugin and the queue/retry/lifecycle in core; Hermes' second parallel send
// path caused a double-send bug (#17261). So the adapter keeps exactly one
// interactive-send path (Send) — Edit only PATCHes, Reply only posts text.
package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// ErrNoTransport is returned when an outbound method runs before a Transport
// is injected (New(cfg) without WithTransport).
var ErrNoTransport = errors.New("feishu channel: no outbound transport configured")

// Feishu message types used on the outbound path.
const (
	msgTypeInteractive = "interactive"
	msgTypeText        = "text"
)

// CardSender is the minimal Feishu IM surface the adapter calls.
// *gateway.FeishuTenantClient satisfies it; tests pass a fake.
type CardSender interface {
	SendMessage(ctx context.Context, appSecret string, req gateway.FeishuMessageSendRequest) (gateway.FeishuMessageSendResult, error)
	ReplyMessage(ctx context.Context, appSecret string, messageID string, req gateway.FeishuMessageReplyRequest) (gateway.FeishuMessageSendResult, error)
	PatchMessage(ctx context.Context, appSecret string, messageID string, content string) error
}

// Transport injects the outbound machinery the adapter must not own: the
// client pool + tenant_access_token cache + per-bot secret resolution. The
// production binding wraps the feishuoutbound worker's clientFor +
// resolveCredentials (wired in PR #3b); PR #3a.2 ships the interface plus a
// fake-backed test only. Resolving per call keeps vault rotation hot.
type Transport interface {
	OutboundSender(ctx context.Context, botID string) (CardSender, gateway.OutboundCredentials, error)
}

// outbound resolves the sender + credentials for this channel's bot.
func (c *Channel) outbound(ctx context.Context) (CardSender, gateway.OutboundCredentials, error) {
	if c.transport == nil {
		return nil, gateway.OutboundCredentials{}, ErrNoTransport
	}
	return c.transport.OutboundSender(ctx, c.appID)
}

// Reply sends a plain-text acknowledgement (command receipts, not streamed).
// It replies to an explicit ReplyToMessageID when set, else replies in the
// thread anchor, else posts to the chat — mirroring the worker's
// thread-vs-chat anchoring.
func (c *Channel) Reply(ctx context.Context, target channel.ReplyTarget, text string) error {
	client, creds, err := c.outbound(ctx)
	if err != nil {
		return err
	}
	content, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return err
	}
	anchor := strings.TrimSpace(target.ReplyToMessageID)
	inThread := false
	if anchor == "" {
		anchor = strings.TrimSpace(target.ExternalThreadID)
		inThread = anchor != ""
	}
	if anchor != "" {
		_, err = client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       msgTypeText,
			Content:       string(content),
			ReplyInThread: inThread,
		})
		return err
	}
	_, err = client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     target.ExternalChatID,
		MsgType:       msgTypeText,
		Content:       string(content),
	})
	return err
}

// Send posts a new interactive card. With a thread anchor it replies
// in-thread; otherwise it sends to the chat. Returns the delivered message
// reference. This is the adapter's single interactive-send path.
func (c *Channel) Send(ctx context.Context, target channel.ReplyTarget, card channel.Card) (gateway.MessageRef, error) {
	client, creds, err := c.outbound(ctx)
	if err != nil {
		return gateway.MessageRef{}, err
	}
	content := string(card.Payload)
	if anchor := strings.TrimSpace(target.ExternalThreadID); anchor != "" {
		res, err := client.ReplyMessage(ctx, creds.AppSecret, anchor, gateway.FeishuMessageReplyRequest{
			MsgType:       msgTypeInteractive,
			Content:       content,
			ReplyInThread: true,
		})
		if err != nil {
			return gateway.MessageRef{}, err
		}
		return gateway.MessageRef{ID: res.MessageID}, nil
	}
	res, err := client.SendMessage(ctx, creds.AppSecret, gateway.FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     target.ExternalChatID,
		MsgType:       msgTypeInteractive,
		Content:       content,
	})
	if err != nil {
		return gateway.MessageRef{}, err
	}
	return gateway.MessageRef{ID: res.MessageID}, nil
}

// Edit PATCHes an already-delivered card in place — the inflight
// "executing" → terminal card transition pins one message_id.
func (c *Channel) Edit(ctx context.Context, _ channel.ReplyTarget, ref gateway.MessageRef, card channel.Card) error {
	client, creds, err := c.outbound(ctx)
	if err != nil {
		return err
	}
	msgID := strings.TrimSpace(ref.ID)
	if msgID == "" {
		return fmt.Errorf("feishu channel: edit requires a message id")
	}
	return client.PatchMessage(ctx, creds.AppSecret, msgID, string(card.Payload))
}
