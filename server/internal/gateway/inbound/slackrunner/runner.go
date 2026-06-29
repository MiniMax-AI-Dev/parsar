// Package slackrunner is the Socket Mode wiring (PR #4d) that drives the pure
// Slack adapter (channel/slack) against the neutral inbound pipeline.
//
// It is the Slack twin of inbound.Manager's Feishu websocket loop: open a
// Socket Mode connection, ack each delivery, hand the raw event_callback
// payload to the adapter's Normalize, apply the shared neutral gates
// (self-echo, group-without-mention), then route through the same
// router.HandleInbound the Feishu path uses. The adapter stays a pure decoder;
// all live-socket concerns live here so they never leak into channel/slack.
//
// Scope (N4): inbound only, env-gated shared bot. Synchronous command replies
// (/list, /select, rejection hints) round-trip via the injected Slack reply
// bridge. Agent async answers do NOT return to Slack yet — the inflight
// outbound worker is still Feishu-bound; a neutral outbound worker is a later
// PR. Interactive (button) deliveries route through the adapter's HandleAction;
// the rendered replace_original card is POSTed to the interaction's
// response_url (the Socket Mode ack envelope does NOT update the source message
// for a block_actions click), while the envelope itself is bare-acked.
// Slash-command routing is still deferred. Thread continuation without a fresh
// @mention is also deferred (history lookup is nil here), so group/channel
// messages always require an explicit @mention.
package slackrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	slackgo "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	slackchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/slack"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// maxSeen bounds the (channel, ts) dedup set. Socket Mode redelivers a
// delivery on reconnect, so we drop repeats; the set is cleared wholesale once
// it fills rather than tracking per-entry age — a redeliver storm is bounded
// to one window, which is all we need.
const maxSeen = 4096

// Config carries everything the runner needs. BotToken (xoxb-) authenticates
// Web API + identity; AppToken (xapp-, connections:write) opens the websocket.
// Channel is the pure Slack adapter (decode + reply transport); Store is the
// shared router store; GateConfig feeds the visibility rejection cards. Logger
// is optional (defaults to slog.Default). BotUserID, when empty, is resolved
// once via auth.test at Run.
type Config struct {
	BotToken   string
	AppToken   string
	Channel    *slackchannel.Channel
	Store      router.Store
	GateConfig gateway.GateConfig
	Logger     *slog.Logger
	BotUserID  string
}

// Runner owns the Socket Mode connection and the per-delivery dispatch loop.
type Runner struct {
	api     *slackgo.Client
	sm      *socketmode.Client
	ch      *slackchannel.Channel
	store   router.Store
	gateCfg gateway.GateConfig
	log     *slog.Logger

	// botUserID is the bot's own Slack user id (the neutral local id). It is
	// the self-echo signal and the @mention target the group gate matches.
	botUserID string

	// host is unused for Slack routing: router.HandleInbound only threads it
	// into the reply/quoted closures, and the Slack reply bridge ignores it.
	// Carried as the zero value so the shared signature is satisfied.
	host gateway.FeishuRouteAgent

	// post delivers a rendered interactive reply to Slack's per-interaction
	// response_url. Pulled out as a field so tests can capture the call without
	// a live HTTP round-trip; New wires postToResponseURL.
	post func(ctx context.Context, url string, body []byte) error

	mu   sync.Mutex
	seen map[string]struct{}
}

// New validates config and builds the Socket Mode client. It does not open the
// connection (Run does), so construction is cheap and side-effect free.
func New(cfg Config) (*Runner, error) {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return nil, errors.New("slack runner: bot token (xoxb-) required")
	}
	if strings.TrimSpace(cfg.AppToken) == "" {
		return nil, errors.New("slack runner: app-level token (xapp-) required")
	}
	if cfg.Channel == nil {
		return nil, errors.New("slack runner: channel adapter required")
	}
	if cfg.Store == nil {
		return nil, errors.New("slack runner: store required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	api := slackgo.New(cfg.BotToken, slackgo.OptionAppLevelToken(cfg.AppToken))
	return &Runner{
		api:       api,
		sm:        socketmode.New(api),
		ch:        cfg.Channel,
		store:     cfg.Store,
		gateCfg:   cfg.GateConfig,
		log:       logger,
		botUserID: strings.TrimSpace(cfg.BotUserID),
		post:      postToResponseURL,
		seen:      make(map[string]struct{}),
	}, nil
}

// Run resolves the bot identity (once, if not pre-set), starts the event
// consumer, and blocks on the Socket Mode connection until ctx is cancelled.
// It mirrors inbound.Manager.Run as the goroutine main.go launches.
func (r *Runner) Run(ctx context.Context) error {
	if r.botUserID == "" {
		resp, err := r.api.AuthTestContext(ctx)
		if err != nil {
			return fmt.Errorf("slack runner: auth.test: %w", err)
		}
		r.botUserID = strings.TrimSpace(resp.UserID)
		r.log.Info("slack runner identity resolved",
			"bot_user_id", r.botUserID, "team", resp.Team)
	}
	go r.consume(ctx)
	return r.sm.RunContext(ctx)
}

// consume drains the Socket Mode event channel until ctx is cancelled or the
// channel closes. Dispatch is synchronous per delivery — Slack's own ack
// budget is generous and serial processing keeps the dedup set race-free.
func (r *Runner) consume(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-r.sm.Events:
			if !ok {
				return
			}
			r.dispatch(ctx, evt)
		}
	}
}

// dispatch acks each Slack-originated delivery and routes the Events API ones
// through handleEvent. Lifecycle events (connecting/connected/hello/...) are
// logged; socket errors are surfaced at error level. Interactive and slash
// deliveries are acked-and-deferred for N4.
func (r *Runner) dispatch(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		r.log.Info("slack runner connecting")
	case socketmode.EventTypeConnected:
		r.log.Info("slack runner connected")
	case socketmode.EventTypeHello:
		r.log.Debug("slack runner hello")
	case socketmode.EventTypeDisconnect:
		r.log.Warn("slack runner disconnected; socketmode will reconnect")
	case socketmode.EventTypeConnectionError,
		socketmode.EventTypeInvalidAuth,
		socketmode.EventTypeIncomingError,
		socketmode.EventTypeErrorBadMessage,
		socketmode.EventTypeErrorWriteFailed:
		r.log.Error("slack runner socket error",
			"type", string(evt.Type), "data", fmt.Sprintf("%v", evt.Data))
	case socketmode.EventTypeEventsAPI:
		if evt.Request == nil {
			return
		}
		r.sm.Ack(*evt.Request)
		if err := r.handleEvent(ctx, []byte(evt.Request.Payload)); err != nil {
			r.log.Error("slack runner handle event", "error", err)
		}
	case socketmode.EventTypeInteractive:
		// Button/action callbacks: route through the adapter's HandleAction and
		// POST the rendered replace_original card to the interaction's
		// response_url. The Socket Mode Ack envelope does NOT update the source
		// message for a block_actions click — only a response_url POST (or
		// chat.update) does — so the envelope is bare-acked and the card rides a
		// separate request.
		if evt.Request == nil {
			return
		}
		r.sm.Ack(*evt.Request)
		ack, responseURL, err := r.handleInteractive(ctx, []byte(evt.Request.Payload))
		if err != nil {
			r.log.Error("slack runner handle interactive", "error", err)
			return
		}
		if len(ack) == 0 || responseURL == "" {
			// A silent ack (e.g. a bare dropdown pick) or a payload without a
			// response_url: nothing to render back.
			return
		}
		if err := r.post(ctx, responseURL, ack); err != nil {
			r.log.Error("slack runner post interactive response", "error", err)
		}
	case socketmode.EventTypeSlashCommand:
		if evt.Request != nil {
			r.sm.Ack(*evt.Request)
		}
		r.log.Debug("slack runner slash command ack (routing deferred)")
	default:
		r.log.Debug("slack runner ignored event", "type", string(evt.Type))
	}
}

// handleEvent is the pure dispatch core: decode → dedup → neutral gates →
// route. It takes the raw event_callback payload (not the socket Event) so it
// is unit-testable with a captured webhook JSON and no live websocket.
func (r *Runner) handleEvent(ctx context.Context, payload []byte) error {
	event, err := r.ch.Normalize(payload)
	if err != nil {
		// Unhandled inner events (reaction_added, member_joined, ...) come back
		// as errors from Normalize; they are skips, not failures.
		r.log.Debug("slack runner skip event", "reason", err.Error())
		return nil
	}
	if r.duplicate(event.ExternalChatID, event.ExternalMessageID) {
		return nil
	}
	// Drop the bot's own posts before any routing/storage — the echo guard.
	if gateway.IsSelfSender(event, r.botUserID) {
		return nil
	}
	// Group/channel messages must @mention the bot. hist is nil in N4, so
	// thread continuation without a mention is not yet auto-admitted.
	if gateway.ShouldSkipGroupWithoutMention(ctx, nil, event, r.botUserID) {
		return nil
	}
	outcome, err := router.HandleInbound(ctx, r.store, r.host, event, r.reply, nil, r.gateCfg)
	if err != nil {
		return fmt.Errorf("slack runner: route inbound: %w", err)
	}
	r.log.Info("slack runner inbound handled",
		"chat", event.ExternalChatID,
		"accepted", outcome.Accepted,
		"reason", outcome.Reason)
	return nil
}

// handleInteractive is the pure dispatch core for button/action callbacks: it
// hands the raw block_actions payload to the adapter's HandleAction and returns
// the rendered ack bytes (the response_url-shaped reply) alongside the
// interaction's response_url, so the caller can POST the card back. Split out
// like handleEvent so it is unit-testable from a captured payload with no live
// websocket. A nil/empty ack means "nothing to render back" (e.g. a bare
// dropdown pick); an error means the payload could not be decoded or routed.
func (r *Runner) handleInteractive(ctx context.Context, payload []byte) (ack []byte, responseURL string, err error) {
	res, err := r.ch.HandleAction(ctx, payload)
	if err != nil {
		return nil, "", fmt.Errorf("slack runner: route interactive: %w", err)
	}
	r.log.Debug("slack runner interactive routed", "handled", res.Handled)
	return res.Ack, interactionResponseURL(payload), nil
}

// interactionResponseURL pulls the response_url out of a raw block_actions
// payload. Slack stamps it on every interactive delivery; for a button click
// over Socket Mode it is the only channel that updates the source message, so
// the rendered replace_original card is POSTed there. A malformed payload (or
// one without the field) yields "", which the caller treats as "nothing to
// render back".
func interactionResponseURL(payload []byte) string {
	var probe struct {
		ResponseURL string `json:"response_url"`
	}
	_ = json.Unmarshal(payload, &probe)
	return strings.TrimSpace(probe.ResponseURL)
}

// postToResponseURL delivers a rendered interactive reply to Slack's
// per-interaction response_url. A block-action message update (replace_original)
// is NOT carried by the Socket Mode ack envelope — only a POST to response_url
// (or chat.update) mutates the source message — so the card rides this
// out-of-band request. response_url is a short-lived signed endpoint, so no bot
// token is attached.
func postToResponseURL(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack runner: build response_url request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("slack runner: post response_url: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("slack runner: response_url returned status %d", resp.StatusCode)
	}
	return nil
}

// reply is the router.ReplyFunc bridge: it posts a synchronous command
// acknowledgement back into the originating chat/thread via the adapter's
// Reply transport. host is ignored (Slack has no per-agent host route in N4).
func (r *Runner) reply(ctx context.Context, _ gateway.FeishuRouteAgent, event gateway.InboundEvent, text string) error {
	return r.ch.Reply(ctx, channel.ReplyTarget{
		ExternalChatID:   event.ExternalChatID,
		ExternalThreadID: event.ExternalThreadID,
		ReplyToMessageID: event.ReplyTo,
	}, text)
}

// duplicate reports whether (channelID, ts) has already been processed in this
// window, recording it when new. An empty ts is never deduped (nothing to key
// on). The set is cleared wholesale at maxSeen to bound memory.
func (r *Runner) duplicate(channelID, ts string) bool {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return false
	}
	key := strings.TrimSpace(channelID) + ":" + ts
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.seen[key]; ok {
		return true
	}
	if len(r.seen) >= maxSeen {
		r.seen = make(map[string]struct{}, maxSeen)
	}
	r.seen[key] = struct{}{}
	return false
}
