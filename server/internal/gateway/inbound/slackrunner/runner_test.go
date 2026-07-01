package slackrunner

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	slackchannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/slack"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// unusedStore satisfies router.Store via a nil embedded interface: any method
// call panics. The skip-path tests assert routing is short-circuited before
// router.HandleInbound, so the store must never be touched — a panic here is a
// loud test failure. HasThreadInboundHistory is the one real method: the mention
// gate consults it on every group message, and a no-history (false) answer keeps
// the skip-path tests exercising the mention-required branch.
type unusedStore struct{ router.Store }

func (unusedStore) HasThreadInboundHistory(context.Context, string, string, string) (bool, error) {
	return false, nil
}

// --- fixtures: event_callback envelopes the adapter's Normalize accepts ------

// selfMessage is a DM authored by the bot's own user id (UBOT) — the echo the
// self-sender guard must drop before routing.
const selfMessage = `{
  "type":"event_callback","team_id":"T1","api_app_id":"A123",
  "event":{"type":"message","user":"UBOT","text":"echo","ts":"1700000010.000100",
    "channel":"D9","channel_type":"im","event_ts":"1700000010.000100"}
}`

// channelNoMention is a public-channel message that does not @mention the bot —
// the group gate must drop it (mention-required, thread-continuation deferred).
const channelNoMention = `{
  "type":"event_callback","team_id":"T1","api_app_id":"A123",
  "event":{"type":"message","user":"U7","text":"just chatting","ts":"1700000011.000200",
    "channel":"C5","channel_type":"channel","event_ts":"1700000011.000200"}
}`

// reactionEnvelope wraps reaction_added, an inner event the adapter does not
// handle — Normalize errors and handleEvent treats it as a quiet skip.
const reactionEnvelope = `{
  "type":"event_callback","team_id":"T1","api_app_id":"A123",
  "event":{"type":"reaction_added","user":"U8","reaction":"thumbsup",
    "item":{"type":"message","channel":"C1","ts":"1700000000.000100"}}
}`

// channelThreadReply is a public-channel message with no @mention that lands in
// an existing thread (thread_ts set) — the reply the history-backed continuation
// gate must admit once the bot was activated in that thread.
const channelThreadReply = `{
  "type":"event_callback","team_id":"T1","api_app_id":"A123",
  "event":{"type":"message","user":"U7","text":"more please","ts":"1700000012.000300",
    "thread_ts":"1700000009.000000","channel":"C5","channel_type":"channel","event_ts":"1700000012.000300"}
}`

func newTestRunner(t *testing.T) *Runner {
	t.Helper()
	r, err := New(Config{
		BotToken: "xoxb-test",
		AppToken: "xapp-test",
		Channel:  slackchannel.New(slackchannel.Config{AppID: "A123", BotToken: "xoxb-test", AppToken: "xapp-test"}),
		Store:    unusedStore{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.botUserID = "UBOT" // pre-set so handleEvent skips the auth.test round-trip
	return r
}

func TestHandleEvent_SkipsSelfEcho(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleEvent(context.Background(), []byte(selfMessage)); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
	// Reaching here without a panic from unusedStore proves we never routed.
}

func TestHandleEvent_SkipsChannelWithoutMention(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleEvent(context.Background(), []byte(channelNoMention)); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
}

func TestHandleEvent_SkipsUnsupportedInnerEvent(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handleEvent(context.Background(), []byte(reactionEnvelope)); err != nil {
		t.Fatalf("handleEvent on unsupported event must be a quiet skip, got: %v", err)
	}
}

func TestDuplicate(t *testing.T) {
	r := newTestRunner(t)
	if r.duplicate("C1", "100.1") {
		t.Fatal("first sighting must not be a duplicate")
	}
	if !r.duplicate("C1", "100.1") {
		t.Fatal("second sighting of the same (channel, ts) must be a duplicate")
	}
	if r.duplicate("C1", "100.2") {
		t.Fatal("a different ts in the same channel is not a duplicate")
	}
	if r.duplicate("C1", "") {
		t.Fatal("an empty ts is never deduped")
	}
}

// Guards the New() validation surface so a misconfigured runner fails fast at
// construction rather than as an opaque socket error later.
func TestNew_RequiresTokensAndDeps(t *testing.T) {
	base := func() Config {
		return Config{
			BotToken: "xoxb-test",
			AppToken: "xapp-test",
			Channel:  slackchannel.New(slackchannel.Config{AppID: "A123", BotToken: "xoxb-test"}),
			Store:    unusedStore{},
		}
	}
	cases := map[string]func(*Config){
		"missing bot token": func(c *Config) { c.BotToken = "" },
		"missing app token": func(c *Config) { c.AppToken = "" },
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

// --- N10: interactive (button) routing -------------------------------------

// fakeActionRouter records the neutral CardAction it received and returns a
// canned ack — the seam the Slack adapter calls once a router is wired.
type fakeActionRouter struct {
	got channel.CardAction
	ack channel.ActionAck
	err error
}

func (f *fakeActionRouter) RouteAction(_ context.Context, a channel.CardAction) (channel.ActionAck, error) {
	f.got = a
	return f.ack, f.err
}

// blockActionsAllow is a permission_allow button click — the payload Slack
// delivers as an EventTypeInteractive over Socket Mode.
const blockActionsAllow = `{
  "type":"block_actions","api_app_id":"A123",
  "user":{"id":"U1"},"channel":{"id":"C1"},"message":{"ts":"1700000000.000200"},
  "actions":[{"action_id":"permission_allow","block_id":"b1","type":"button","value":"req-42"}]
}`

// TestHandleInteractive_RoutesThroughActionRouter is the N10 wire-in guard: a
// block_actions delivery decodes to a neutral CardAction, reaches the injected
// router with Platform=slack, and the rendered ack comes back for the Socket
// Mode envelope to inline.
func TestHandleInteractive_RoutesThroughActionRouter(t *testing.T) {
	router := &fakeActionRouter{ack: channel.ActionAck{Result: &channel.ActionResultCard{
		Kind:     channel.CardActionPermissionAllow,
		Title:    "Demo Agent",
		Approved: true,
	}}}
	ch := slackchannel.New(
		slackchannel.Config{AppID: "A123", BotToken: "xoxb-test", AppToken: "xapp-test"},
		slackchannel.WithActionRouter(router),
	)
	r, err := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test", Channel: ch, Store: unusedStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ack, _, err := r.handleInteractive(context.Background(), []byte(blockActionsAllow))
	if err != nil {
		t.Fatalf("handleInteractive: %v", err)
	}
	if router.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("router saw kind %q, want permission_allow", router.got.Kind)
	}
	if router.got.Platform != channel.PlatformSlack {
		t.Errorf("router saw platform %q, want slack", router.got.Platform)
	}
	if router.got.Values["permission_request_id"] != "req-42" {
		t.Errorf("router saw permission_request_id %q, want req-42", router.got.Values["permission_request_id"])
	}
	if len(ack) == 0 {
		t.Fatal("ack empty, want the rendered reply to inline into the envelope")
	}
	var resp map[string]any
	if err := json.Unmarshal(ack, &resp); err != nil {
		t.Fatalf("ack not JSON: %v", err)
	}
	if resp["replace_original"] != true {
		t.Errorf("a neutral Result must render as replace_original; got %v", resp)
	}
}

// TestHandleInteractive_NoRouterEchoesReceived: a router-less adapter still
// produces a non-empty "received" ack so a click never hangs silently.
func TestHandleInteractive_NoRouterEchoesReceived(t *testing.T) {
	r := newTestRunner(t) // channel built without WithActionRouter
	ack, _, err := r.handleInteractive(context.Background(), []byte(blockActionsAllow))
	if err != nil {
		t.Fatalf("handleInteractive: %v", err)
	}
	if len(ack) == 0 {
		t.Fatal("ack empty, want the neutral received echo")
	}
}

// TestHandleInteractive_RejectsMalformed: a payload that is not a valid
// interaction surfaces as an error so dispatch falls back to a bare ack.
func TestHandleInteractive_RejectsMalformed(t *testing.T) {
	r := newTestRunner(t)
	if _, _, err := r.handleInteractive(context.Background(), []byte("not json")); err == nil {
		t.Fatal("handleInteractive must error on a malformed payload")
	}
}

// blockActionsAllowWithURL is the permission_allow click carrying the
// response_url Slack stamps on every interactive delivery — the endpoint the
// rendered replace_original card must be POSTed to.
const blockActionsAllowWithURL = `{
  "type":"block_actions","api_app_id":"A123",
  "user":{"id":"U1"},"channel":{"id":"C1"},"message":{"ts":"1700000000.000200"},
  "response_url":"https://hooks.slack.test/actions/T1/123/abc",
  "actions":[{"action_id":"permission_allow","block_id":"b1","type":"button","value":"req-42"}]
}`

// blockActionsPick is a bare dropdown selection (ask_user_choice_pick): Slack
// fires it on every static_select change, before the user clicks Submit. It
// must be a silent no-op — neither routed nor rendered back.
const blockActionsPick = `{
  "type":"block_actions","api_app_id":"A123",
  "user":{"id":"U1"},"channel":{"id":"C1"},"message":{"ts":"1.2"},
  "response_url":"https://hooks.slack.test/actions/T1/123/abc",
  "actions":[{"action_id":"ask_user_choice_pick","block_id":"choice_0","type":"static_select","selected_option":{"value":"opt-2"}}]
}`

// TestHandleInteractive_SurfacesResponseURL proves handleInteractive returns
// the interaction's response_url so dispatch can POST the rendered card there
// (the Socket Mode ack body does not update a block_actions message).
func TestHandleInteractive_SurfacesResponseURL(t *testing.T) {
	router := &fakeActionRouter{ack: channel.ActionAck{Result: &channel.ActionResultCard{
		Kind: channel.CardActionPermissionAllow, Title: "Demo Agent", Approved: true,
	}}}
	ch := slackchannel.New(
		slackchannel.Config{AppID: "A123", BotToken: "xoxb-test", AppToken: "xapp-test"},
		slackchannel.WithActionRouter(router),
	)
	r, err := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test", Channel: ch, Store: unusedStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ack, responseURL, err := r.handleInteractive(context.Background(), []byte(blockActionsAllowWithURL))
	if err != nil {
		t.Fatalf("handleInteractive: %v", err)
	}
	if responseURL != "https://hooks.slack.test/actions/T1/123/abc" {
		t.Errorf("responseURL = %q, want the payload's response_url", responseURL)
	}
	if len(ack) == 0 {
		t.Fatal("ack empty, want the rendered card to POST back")
	}
}

// TestHandleInteractive_PickIsSilentNoOp guards Fix B: a bare dropdown pick
// must neither error (it would otherwise classify as unrouted) nor produce an
// ack to render back. The picked value is consumed only at submit time.
func TestHandleInteractive_PickIsSilentNoOp(t *testing.T) {
	router := &fakeActionRouter{err: errors.New("router must not be called for a pick")}
	ch := slackchannel.New(
		slackchannel.Config{AppID: "A123", BotToken: "xoxb-test", AppToken: "xapp-test"},
		slackchannel.WithActionRouter(router),
	)
	r, err := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test", Channel: ch, Store: unusedStore{}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ack, _, err := r.handleInteractive(context.Background(), []byte(blockActionsPick))
	if err != nil {
		t.Fatalf("a pick must be a silent no-op, got err: %v", err)
	}
	if len(ack) != 0 {
		t.Errorf("a pick must produce no ack to render back, got %s", ack)
	}
}

// TestInteractionResponseURL covers the response_url projection: present,
// absent, and malformed payloads.
func TestInteractionResponseURL(t *testing.T) {
	if got := interactionResponseURL([]byte(blockActionsAllowWithURL)); got != "https://hooks.slack.test/actions/T1/123/abc" {
		t.Errorf("present: got %q", got)
	}
	if got := interactionResponseURL([]byte(blockActionsAllow)); got != "" {
		t.Errorf("absent: got %q, want empty", got)
	}
	if got := interactionResponseURL([]byte("not json")); got != "" {
		t.Errorf("malformed: got %q, want empty", got)
	}
}

// TestPostToResponseURL_PostsJSONBody asserts the rendered card is delivered as
// a JSON POST to the response_url and a non-2xx status surfaces as an error.
func TestPostToResponseURL_PostsJSONBody(t *testing.T) {
	var gotBody []byte
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotContentType = req.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(req.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	body := []byte(`{"replace_original":true,"text":"ok"}`)
	if err := postToResponseURL(context.Background(), srv.URL, body); err != nil {
		t.Fatalf("postToResponseURL: %v", err)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", gotContentType)
	}
	if string(gotBody) != string(body) {
		t.Errorf("posted body = %s, want %s", gotBody, body)
	}

	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer fail.Close()
	if err := postToResponseURL(context.Background(), fail.URL, body); err == nil {
		t.Fatal("a non-2xx response_url status must surface as an error")
	}
}

// --- thread continuation -----------------------------------------------------

// errRoutingReached is the sentinel a threadHistoryStore returns from the first
// routing lookup, so a test can prove the mention gate admitted a non-@ message
// (routing was entered) without standing up a full routing store.
var errRoutingReached = errors.New("routing reached")

// threadHistoryStore admits a non-@ message past the mention gate by reporting
// thread history, then halts HandleInbound at its first store lookup
// (FindUserIDByPlatformSubject) with errRoutingReached — network-free proof that
// the gate passed. Every other router.Store method panics via the nil embed.
type threadHistoryStore struct {
	router.Store
	hasHistory bool
	sawLookup  bool
}

func (s *threadHistoryStore) HasThreadInboundHistory(_ context.Context, _, _, _ string) (bool, error) {
	return s.hasHistory, nil
}

func (s *threadHistoryStore) FindUserIDByPlatformSubject(_ context.Context, _, _ string) (string, error) {
	s.sawLookup = true
	return "", errRoutingReached
}

func newRunnerWithStore(t *testing.T, st router.Store) *Runner {
	t.Helper()
	r, err := New(Config{
		BotToken: "xoxb-test",
		AppToken: "xapp-test",
		Channel:  slackchannel.New(slackchannel.Config{AppID: "A123", BotToken: "xoxb-test", AppToken: "xapp-test"}),
		Store:    st,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r.botUserID = "UBOT"
	return r
}

// TestHandleEvent_ContinuesActivatedThread: a non-@ reply in a thread the bot was
// already activated in (history present) skips the mention gate and enters
// routing — aligning Slack with the Feishu 话题续聊 rule.
func TestHandleEvent_ContinuesActivatedThread(t *testing.T) {
	st := &threadHistoryStore{hasHistory: true}
	r := newRunnerWithStore(t, st)
	err := r.handleEvent(context.Background(), []byte(channelThreadReply))
	if !errors.Is(err, errRoutingReached) {
		t.Fatalf("a non-@ reply in an activated thread must reach routing, got err=%v", err)
	}
	if !st.sawLookup {
		t.Fatal("expected routing to be entered (FindUserIDByPlatformSubject called)")
	}
}

// TestHandleEvent_SkipsThreadWithoutHistory: the same thread reply, but with no
// prior activation, still requires an @mention — the gate drops it before
// routing.
func TestHandleEvent_SkipsThreadWithoutHistory(t *testing.T) {
	st := &threadHistoryStore{hasHistory: false}
	r := newRunnerWithStore(t, st)
	if err := r.handleEvent(context.Background(), []byte(channelThreadReply)); err != nil {
		t.Fatalf("handleEvent: %v", err)
	}
	if st.sawLookup {
		t.Fatal("a thread with no prior history must not route a non-@ reply")
	}
}
