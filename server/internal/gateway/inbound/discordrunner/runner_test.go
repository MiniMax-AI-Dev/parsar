package discordrunner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	discordchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/discord"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// unusedStore satisfies router.Store via a nil embedded interface: any method
// call panics. The skip-path tests assert routing is short-circuited before
// router.HandleInbound, so the store must never be touched — a panic here is a
// loud test failure.
type unusedStore struct{ router.Store }

// --- fixtures: MESSAGE_CREATE payloads the adapter's Normalize accepts --------

// selfMessage is a channel message authored by the bot's own user id (BOT) — the
// echo the self-sender guard must drop before routing.
const selfMessage = `{
  "id":"m-self","channel_id":"c1","guild_id":"g1",
  "content":"<@BOT> echo","author":{"id":"BOT","bot":true},
  "mentions":[{"id":"BOT"}]
}`

// channelNoMention is a guild message that does not @mention the bot — the group
// gate must drop it (mention-required, thread-continuation deferred).
const channelNoMention = `{
  "id":"m-2","channel_id":"c1","guild_id":"g1",
  "content":"just chatting","author":{"id":"u-7"}
}`

// noAuthor is a payload Normalize rejects (no author) — handleMessage treats the
// decode error as a quiet skip, not a failure.
const noAuthor = `{"id":"m-3","channel_id":"c1"}`

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(Config{
		BotToken: "bot-test",
		Channel:  discordchannel.New(discordchannel.Config{AppID: "A123", BotToken: "bot-test"}),
		Store:    unusedStore{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.botUserID = "BOT" // pre-set so handleMessage skips the @me round-trip
	return r
}

func TestHandleMessage_SkipsSelfEcho(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleMessage(context.Background(), []byte(selfMessage)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
	// Reaching here without a panic from unusedStore proves we never routed.
}

func TestHandleMessage_SkipsChannelWithoutMention(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleMessage(context.Background(), []byte(channelNoMention)); err != nil {
		t.Fatalf("handleMessage: %v", err)
	}
}

func TestHandleMessage_SkipsUndecodableMessage(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleMessage(context.Background(), []byte(noAuthor)); err != nil {
		t.Fatalf("handleMessage on an undecodable message must be a quiet skip, got: %v", err)
	}
}

func TestDuplicate(t *testing.T) {
	r := newTestRunner(t)
	if r.duplicate("c1", "100") {
		t.Fatal("first sighting must not be a duplicate")
	}
	if !r.duplicate("c1", "100") {
		t.Fatal("second sighting of the same (channel, id) must be a duplicate")
	}
	if r.duplicate("c1", "101") {
		t.Fatal("a different id in the same channel is not a duplicate")
	}
	if r.duplicate("c1", "") {
		t.Fatal("an empty message id is never deduped")
	}
}

// TestNew_RequiresTokenAndDeps guards the New() validation surface so a
// misconfigured runner fails fast at construction rather than as an opaque socket
// error later.
func TestNew_RequiresTokenAndDeps(t *testing.T) {
	base := func() Config {
		return Config{
			BotToken: "bot-test",
			Channel:  discordchannel.New(discordchannel.Config{AppID: "A123", BotToken: "bot-test"}),
			Store:    unusedStore{},
		}
	}
	cases := map[string]func(*Config){
		"missing bot token": func(c *Config) { c.BotToken = "" },
		"missing channel":   func(c *Config) { c.Channel = nil },
		"missing store":     func(c *Config) { c.Store = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := base()
			mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatalf("New(%s) = nil error, want a validation error", name)
			}
		})
	}
}

// compile-time guard: the reply bridge matches router.ReplyFunc.
var _ router.ReplyFunc = (*Runner)(nil).reply

// compile-time guard: an empty host is a valid gateway route value.
var _ gateway.FeishuRouteAgent = (&Runner{}).host

// --- interaction (component) routing ---------------------------------------

// fakeActionRouter records the neutral CardAction it received and returns a
// canned ack — the seam the Discord adapter calls once a router is wired.
type fakeActionRouter struct {
	got channel.CardAction
	ack channel.ActionAck
	err error
}

func (f *fakeActionRouter) RouteAction(_ context.Context, a channel.CardAction) (channel.ActionAck, error) {
	f.got = a
	return f.ack, f.err
}

// buttonAllow is a permission_allow button click — the interaction Discord
// delivers as INTERACTION_CREATE (type 3) over the Gateway.
const buttonAllow = `{
  "type":3,"guild_id":"g1","channel_id":"c1",
  "message":{"id":"m-100"},"member":{"user":{"id":"op-1"}},
  "data":{"custom_id":"permission_allow:req-42","component_type":2}
}`

// barePick is an ask_user_choice_pick select change: Discord fires it on every
// select change before Submit. It must be a silent no-op — neither routed nor
// rendered back.
const barePick = `{
  "type":3,"guild_id":"g1","channel_id":"c1","message":{"id":"m-100"},
  "data":{"custom_id":"ask_user_choice_pick:0","values":["prod"]}
}`

func newRoutedRunner(t *testing.T, router *fakeActionRouter) *Runner {
	t.Helper()
	ch := discordchannel.New(
		discordchannel.Config{AppID: "A123", BotToken: "bot-test"},
		discordchannel.WithActionRouter(router),
		discordchannel.WithPickStore(discordchannel.NewMemoryPickStore()),
	)
	r, err := New(Config{BotToken: "bot-test", Channel: ch, Store: unusedStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.botUserID = "BOT"
	return r
}

// TestHandleInteraction_RoutesThroughActionRouter is the wire-in guard: a button
// interaction decodes to a neutral CardAction, reaches the injected router with
// Platform=discord, and the rendered ack comes back for InteractionRespond.
func TestHandleInteraction_RoutesThroughActionRouter(t *testing.T) {
	router := &fakeActionRouter{ack: channel.ActionAck{Result: &channel.ActionResultCard{
		Kind:     channel.CardActionPermissionAllow,
		Title:    "Demo Agent",
		Approved: true,
	}}}
	r := newRoutedRunner(t, router)

	ack, err := r.handleInteraction(context.Background(), []byte(buttonAllow))
	if err != nil {
		t.Fatalf("handleInteraction: %v", err)
	}
	if router.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("router saw kind %q, want permission_allow", router.got.Kind)
	}
	if router.got.Platform != channel.PlatformDiscord {
		t.Errorf("router saw platform %q, want discord", router.got.Platform)
	}
	if router.got.Values["permission_request_id"] != "req-42" {
		t.Errorf("router saw permission_request_id %q, want req-42", router.got.Values["permission_request_id"])
	}
	if len(ack) == 0 {
		t.Fatal("ack empty, want the rendered card to update the source message")
	}
}

// TestHandleInteraction_NoRouterEchoesReceived: a router-less adapter still
// produces a non-empty "received" ack so a click never hangs silently.
func TestHandleInteraction_NoRouterEchoesReceived(t *testing.T) {
	r := newTestRunner(t) // channel built without WithActionRouter
	ack, err := r.handleInteraction(context.Background(), []byte(buttonAllow))
	if err != nil {
		t.Fatalf("handleInteraction: %v", err)
	}
	if len(ack) == 0 {
		t.Fatal("ack empty, want the neutral received echo")
	}
}

// TestHandleInteraction_PickIsSilentNoOp: a bare select pick must neither error
// (it would otherwise classify as unrouted) nor produce an ack to render back.
// The picked value is consumed only at submit time.
func TestHandleInteraction_PickIsSilentNoOp(t *testing.T) {
	router := &fakeActionRouter{err: errors.New("router must not be called for a pick")}
	r := newRoutedRunner(t, router)
	ack, err := r.handleInteraction(context.Background(), []byte(barePick))
	if err != nil {
		t.Fatalf("a pick must be a silent no-op, got err: %v", err)
	}
	if len(ack) != 0 {
		t.Errorf("a pick must produce no ack to render back, got %s", ack)
	}
}

// TestHandleInteraction_RejectsMalformed: a payload that is not a valid
// interaction surfaces as an error so dispatch logs and bare-defers.
func TestHandleInteraction_RejectsMalformed(t *testing.T) {
	r := newTestRunner(t)
	if _, err := r.handleInteraction(context.Background(), []byte("not json")); err == nil {
		t.Fatal("handleInteraction must error on a malformed payload")
	}
}

// TestAnswer_UpdateMessageVsDefer proves answer maps a rendered ack to an
// UpdateMessage response and an empty ack to a DeferredMessageUpdate (a silent
// ack that still resolves the click), capturing the response via the respond seam.
func TestAnswer_UpdateMessageVsDefer(t *testing.T) {
	r := newTestRunner(t)
	var gotType discordgo.InteractionResponseType
	var gotData *discordgo.InteractionResponseData
	r.respond = func(_ *discordgo.Interaction, resp *discordgo.InteractionResponse, _ ...discordgo.RequestOption) error {
		gotType = resp.Type
		gotData = resp.Data
		return nil
	}

	// A rendered card (a content-only deMessage ack) → UpdateMessage with data.
	rendered, err := json.Marshal(map[string]any{"content": "approved"})
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}
	if err := r.answer(&discordgo.Interaction{}, rendered); err != nil {
		t.Fatalf("answer(rendered): %v", err)
	}
	if gotType != discordgo.InteractionResponseUpdateMessage {
		t.Errorf("rendered ack type = %d, want UpdateMessage (%d)", gotType, discordgo.InteractionResponseUpdateMessage)
	}
	if gotData == nil || gotData.Content != "approved" {
		t.Errorf("rendered ack data = %+v, want content=approved", gotData)
	}

	// An empty ack → DeferredMessageUpdate with no data (a silent ack).
	gotData = nil
	if err := r.answer(&discordgo.Interaction{}, nil); err != nil {
		t.Fatalf("answer(empty): %v", err)
	}
	if gotType != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Errorf("empty ack type = %d, want DeferredMessageUpdate (%d)", gotType, discordgo.InteractionResponseDeferredMessageUpdate)
	}
	if gotData != nil {
		t.Errorf("empty ack must carry no data, got %+v", gotData)
	}
}
