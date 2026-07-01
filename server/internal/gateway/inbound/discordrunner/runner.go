// Package discordrunner is the Gateway WebSocket wiring (PR #5c) that drives the
// pure Discord adapter (channel/discord) against the neutral inbound pipeline.
//
// It is the Discord twin of slackrunner: open a long-lived Gateway WebSocket
// (discordgo), register MESSAGE_CREATE / INTERACTION_CREATE handlers, hand the
// raw event JSON to the adapter's Normalize / HandleAction, apply the shared
// neutral gates (self-echo, group-without-mention), then route through the same
// router.HandleInbound the Feishu and Slack paths use. The adapter stays a pure
// decoder; all live-socket concerns live here so they never leak into
// channel/discord.
//
// Two Discord-transport facts shape this runner:
//
//   - The Gateway WebSocket is authenticated once at the IDENTIFY handshake (the
//     bot token), so there is no per-event verification — channel/discord.Verify
//     is a pass-through and is not called here.
//   - Unlike Slack's Socket Mode (which acks an envelope), an interaction is
//     answered with a direct InteractionRespond: a rendered card replaces the
//     source message (UpdateMessage), while a silent ack (an empty render, e.g. a
//     bare select pick) defers the update (DeferredMessageUpdate) so the click
//     never spins.
//
// Scope (5c): inbound only, env-gated shared bot. Synchronous command replies
// round-trip via the adapter's Reply transport. Component (button / select /
// modal) deliveries route through the adapter's HandleAction; the rendered card
// is sent back via InteractionRespond. discordgo owns reconnection, so a dropped
// socket resumes without runner involvement. Thread continuation is enabled: a
// Discord thread is itself a channel, so a follow-up in a thread the bot was
// already activated in routes without a fresh @mention (history-backed, mirroring
// the Feishu 话题 path). The MESSAGE_CONTENT gateway intent is
// privileged and must be enabled in the Discord Developer Portal or message
// bodies arrive empty.
package discordrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	discordchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/discord"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// maxSeen bounds the (channel, message id) dedup set. The Gateway can redeliver
// an event after a resume, so repeats are dropped; the set is cleared wholesale
// once it fills rather than tracking per-entry age — a redeliver storm is
// bounded to one window, which is all we need.
const maxSeen = 4096

// gatewayIntents is the privileged-aware intent set the runner identifies with:
// guild + DM message delivery, plus MESSAGE_CONTENT so the bot actually receives
// message bodies (without it content arrives empty for non-mention messages).
// IntentGuilds populates the session's channel/thread State cache so thread
// continuation can classify a channel id without a REST round-trip.
// MESSAGE_CONTENT is a privileged intent and must be toggled on in the Discord
// Developer Portal for the bot.
const gatewayIntents = discordgo.IntentGuilds |
	discordgo.IntentGuildMessages |
	discordgo.IntentDirectMessages |
	discordgo.IntentMessageContent

// Config carries everything the runner needs. BotToken authenticates the Gateway
// WebSocket (the IDENTIFY) and the REST API used to answer interactions. Channel
// is the pure Discord adapter (decode + reply transport); Store is the shared
// router store; GateConfig feeds the visibility rejection cards. Logger is
// optional (defaults to log.Bg()). BotUserID, when empty, is resolved once
// via the /users/@me REST call at Run.
type Config struct {
	BotToken   string
	Channel    *discordchannel.Channel
	Store      router.Store
	GateConfig gateway.GateConfig
	Logger     *slog.Logger
	BotUserID  string
}

// Runner owns the Gateway WebSocket connection and the per-delivery dispatch.
type Runner struct {
	session *discordgo.Session
	ch      *discordchannel.Channel
	store   router.Store
	gateCfg gateway.GateConfig
	log     *slog.Logger

	// botUserID is the bot's own Discord user id (the neutral local id). It is the
	// self-echo signal and the @mention target the group gate matches.
	botUserID string

	// host is unused for Discord routing: router.HandleInbound only threads it into
	// the reply/quoted closures, and the Discord reply bridge ignores it. Carried
	// as the zero value so the shared signature is satisfied.
	host gateway.FeishuRouteAgent

	// respond answers an interaction. Pulled out as a field so tests capture the
	// call without a live REST round-trip; New wires the session's
	// InteractionRespond. The variadic options match discordgo's signature so the
	// method value assigns directly.
	respond func(i *discordgo.Interaction, resp *discordgo.InteractionResponse, opts ...discordgo.RequestOption) error

	// isThread reports whether a channel id refers to a Discord thread (public /
	// private / news thread) rather than a regular channel. A thread's own id is
	// the stable session key, so a message in it carries the thread as its root —
	// grouping every follow-up into one conversation without a re-@mention. Pulled
	// out as a field so tests inject a deterministic classifier; New wires the
	// session-State-backed resolver.
	isThread func(channelID string) bool

	mu   sync.Mutex
	seen map[string]struct{}
}

// New validates config and builds the discordgo session. It does not open the
// connection (Run does), so construction is cheap and side-effect free.
func New(cfg Config) (*Runner, error) {
	if strings.TrimSpace(cfg.BotToken) == "" {
		return nil, errors.New("discord runner: bot token required")
	}
	if cfg.Channel == nil {
		return nil, errors.New("discord runner: channel adapter required")
	}
	if cfg.Store == nil {
		return nil, errors.New("discord runner: store required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Bg()
	}
	// discordgo.New only parses the token shape, never reaching the network, so
	// the error is non-fatal here; an invalid token surfaces at Open / first REST
	// call. The "Bot " prefix is discordgo's auth convention for a bot token.
	session, err := discordgo.New("Bot " + strings.TrimSpace(cfg.BotToken))
	if err != nil {
		return nil, fmt.Errorf("discord runner: build session: %w", err)
	}
	session.Identify.Intents = gatewayIntents
	r := &Runner{
		session:   session,
		ch:        cfg.Channel,
		store:     cfg.Store,
		gateCfg:   cfg.GateConfig,
		log:       logger,
		botUserID: strings.TrimSpace(cfg.BotUserID),
		respond:   session.InteractionRespond,
		seen:      make(map[string]struct{}),
	}
	r.isThread = r.channelIsThread
	return r, nil
}

// channelIsThread classifies a Discord channel id as a thread (public / private
// / news thread) or not, preferring the session's State cache (populated by the
// IntentGuilds intent) and falling back to a REST lookup on a cache miss. A
// lookup failure classifies as not-a-thread so the mention gate stays strict
// rather than admitting a non-mention message on a bad read.
func (r *Runner) channelIsThread(channelID string) bool {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return false
	}
	if r.session.State != nil {
		if ch, err := r.session.State.Channel(channelID); err == nil && ch != nil {
			return ch.IsThread()
		}
	}
	ch, err := r.session.Channel(channelID)
	if err != nil || ch == nil {
		return false
	}
	return ch.IsThread()
}

// Run resolves the bot identity (once, if not pre-set), registers the gateway
// handlers, opens the WebSocket, and blocks until ctx is cancelled. It mirrors
// slackrunner.Runner.Run as the goroutine main.go launches.
func (r *Runner) Run(ctx context.Context) error {
	if r.botUserID == "" {
		me, err := r.session.User("@me", discordgo.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("discord runner: resolve identity (@me): %w", err)
		}
		r.botUserID = strings.TrimSpace(me.ID)
		r.log.Info("discord runner identity resolved", "bot_user_id", r.botUserID, "username", me.Username)
	}
	r.session.AddHandler(r.onMessageCreate(ctx))
	r.session.AddHandler(r.onInteractionCreate(ctx))
	if err := r.session.Open(); err != nil {
		return fmt.Errorf("discord runner: open gateway: %w", err)
	}
	r.log.Info("discord runner connected")
	defer func() {
		if err := r.session.Close(); err != nil {
			r.log.Warn("discord runner close", "error", err)
		}
	}()
	<-ctx.Done()
	return ctx.Err()
}

// onMessageCreate adapts a typed MESSAGE_CREATE into the raw-JSON dispatch core.
// discordgo hands a typed *Message; re-marshaling it yields exactly the wire
// shape channel/discord.Normalize decodes, so the adapter stays the single owner
// of the decode. ctx is the Run context (gateway handlers carry no per-event
// context of their own).
func (r *Runner) onMessageCreate(ctx context.Context) func(*discordgo.Session, *discordgo.MessageCreate) {
	return func(_ *discordgo.Session, m *discordgo.MessageCreate) {
		payload, err := json.Marshal(m.Message)
		if err != nil {
			r.log.Error("discord runner marshal message", "error", err)
			return
		}
		if err := r.handleMessage(ctx, payload); err != nil {
			r.log.Error("discord runner handle message", "error", err)
		}
	}
}

// onInteractionCreate adapts a typed INTERACTION_CREATE into the dispatch core
// and answers the interaction. A rendered ack replaces the source card
// (UpdateMessage); an empty ack (a silent pick or a router-less echo with no
// card) defers the update so the click resolves without mutating the message.
func (r *Runner) onInteractionCreate(ctx context.Context) func(*discordgo.Session, *discordgo.InteractionCreate) {
	return func(_ *discordgo.Session, i *discordgo.InteractionCreate) {
		payload, err := json.Marshal(i.Interaction)
		if err != nil {
			r.log.Error("discord runner marshal interaction", "error", err)
			return
		}
		ack, err := r.handleInteraction(ctx, payload)
		if err != nil {
			r.log.Error("discord runner handle interaction", "error", err)
			return
		}
		if err := r.answer(i.Interaction, ack); err != nil {
			r.log.Error("discord runner answer interaction", "error", err)
		}
	}
}

// handleMessage is the pure dispatch core: decode → dedup → neutral gates →
// route. It takes the raw MESSAGE_CREATE JSON (not the gateway event) so it is
// unit-testable with a captured payload and no live WebSocket.
func (r *Runner) handleMessage(ctx context.Context, payload []byte) error {
	event, err := r.ch.Normalize(payload)
	if err != nil {
		// Non-message deliveries (no author, system messages, ...) come back as
		// errors from Normalize; they are skips, not failures.
		r.log.Debug("discord runner skip message", "reason", err.Error())
		return nil
	}
	if r.duplicate(event.ExternalChatID, event.ExternalMessageID) {
		return nil
	}
	// Drop the bot's own posts before any routing/storage — the echo guard.
	if gateway.IsSelfSender(event, r.botUserID) {
		return nil
	}
	// A Discord thread is itself a channel: a message in it carries the thread as
	// channel_id. Stamping that id into the root slot gives every message in the
	// thread a shared ThreadKey, so the activating @mention and its follow-ups
	// group into one conversation and history-backed continuation can match.
	r.enrichThread(&event)
	// Group/channel messages must @mention the bot, unless the message lands in a
	// thread the bot was already activated in — then it's a 话题续聊 and no fresh
	// @mention is required (mirrors the Feishu path).
	if gateway.ShouldSkipGroupWithoutMention(ctx, discordThreadHist{r.store}, event, r.botUserID) {
		return nil
	}
	outcome, err := router.HandleInbound(ctx, r.store, r.host, event, r.reply, nil, r.gateCfg)
	if err != nil {
		return fmt.Errorf("discord runner: route inbound: %w", err)
	}
	r.log.Info("discord runner inbound handled",
		"chat", event.ExternalChatID,
		"accepted", outcome.Accepted,
		"reason", outcome.Reason)
	return nil
}

// enrichThread stamps a Discord thread channel's own id into the event's thread
// and root slots. It runs before the mention gate so both the activating
// @mention and later follow-ups in the thread share a ThreadKey (the thread id),
// which is what history-backed continuation and conversation grouping key on. It
// is a no-op for DMs, for a message already carrying a thread id, and for a
// regular (non-thread) channel — there each message stays its own root, so a
// plain-channel reply still needs an explicit @mention.
func (r *Runner) enrichThread(event *gateway.InboundEvent) {
	if event == nil || r.isThread == nil {
		return
	}
	if strings.EqualFold(strings.TrimSpace(event.ChatType), "dm") {
		return
	}
	channelID := strings.TrimSpace(event.ExternalChatID)
	if channelID == "" || strings.TrimSpace(event.ExternalRootID) != "" {
		return
	}
	if !r.isThread(channelID) {
		return
	}
	event.ExternalThreadID = channelID
	event.ExternalRootID = channelID
}

// discordThreadHist binds the runner's store to the discord platform so the
// neutral gate's platform-agnostic ThreadHistoryLookup resolves 话题续聊 history
// against discord conversations only.
type discordThreadHist struct{ store router.Store }

func (h discordThreadHist) HasThreadInboundHistory(ctx context.Context, externalChatID, threadKey string) (bool, error) {
	return h.store.HasThreadInboundHistory(ctx, string(channel.PlatformDiscord), externalChatID, threadKey)
}

// handleInteraction is the pure dispatch core for component/modal callbacks: it
// hands the raw interaction JSON to the adapter's HandleAction and returns the
// rendered ack bytes (the card to replace the source message with). Split out
// like handleMessage so it is unit-testable from a captured payload with no live
// socket. A nil/empty ack means "nothing to render back" (a bare select pick, or
// a router-less echo); an error means the payload could not be decoded or routed.
func (r *Runner) handleInteraction(ctx context.Context, payload []byte) ([]byte, error) {
	res, err := r.ch.HandleAction(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("discord runner: route interaction: %w", err)
	}
	r.log.Debug("discord runner interaction routed", "handled", res.Handled)
	return res.Ack, nil
}

// answer sends the interaction response: a rendered card replaces the source
// message (UpdateMessage); an empty ack, or an ack that maps to no data, defers
// the update (DeferredMessageUpdate) — a silent ack that still resolves the
// click. The deMessage→discordgo mapping lives in the adapter
// (discordchannel.RenderInteractionUpdate) so all discordgo translation stays in
// one package.
func (r *Runner) answer(i *discordgo.Interaction, ack []byte) error {
	data, err := discordchannel.RenderInteractionUpdate(ack)
	if err != nil {
		return fmt.Errorf("discord runner: render interaction update: %w", err)
	}
	resp := &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}
	if data != nil {
		resp = &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: data,
		}
	}
	return r.respond(i, resp)
}

// reply is the router.ReplyFunc bridge: it posts a synchronous command
// acknowledgement back into the originating chat/thread via the adapter's Reply
// transport. host is ignored (Discord has no per-agent host route in 5c).
func (r *Runner) reply(ctx context.Context, _ gateway.FeishuRouteAgent, event gateway.InboundEvent, text string) error {
	return r.ch.Reply(ctx, channel.ReplyTarget{
		TenantKey:        event.Sender.TenantKey,
		ExternalChatID:   event.ExternalChatID,
		ExternalThreadID: event.ExternalThreadID,
		ReplyToMessageID: event.ReplyTo,
	}, text)
}

// duplicate reports whether (channelID, messageID) has already been processed in
// this window, recording it when new. An empty message id is never deduped
// (nothing to key on). The set is cleared wholesale at maxSeen to bound memory.
func (r *Runner) duplicate(channelID, messageID string) bool {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return false
	}
	key := strings.TrimSpace(channelID) + ":" + messageID
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
