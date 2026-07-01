package teamsrunner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	teamschannel "github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel/teams"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/router"
)

// unusedStore satisfies router.Store via a nil embed: any method call panics. The
// skip-path tests assert routing is short-circuited before router.HandleInbound,
// so the store must never be touched — a panic is a loud failure. Only
// HasThreadInboundHistory is real: the mention gate consults it, and a false
// answer keeps the skip-path tests exercising the mention-required branch.
type unusedStore struct{ router.Store }

func (unusedStore) HasThreadInboundHistory(context.Context, string, string, string) (bool, error) {
	return false, nil
}

// selfMessage is a DM authored by the bot's own local id ("28:app-123") — the
// echo the self-sender guard must drop before any routing.
const selfMessage = `{
  "type":"message","id":"m-self","text":"echo",
  "from":{"id":"28:app-123"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-dm","conversationType":"personal"}
}`

// channelNoMention is a team-channel message that does not @mention the bot —
// the group gate must drop it (mention required, no thread history).
const channelNoMention = `{
  "type":"message","id":"m-ch","text":"just chatting",
  "from":{"id":"29:user-a"},
  "recipient":{"id":"28:app-123"},
  "conversation":{"id":"conv-ch","conversationType":"channel"}
}`

// conversationUpdate is a non-message activity — Normalize rejects it and the
// runner treats it as a quiet skip.
const conversationUpdate = `{
  "type":"conversationUpdate","id":"c1",
  "conversation":{"id":"conv-x"}
}`

func newTestRunner(t *testing.T, opts ...teamschannel.Option) *Runner {
	t.Helper()
	r, err := New(Config{
		Channel: teamschannel.New(teamschannel.Config{AppID: "app-123"}, opts...),
		Store:   unusedStore{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func TestHandle_SkipsSelfEcho(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handle(context.Background(), []byte(selfMessage)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	// Reaching here without a panic from unusedStore proves we never routed.
}

func TestHandle_SkipsChannelWithoutMention(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handle(context.Background(), []byte(channelNoMention)); err != nil {
		t.Fatalf("handle: %v", err)
	}
}

func TestHandle_SkipsNonMessage(t *testing.T) {
	r := newTestRunner(t)
	if err := r.handle(context.Background(), []byte(conversationUpdate)); err != nil {
		t.Fatalf("a non-message activity must be a quiet skip, got: %v", err)
	}
}

func TestDuplicate(t *testing.T) {
	r := newTestRunner(t)
	if r.duplicate("conv-1", "a1") {
		t.Fatal("first sighting must not be a duplicate")
	}
	if !r.duplicate("conv-1", "a1") {
		t.Fatal("second sighting of the same (conv, activity) must be a duplicate")
	}
	if r.duplicate("conv-1", "a2") {
		t.Fatal("a different activity id is not a duplicate")
	}
	if r.duplicate("conv-1", "") {
		t.Fatal("an empty activity id is never deduped")
	}
}

func TestNew_RequiresChannelAndStore(t *testing.T) {
	if _, err := New(Config{Store: unusedStore{}}); err == nil {
		t.Error("New with no channel must error")
	}
	if _, err := New(Config{Channel: teamschannel.New(teamschannel.Config{AppID: "app-123"})}); err == nil {
		t.Error("New with no store must error")
	}
}

// fakeActionRouter records the routed action so the card-action fork test can
// assert the neutral decode reached the router with Platform=teams.
type fakeActionRouter struct {
	got channel.CardAction
}

func (f *fakeActionRouter) RouteAction(_ context.Context, a channel.CardAction) (channel.ActionAck, error) {
	f.got = a
	return channel.ActionAck{ToastKind: "info", ToastContent: "ok"}, nil
}

// cardSubmitNoConv is an Action.Submit with NO conversation id: the fork still
// routes it through HandleAction (the assertion target), but the empty
// conversation id makes the runner skip the ack post-back — so the test exercises
// the card-action fork + routing without a live Connector send. The full ack
// post-back is covered by the adapter's outbound Send tests.
const cardSubmitNoConv = `{
  "type":"message","id":"sub-1",
  "from":{"id":"29:user-a","aadObjectId":"aad-alice"},
  "recipient":{"id":"28:app-123"},
  "value":{"action":"permission_allow","permission_request_id":"req-42"}
}`

// TestHandle_ForksCardActionToRouter: a card submit forks to HandleAction and
// reaches the wired router with Platform=teams and the permission routing keys.
func TestHandle_ForksCardActionToRouter(t *testing.T) {
	ar := &fakeActionRouter{}
	r := newTestRunner(t, teamschannel.WithActionRouter(ar))
	if err := r.handle(context.Background(), []byte(cardSubmitNoConv)); err != nil {
		t.Fatalf("handle: %v", err)
	}
	if ar.got.Kind != channel.CardActionPermissionAllow {
		t.Errorf("router saw kind %q, want permission_allow", ar.got.Kind)
	}
	if ar.got.Platform != channel.PlatformTeams {
		t.Errorf("router saw platform %q, want teams", ar.got.Platform)
	}
	if ar.got.Values["permission_request_id"] != "req-42" {
		t.Errorf("permission_request_id = %q, want req-42", ar.got.Values["permission_request_id"])
	}
}

// TestHandler_Returns200OnHandled: with no verifier wired the webhook accepts a
// well-formed POST and returns 200, never a retry-inducing 5xx.
func TestHandler_Returns200OnHandled(t *testing.T) {
	r := newTestRunner(t)
	req := httptest.NewRequest(http.MethodPost, "/api/teams/messages", strings.NewReader(selfMessage))
	rec := httptest.NewRecorder()
	r.Handler()(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// compile-time guard: the reply bridge matches router.ReplyFunc.
var _ router.ReplyFunc = (*Runner)(nil).reply
