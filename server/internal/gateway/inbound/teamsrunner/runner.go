// Package teamsrunner is the Bot Framework webhook wiring that drives the pure
// Teams adapter (channel/teams) against the neutral inbound pipeline.
//
// Unlike Slack (Socket Mode) or Discord (Gateway websocket), Teams delivers
// inbound activities as HTTPS POSTs to a webhook. So the runner is an
// http.Handler, not a Run loop: main.go mounts Handler() on the chi router
// rather than launching a goroutine. Each POST is verified (JWT bearer, via the
// adapter's Verify), the conversation reference is primed (so a synchronous
// reply / card ack can address the regional Connector), then the payload forks:
//
//   - An Adaptive Card Action.Submit (a message activity carrying a `value`)
//     routes through the adapter's HandleAction; the rendered ack card is posted
//     back into the conversation as a new activity.
//   - A plain message activity is Normalize-d, de-duplicated, run through the
//     shared neutral gates (self-echo, group-without-mention with thread-continuation
//     history continuation), then routed via the same router.HandleInbound the
//     Feishu/Slack paths use.
//
// The adapter stays a pure decoder/renderer; all live-webhook concerns (JWT
// verify wiring, dedup, HTTP status codes) live here so they never leak into
// channel/teams. Scope: inbound only, env-gated shared bot — agent async answers
// do not stream back to Teams yet (matching the current Slack/Discord state);
// synchronous command replies and card acks do work via the outbound transport.
package teamsrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	teamschannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/teams"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// maxSeen bounds the (conversation, activity) dedup set. The Bot Framework
// retries a webhook POST on a slow/failed response, so we drop repeats; the set
// is cleared wholesale once it fills rather than tracking per-entry age.
const maxSeen = 4096

// maxBodyBytes caps the webhook body read. A Bot Framework Activity is small
// (a few KB); the cap defends against a malformed oversized POST.
const maxBodyBytes = 1 << 20 // 1 MiB

// Config carries everything the runner needs. Channel is the pure Teams adapter
// (decode + reply/card transport); Store is the shared router store; GateConfig
// feeds the visibility rejection cards; Logger is optional (defaults to log.Bg).
// BotLocalID, when empty, is read off the adapter (the "28:<AppID>" mention
// target) at New.
type Config struct {
	Channel    *teamschannel.Channel
	Store      router.Store
	GateConfig gateway.GateConfig
	Logger     *slog.Logger
	BotLocalID string
}

// Runner owns the webhook handler and the per-delivery dispatch. It holds no
// live connection (the transport is HTTP request/response), so there is no Run
// loop — main.go mounts Handler().
type Runner struct {
	ch      *teamschannel.Channel
	store   router.Store
	gateCfg gateway.GateConfig
	log     *slog.Logger

	// botLocalID is the bot's Teams channel-account id ("28:<AppID>") — the
	// self-echo signal and the @mention target the group gate matches.
	botLocalID string

	// host is unused for Teams routing: router.HandleInbound only threads it
	// into the reply/quoted closures, which the Teams reply bridge ignores.
	host gateway.FeishuRouteAgent

	mu   sync.Mutex
	seen map[string]struct{}
}

// New validates config and builds the runner. It performs no network I/O.
func New(cfg Config) (*Runner, error) {
	if cfg.Channel == nil {
		return nil, errors.New("teams runner: channel adapter required")
	}
	if cfg.Store == nil {
		return nil, errors.New("teams runner: store required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Bg()
	}
	botLocalID := strings.TrimSpace(cfg.BotLocalID)
	if botLocalID == "" {
		botLocalID = cfg.Channel.BotLocalID()
	}
	return &Runner{
		ch:         cfg.Channel,
		store:      cfg.Store,
		gateCfg:    cfg.GateConfig,
		log:        logger,
		botLocalID: botLocalID,
		seen:       make(map[string]struct{}),
	}, nil
}

// Handler returns the webhook http.HandlerFunc main.go mounts (e.g. at
// POST /api/teams/messages). It verifies the inbound token, primes the
// conversation reference, and dispatches. It always replies 200 on a handled
// (or intentionally skipped) delivery so the Bot Framework does not retry; a
// verification failure is a 401 and a decode failure a 400.
func (r *Runner) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		verified, _, err := r.ch.Verify(req, body)
		if err != nil {
			// Inbound JWT rejected: the Connector (or an impostor) failed auth.
			r.log.Warn("teams runner verify failed", "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if err := r.handle(req.Context(), verified); err != nil {
			r.log.Error("teams runner handle", "error", err)
			// Still 200: a handling error (route failure) is ours, not the
			// Connector's — retrying the same payload will not help, and a 5xx
			// makes the Bot Framework hammer the endpoint.
		}
		w.WriteHeader(http.StatusOK)
	}
}

// handle is the pure dispatch core (independent of the HTTP request), so it is
// unit-testable from a captured verified payload. It primes the conversation
// ref, forks card-action vs message, and routes.
func (r *Runner) handle(ctx context.Context, verified []byte) error {
	// Prime the conversation reference for EVERY inbound (even ones Normalize
	// rejects) so an outbound reply/ack in this request can address the
	// regional Connector.
	convID := r.ch.RememberInbound(verified)

	// Fork: an Adaptive Card Action.Submit rides a message activity carrying a
	// `value`. Route it through HandleAction and post the ack card back.
	if teamschannel.IsCardAction(verified) {
		return r.handleAction(ctx, convID, verified)
	}
	return r.handleMessage(ctx, verified)
}

// handleAction routes a decoded card submit through the adapter and posts the
// rendered ack back into the conversation as a new activity. A nil ack (no
// router wired returns a "received" reply; a genuinely empty ack) is skipped.
func (r *Runner) handleAction(ctx context.Context, convID string, verified []byte) error {
	res, err := r.ch.HandleAction(ctx, verified)
	if err != nil {
		return fmt.Errorf("teams runner: route card action: %w", err)
	}
	r.log.Debug("teams runner card action routed", "handled", res.Handled)
	if len(res.Ack) == 0 || strings.TrimSpace(convID) == "" {
		return nil
	}
	// Post the ack card as a new activity in the conversation. The ack bytes are
	// a teamsWireMessage {text, card} the adapter's Send path understands.
	if _, err := r.ch.Send(ctx, channel.ReplyTarget{ExternalChatID: convID}, channel.Card{Payload: res.Ack}); err != nil {
		return fmt.Errorf("teams runner: post card ack: %w", err)
	}
	return nil
}

// teamsThreadHist binds the runner's store to the teams platform so the neutral
// gate's platform-agnostic ThreadHistoryLookup resolves thread-continuation history against
// teams conversations only.
type teamsThreadHist struct{ store router.Store }

func (h teamsThreadHist) HasThreadInboundHistory(ctx context.Context, externalChatID, threadKey string) (bool, error) {
	return h.store.HasThreadInboundHistory(ctx, string(channel.PlatformTeams), externalChatID, threadKey)
}

// handleMessage is the message-activity dispatch: decode → dedup → neutral gates
// → route. Non-message activities (invoke / conversationUpdate) come back as
// errors from Normalize and are skips, not failures.
func (r *Runner) handleMessage(ctx context.Context, verified []byte) error {
	event, err := r.ch.Normalize(verified)
	if err != nil {
		r.log.Debug("teams runner skip activity", "reason", err.Error())
		return nil
	}
	if r.duplicate(event.ExternalChatID, event.ExternalMessageID) {
		return nil
	}
	// Drop the bot's own posts before any routing/storage — the echo guard.
	if gateway.IsSelfSender(event, r.botLocalID) {
		return nil
	}
	// Group/channel messages must @mention the bot, unless the message lands in
	// a thread the bot was already activated in — then it's a thread continuation and no
	// fresh @mention is required (mirrors the Feishu/Slack path).
	if gateway.ShouldSkipGroupWithoutMention(ctx, teamsThreadHist{r.store}, event, r.botLocalID) {
		return nil
	}
	outcome, err := router.HandleInbound(ctx, r.store, r.host, event, r.reply, nil, r.gateCfg)
	if err != nil {
		return fmt.Errorf("teams runner: route inbound: %w", err)
	}
	r.log.Info("teams runner inbound handled",
		"chat", event.ExternalChatID,
		"accepted", outcome.Accepted,
		"reason", outcome.Reason)
	return nil
}

// reply is the router.ReplyFunc bridge: it posts a synchronous command
// acknowledgement back into the originating conversation via the adapter's Reply
// transport. host is ignored (Teams has no per-agent host route yet).
func (r *Runner) reply(ctx context.Context, _ gateway.FeishuRouteAgent, event gateway.InboundEvent, text string) error {
	return r.ch.Reply(ctx, channel.ReplyTarget{
		ExternalChatID:   event.ExternalChatID,
		ExternalThreadID: event.ExternalThreadID,
		ReplyToMessageID: event.ReplyTo,
	}, text)
}

// duplicate reports whether (conversationID, activityID) has already been
// processed in this window, recording it when new. An empty activity id is never
// deduped. The set is cleared wholesale at maxSeen to bound memory.
func (r *Runner) duplicate(conversationID, activityID string) bool {
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return false
	}
	key := strings.TrimSpace(conversationID) + ":" + activityID
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
