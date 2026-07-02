package imhistoryapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

const testSecret = "s3cr3t-signing-key"

type fakeStore struct {
	ref store.ConversationIMRef
	err error
}

func (f fakeStore) GetConversationIMRef(_ context.Context, _ string) (store.ConversationIMRef, error) {
	return f.ref, f.err
}

type fakeFetcher struct {
	res    channel.FetchHistoryResult
	err    error
	gotReq channel.FetchHistoryRequest
}

func (f *fakeFetcher) FetchHistory(_ context.Context, req channel.FetchHistoryRequest) (channel.FetchHistoryResult, error) {
	f.gotReq = req
	return f.res, f.err
}

type fakeResolver struct {
	fetcher  channel.HistoryFetcher
	platform channel.Platform
	found    bool
	gotRef   store.ConversationIMRef
}

func (f *fakeResolver) HistoryFetcher(_ context.Context, ref store.ConversationIMRef) (channel.HistoryFetcher, channel.Platform, bool) {
	f.gotRef = ref
	return f.fetcher, f.platform, f.found
}

func newServer(t *testing.T, deps Deps) http.Handler {
	t.Helper()
	if deps.Signer == nil {
		s, err := NewSigner(testSecret)
		if err != nil {
			t.Fatalf("NewSigner: %v", err)
		}
		deps.Signer = s
	}
	r := chi.NewRouter()
	RegisterRoutes(r, deps)
	return r
}

func doGet(h http.Handler, url, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestFetch_HappyPath: a valid token returns the fetcher's page projected to
// the wire shape, and the resolved routing tuple reaches the fetcher.
func TestFetch_HappyPath(t *testing.T) {
	ref := store.ConversationIMRef{Platform: "feishu", ExternalID: "oc_1", ExternalThreadID: "om_root", SourceAppID: "cli_x"}
	ff := &fakeFetcher{res: channel.FetchHistoryResult{
		Messages: []channel.HistoryMessage{
			{ExternalMessageID: "om_a", SenderID: "u1", Text: "hi", CreatedAt: time.Unix(1700000000, 0)},
		},
		NextCursor: "page2",
		Cap:        50,
	}}
	res := &fakeResolver{fetcher: ff, platform: channel.PlatformFeishu, found: true}
	h := newServer(t, Deps{Store: fakeStore{ref: ref}, Resolver: res})

	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-1&limit=10", sgn.Token("conv-1"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got response
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Cap != 50 || got.NextCursor != "page2" || got.Platform != "feishu" {
		t.Fatalf("response meta = %+v", got)
	}
	if len(got.Messages) != 1 || got.Messages[0].ID != "om_a" || got.Messages[0].Text != "hi" {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if got.Messages[0].CreatedAt == "" {
		t.Fatal("CreatedAt must be RFC3339, got empty")
	}
	// Routing tuple + limit reached the fetcher. With no `thread_id` query
	// param the request is the default whole-channel scope; the conversation
	// ref's external_thread_id is a dedupe key, not a history scope.
	if ff.gotReq.ExternalChatID != "oc_1" || ff.gotReq.ExternalThreadID != "" || ff.gotReq.Limit != 10 {
		t.Fatalf("fetcher req = %+v", ff.gotReq)
	}
	if res.gotRef.SourceAppID != "cli_x" {
		t.Fatalf("resolver ref = %+v", res.gotRef)
	}
}

// TestFetch_BadToken: a wrong token is rejected before any store/resolver call.
func TestFetch_BadToken(t *testing.T) {
	ff := &fakeFetcher{}
	h := newServer(t, Deps{Store: fakeStore{}, Resolver: &fakeResolver{fetcher: ff, found: true}})
	rec := doGet(h, "/internal/im/history?conversation_id=conv-1", "deadbeef")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// TestFetch_TokenScopedToConversation: a token for conv-A must not authorize
// conv-B.
func TestFetch_TokenScopedToConversation(t *testing.T) {
	h := newServer(t, Deps{Store: fakeStore{}, Resolver: &fakeResolver{found: true}})
	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-B", sgn.Token("conv-A"))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("cross-conversation token must be 401, got %d", rec.Code)
	}
}

// TestFetch_MissingConversationID: 400 before auth.
func TestFetch_MissingConversationID(t *testing.T) {
	h := newServer(t, Deps{Store: fakeStore{}, Resolver: &fakeResolver{}})
	rec := doGet(h, "/internal/im/history", "whatever")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// TestFetch_UnsupportedPlatform: a resolver miss degrades to an empty 200 page
// (never-fail contract), not an error.
func TestFetch_UnsupportedPlatform(t *testing.T) {
	res := &fakeResolver{found: false}
	h := newServer(t, Deps{Store: fakeStore{ref: store.ConversationIMRef{Platform: "irc"}}, Resolver: res})
	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-1", sgn.Token("conv-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 empty page", rec.Code)
	}
	var got response
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Messages) != 0 {
		t.Fatalf("want empty page, got %+v", got.Messages)
	}
}

// TestFetch_UnknownConversation: 404 when the store can't resolve the id.
func TestFetch_UnknownConversation(t *testing.T) {
	h := newServer(t, Deps{Store: fakeStore{err: store.ErrUnknownConversation}, Resolver: &fakeResolver{found: true}})
	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-x", sgn.Token("conv-x"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// TestFetch_FetcherError: a fetch failure surfaces as 502 (the Gate is what
// guarantees success in production; here we assert the bare handler's mapping).
func TestFetch_FetcherError(t *testing.T) {
	ff := &fakeFetcher{err: errors.New("boom")}
	h := newServer(t, Deps{Store: fakeStore{ref: store.ConversationIMRef{Platform: "feishu"}}, Resolver: &fakeResolver{fetcher: ff, platform: channel.PlatformFeishu, found: true}})
	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-1", sgn.Token("conv-1"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
}

// TestFetch_ThreadIDPassthrough: a `thread_id` query param is forwarded
// verbatim to the platform fetcher as ExternalThreadID. The agent uses it
// to scope a Slack history pull to one thread, a Discord pull to one thread
// channel, or a Teams pull to one chatMessage replies list.
func TestFetch_ThreadIDPassthrough(t *testing.T) {
	ref := store.ConversationIMRef{Platform: "slack", ExternalID: "C123", SourceAppID: "T123"}
	ff := &fakeFetcher{res: channel.FetchHistoryResult{Messages: nil, Cap: 15}}
	res := &fakeResolver{fetcher: ff, platform: channel.PlatformSlack, found: true}
	h := newServer(t, Deps{Store: fakeStore{ref: ref}, Resolver: res})
	sgn, _ := NewSigner(testSecret)
	rec := doGet(h, "/internal/im/history?conversation_id=conv-1&thread_id=1700000000.000200", sgn.Token("conv-1"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ff.gotReq.ExternalThreadID != "1700000000.000200" {
		t.Fatalf("ExternalThreadID = %q, want %q", ff.gotReq.ExternalThreadID, "1700000000.000200")
	}
	if ff.gotReq.ExternalChatID != "C123" || ff.gotReq.SourceAppID != "T123" {
		t.Fatalf("routing tuple lost: %+v", ff.gotReq)
	}
}

// TestSigner_RejectsEmptySecret locks the fail-loud construction.
func TestSigner_RejectsEmptySecret(t *testing.T) {
	if _, err := NewSigner("  "); !errors.Is(err, ErrEmptySecret) {
		t.Fatalf("err = %v, want ErrEmptySecret", err)
	}
}

// TestSigner_RoundTrip: a minted token verifies; a tampered one does not.
func TestSigner_RoundTrip(t *testing.T) {
	s, _ := NewSigner(testSecret)
	tok := s.Token("conv-1")
	if !s.Verify("conv-1", tok) {
		t.Fatal("minted token must verify")
	}
	if s.Verify("conv-1", tok+"00") {
		t.Fatal("tampered token must not verify")
	}
	if s.Verify("conv-2", tok) {
		t.Fatal("token must not verify for a different conversation")
	}
}
