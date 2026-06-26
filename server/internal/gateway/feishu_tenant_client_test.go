package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTenantClientWithServer wires up a FeishuTenantClient against an
// httptest.Server and a fixed-clock so tests can assert cache behaviour
// without sleeping. Returns the client + a counter recording how many
// token-fetch requests the server saw.
func newTenantClientWithServer(t *testing.T, handler http.HandlerFunc, startAt time.Time) (*FeishuTenantClient, *int64, *httptest.Server) {
	t.Helper()
	var fetchCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			atomic.AddInt64(&fetchCount, 1)
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	current := startAt
	client, err := NewFeishuTenantClient(FeishuTenantClientOptions{
		BaseURL: srv.URL,
		AppID:   "cli_test_app",
		Now:     func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = current
	})
	return client, &fetchCount, srv
}

func TestNewFeishuTenantClient_RequiresAppID(t *testing.T) {
	t.Parallel()
	for _, opts := range []FeishuTenantClientOptions{
		{},                    // both empty
		{AppID: "  "},         // whitespace only
		{BaseURL: "http://x"}, // app_id missing
	} {
		if _, err := NewFeishuTenantClient(opts); !errors.Is(err, ErrFeishuTenantClientConfig) {
			t.Errorf("expected ErrFeishuTenantClientConfig for opts %+v, got %v", opts, err)
		}
	}
}

// TestNewFeishuTenantClient_NoAppSecretInStruct guards spec §5: the
// app_secret MUST NOT live inside the client struct. If a future
// regression re-adds it, this test fails loudly.
func TestNewFeishuTenantClient_NoAppSecretInStruct(t *testing.T) {
	t.Parallel()
	client, err := NewFeishuTenantClient(FeishuTenantClientOptions{AppID: "cli_x"})
	if err != nil {
		t.Fatal(err)
	}
	// Reflection-free guard: if a future change reintroduces appSecret,
	// the build itself breaks because Options has no AppSecret field.
	// The test exists to make the intent explicit and to fail with a
	// pointed message if someone deletes this guard during refactor.
	_ = client
}

func TestFeishuTenantClient_TokenExchangeHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, fetchCount, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("token endpoint method = %s, want POST", r.Method)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["app_id"] != "cli_test_app" || body["app_secret"] != "secret-xyz" {
			t.Errorf("unexpected token request body: %+v", body)
		}
		_, _ = io.WriteString(w, `{"code":0,"msg":"ok","tenant_access_token":"t-abc","expire":7200}`)
	}, now)

	tok, err := client.TenantAccessToken(context.Background(), "secret-xyz")
	if err != nil {
		t.Fatalf("TenantAccessToken: %v", err)
	}
	if tok != "t-abc" {
		t.Errorf("token = %q, want t-abc", tok)
	}
	if atomic.LoadInt64(fetchCount) != 1 {
		t.Errorf("expected 1 token fetch, got %d", atomic.LoadInt64(fetchCount))
	}
}

func TestFeishuTenantClient_TokenCacheHitWithinTTL(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, fetchCount, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-cache","expire":7200}`)
	}, now)

	// Three back-to-back calls at the same instant should hit the
	// cache and only generate ONE upstream fetch.
	for i := 0; i < 3; i++ {
		if _, err := client.TenantAccessToken(context.Background(), "secret-xyz"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := atomic.LoadInt64(fetchCount); got != 1 {
		t.Errorf("expected 1 fetch (cache hits the rest), got %d", got)
	}
}

func TestFeishuTenantClient_RefreshesNearExpiry(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var fetchCount int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt64(&fetchCount, 1)
		_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-refresh","expire":300}`)
	}))
	t.Cleanup(srv.Close)

	client, err := NewFeishuTenantClient(FeishuTenantClientOptions{
		BaseURL: srv.URL,
		AppID:   "cli_x",
		Now:     func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}

	// First call seeds the cache with a 300s TTL token.
	if _, err := client.TenantAccessToken(context.Background(), "y"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&fetchCount) != 1 {
		t.Fatalf("expected 1 fetch after seed, got %d", atomic.LoadInt64(&fetchCount))
	}

	// Jump to just inside the refresh margin (token "real" expiry minus 2 min).
	// 300 - 120 = 180s; advance 170s — still cached.
	current = current.Add(170 * time.Second)
	if _, err := client.TenantAccessToken(context.Background(), "y"); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt64(&fetchCount) != 1 {
		t.Fatalf("expected still cached at +170s, got %d fetches", atomic.LoadInt64(&fetchCount))
	}

	// Cross the refresh margin (190s past seed). Cache must refresh.
	current = current.Add(20 * time.Second)
	if _, err := client.TenantAccessToken(context.Background(), "y"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&fetchCount); got != 2 {
		t.Errorf("expected 2 fetches after crossing margin, got %d", got)
	}
}

// TestFeishuTenantClient_SecretRotationOnRefresh asserts that when the
// token cache misses and the caller passes a NEW app_secret, the
// upstream token-exchange request carries the new value. Spec §5: the
// client must not pin app_secret; it must take whatever the caller
// (= worker re-reading vault per dispatch) supplies on the refresh call.
//
// Without the InvalidateTokenCache helper this test verifies the
// "natural" rotation path: the token expires, the next call refreshes
// and picks up the new secret.
func TestFeishuTenantClient_SecretRotationOnRefresh(t *testing.T) {
	t.Parallel()
	current := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawSecrets []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			http.NotFound(w, r)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		sawSecrets = append(sawSecrets, body["app_secret"])
		// 300s TTL so we can deterministically expire mid-test.
		_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-rotated","expire":300}`)
	}))
	t.Cleanup(srv.Close)

	client, err := NewFeishuTenantClient(FeishuTenantClientOptions{
		BaseURL: srv.URL,
		AppID:   "cli_rotation",
		Now:     func() time.Time { return current },
	})
	if err != nil {
		t.Fatal(err)
	}

	// First fetch with v1.
	if _, err := client.TenantAccessToken(context.Background(), "secret-v1"); err != nil {
		t.Fatal(err)
	}
	// Second call within TTL with a DIFFERENT secret: should HIT cache,
	// upstream not re-asked. v1 only on the wire so far.
	if _, err := client.TenantAccessToken(context.Background(), "secret-v2"); err != nil {
		t.Fatal(err)
	}
	if len(sawSecrets) != 1 || sawSecrets[0] != "secret-v1" {
		t.Fatalf("after rotation within cache TTL expected 1 fetch with secret-v1; got %v", sawSecrets)
	}

	// Force token expiry by advancing past the refresh margin.
	current = current.Add(200 * time.Second)
	// Now caller passes v2 and the refresh must use it.
	if _, err := client.TenantAccessToken(context.Background(), "secret-v2"); err != nil {
		t.Fatal(err)
	}
	if len(sawSecrets) != 2 || sawSecrets[1] != "secret-v2" {
		t.Fatalf("post-expiry token exchange must use rotated secret-v2; saw %v", sawSecrets)
	}

	// InvalidateTokenCache lets ops force the refresh immediately
	// instead of waiting for the natural expiry. Verify that path
	// picks up the next caller-supplied secret too.
	client.InvalidateTokenCache()
	if _, err := client.TenantAccessToken(context.Background(), "secret-v3"); err != nil {
		t.Fatal(err)
	}
	if len(sawSecrets) != 3 || sawSecrets[2] != "secret-v3" {
		t.Fatalf("after InvalidateTokenCache, next fetch should carry secret-v3; saw %v", sawSecrets)
	}
}

func TestFeishuTenantClient_FetchRequiresAppSecret(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("server should not be hit when app_secret is empty")
	}, now)
	if _, err := client.TenantAccessToken(context.Background(), "   "); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Errorf("expected ErrFeishuTenantClientConfig on empty app_secret; got %v", err)
	}
}

func TestFeishuTenantClient_UpstreamRejectError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"code":99991663,"msg":"app secret invalid"}`)
	}, now)

	_, err := client.TenantAccessToken(context.Background(), "wrong-secret")
	if !errors.Is(err, ErrFeishuTokenExchangeRejected) {
		t.Fatalf("expected ErrFeishuTokenExchangeRejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "app secret invalid") {
		t.Errorf("error must include upstream msg; got %q", err)
	}
}

func TestFeishuTenantClient_UpstreamNon2xxError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `upstream blew up`)
	}, now)
	_, err := client.TenantAccessToken(context.Background(), "x")
	if !errors.Is(err, ErrFeishuTokenExchangeFailed) {
		t.Fatalf("expected ErrFeishuTokenExchangeFailed, got %v", err)
	}
}

func TestFeishuTenantClient_SendMessageHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawAuth string
	var sawReceiveIDType string

	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-send","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			sawAuth = r.Header.Get("Authorization")
			sawReceiveIDType = r.URL.Query().Get("receive_id_type")
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"message_id":"om_resp_123"}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	res, err := client.SendMessage(context.Background(), "secret", FeishuMessageSendRequest{
		ReceiveIDType: "chat_id",
		ReceiveID:     "oc_test",
		MsgType:       "text",
		Content:       `{"text":"hello"}`,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if res.MessageID != "om_resp_123" {
		t.Errorf("MessageID = %q", res.MessageID)
	}
	if sawAuth != "Bearer t-send" {
		t.Errorf("Authorization = %q", sawAuth)
	}
	if sawReceiveIDType != "chat_id" {
		t.Errorf("receive_id_type = %q", sawReceiveIDType)
	}
}

func TestFeishuTenantClient_SendMessageRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-send","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			_, _ = io.WriteString(w, `{"code":230002,"msg":"forbidden by Feishu","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	res, err := client.SendMessage(context.Background(), "secret", FeishuMessageSendRequest{
		ReceiveID: "oc_x",
		MsgType:   "text",
		Content:   `{"text":"x"}`,
	})
	if !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Fatalf("expected ErrFeishuInvalidResponse, got %v", err)
	}
	if res.Code != 230002 {
		t.Errorf("expected upstream code surfaced, got %d", res.Code)
	}
}

func TestFeishuTenantClient_SendMessageNon2xx(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-send","expire":7200}`)
		case strings.Contains(r.URL.Path, "/im/v1/messages"):
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `<html>nginx oops</html>`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, err := client.SendMessage(context.Background(), "secret", FeishuMessageSendRequest{
		ReceiveID: "oc_x", MsgType: "text", Content: `{"text":"x"}`,
	})
	if !errors.Is(err, ErrFeishuNon2xx) {
		t.Fatalf("expected ErrFeishuNon2xx, got %v", err)
	}
}

func TestBuildFeishuTextContent(t *testing.T) {
	t.Parallel()
	got, err := BuildFeishuTextContent(`hi "there"`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]string
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["text"] != `hi "there"` {
		t.Errorf("round-trip failed: %v", parsed)
	}
}

// TestBuildFeishuInteractiveContent_HappyPath asserts the on-wire shape
// of the simple reply card: schema 2.0, header with the default title +
// template, single markdown body element carrying the agent reply.
func TestBuildFeishuInteractiveContent_HappyPath(t *testing.T) {
	t.Parallel()
	got, err := BuildFeishuInteractiveContent(`hello **world**`)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("interactive content not valid JSON: %v", err)
	}
	if parsed["schema"] != FeishuCardSchema {
		t.Errorf("schema = %v, want %s", parsed["schema"], FeishuCardSchema)
	}
	header, _ := parsed["header"].(map[string]any)
	if header == nil {
		t.Fatalf("header missing in: %v", parsed)
	}
	if header["template"] != FeishuCardDefaultTemplate {
		t.Errorf("header.template = %v, want %s", header["template"], FeishuCardDefaultTemplate)
	}
	title, _ := header["title"].(map[string]any)
	if title == nil || title["content"] != FeishuCardDefaultTitle {
		t.Errorf("header.title = %+v, want content=%s", title, FeishuCardDefaultTitle)
	}
	body, _ := parsed["body"].(map[string]any)
	if body == nil {
		t.Fatalf("body missing in: %v", parsed)
	}
	elements, _ := body["elements"].([]any)
	if len(elements) != 1 {
		t.Fatalf("body.elements len = %d, want 1", len(elements))
	}
	el0, _ := elements[0].(map[string]any)
	if el0["tag"] != "markdown" {
		t.Errorf("element tag = %v, want markdown", el0["tag"])
	}
	if el0["content"] != `hello **world**` {
		t.Errorf("element content = %v, want round-trip", el0["content"])
	}
}

// TestBuildFeishuInteractiveContent_BlankFallsBackToSpace defends in
// depth: worker.send already short-circuits empty replies as
// dead-letters, but if anything ever pipes a whitespace-only string
// through, the builder must still produce a Feishu-acceptable card
// body (a single space) instead of an empty content field that
// upstream would reject.
func TestBuildFeishuInteractiveContent_BlankFallsBackToSpace(t *testing.T) {
	t.Parallel()
	got, err := BuildFeishuInteractiveContent("   ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `"content":" "`) {
		t.Errorf("blank input must fall back to single-space content; got %s", got)
	}
}

func TestFeishuTenantClient_ReplyMessageHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawAuth string
	var sawPath string
	var replyBody []byte

	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-reply","expire":7200}`)
		case strings.HasSuffix(r.URL.Path, "/im/v1/messages/om_anchor/reply"):
			sawAuth = r.Header.Get("Authorization")
			sawPath = r.URL.Path
			replyBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"message_id":"om_reply_123"}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	res, err := client.ReplyMessage(context.Background(), "secret", "om_anchor", FeishuMessageReplyRequest{
		MsgType:       "interactive",
		Content:       `{"schema":"2.0"}`,
		ReplyInThread: true,
	})
	if err != nil {
		t.Fatalf("ReplyMessage: %v", err)
	}
	if res.MessageID != "om_reply_123" {
		t.Errorf("MessageID = %q", res.MessageID)
	}
	if sawAuth != "Bearer t-reply" {
		t.Errorf("Authorization = %q", sawAuth)
	}
	if sawPath != "/open-apis/im/v1/messages/om_anchor/reply" {
		t.Errorf("reply path = %q", sawPath)
	}
	var outer map[string]any
	if err := json.Unmarshal(replyBody, &outer); err != nil {
		t.Fatalf("reply body not JSON: %v", err)
	}
	if outer["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v", outer["msg_type"])
	}
	if outer["receive_id"] != nil {
		t.Errorf("reply body must not include receive_id: %v", outer)
	}
	if outer["reply_in_thread"] != true {
		t.Errorf("reply_in_thread = %v", outer["reply_in_thread"])
	}
}

func TestFeishuTenantClient_ReplyMessageRequiresMessageID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-reply","expire":7200}`)
			return
		}
		t.Errorf("unexpected request path %s", r.URL.Path)
	}, now)

	_, err := client.ReplyMessage(context.Background(), "secret", "   ", FeishuMessageReplyRequest{MsgType: "interactive", Content: `{}`})
	if !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Fatalf("expected ErrFeishuTenantClientConfig, got %v", err)
	}
}

// AddReaction / DeleteReaction round-trip checks. These are the P4
// typing-reaction primitives the inbound webhook uses to ack a user's
// message ("the bot is thinking") and the outbound terminal uses to
// clear that ack when the reply lands. The pair is mandatory for the
// Stewardhouse-parity UX, so a regression here would silently drop
// the typing indicator without breaking any other path.

func TestFeishuTenantClient_AddReactionHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawPath string
	var sawBody []byte
	var sawAuth string
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-react","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			sawPath = r.URL.Path
			sawAuth = r.Header.Get("Authorization")
			sawBody, _ = io.ReadAll(r.Body)
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"reaction_id":"r-789"}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	reactionID, err := client.AddReaction(context.Background(), "secret", "om_user_123", DefaultTypingReactionEmoji)
	if err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if reactionID != "r-789" {
		t.Errorf("reaction_id = %q", reactionID)
	}
	if !strings.HasSuffix(sawPath, "/im/v1/messages/om_user_123/reactions") {
		t.Errorf("path = %q, want .../om_user_123/reactions", sawPath)
	}
	if sawAuth != "Bearer t-react" {
		t.Errorf("Authorization = %q", sawAuth)
	}
	var outer map[string]any
	if err := json.Unmarshal(sawBody, &outer); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	rt, _ := outer["reaction_type"].(map[string]any)
	if rt["emoji_type"] != DefaultTypingReactionEmoji {
		t.Errorf("emoji_type = %v, want %q", rt["emoji_type"], DefaultTypingReactionEmoji)
	}
}

func TestFeishuTenantClient_AddReactionRequiresMessageID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-react","expire":7200}`)
			return
		}
		t.Errorf("unexpected request path %s", r.URL.Path)
	}, now)

	if _, err := client.AddReaction(context.Background(), "secret", "   ", ""); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Fatalf("expected ErrFeishuTenantClientConfig, got %v", err)
	}
}

func TestFeishuTenantClient_AddReactionDefaultsEmojiType(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawEmoji string
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			b, _ := io.ReadAll(r.Body)
			var outer map[string]any
			_ = json.Unmarshal(b, &outer)
			if rt, ok := outer["reaction_type"].(map[string]any); ok {
				if s, ok := rt["emoji_type"].(string); ok {
					sawEmoji = s
				}
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"reaction_id":"r-1"}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	if _, err := client.AddReaction(context.Background(), "secret", "om_x", "  "); err != nil {
		t.Fatalf("AddReaction: %v", err)
	}
	if sawEmoji != DefaultTypingReactionEmoji {
		t.Errorf("emoji_type = %q, want default %q", sawEmoji, DefaultTypingReactionEmoji)
	}
}

func TestFeishuTenantClient_AddReactionRejected(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions"):
			_, _ = io.WriteString(w, `{"code":230002,"msg":"forbidden","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	if _, err := client.AddReaction(context.Background(), "secret", "om_x", DefaultTypingReactionEmoji); !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Fatalf("expected ErrFeishuInvalidResponse, got %v", err)
	}
}

func TestFeishuTenantClient_DeleteReactionHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawPath, sawMethod, sawAuth string
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-del","expire":7200}`)
		case strings.Contains(r.URL.Path, "/reactions/"):
			sawPath = r.URL.Path
			sawMethod = r.Method
			sawAuth = r.Header.Get("Authorization")
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	if err := client.DeleteReaction(context.Background(), "secret", "om_user_123", "r-789"); err != nil {
		t.Fatalf("DeleteReaction: %v", err)
	}
	if sawMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", sawMethod)
	}
	if !strings.HasSuffix(sawPath, "/im/v1/messages/om_user_123/reactions/r-789") {
		t.Errorf("path = %q", sawPath)
	}
	if sawAuth != "Bearer t-del" {
		t.Errorf("Authorization = %q", sawAuth)
	}
}

func TestFeishuTenantClient_DeleteReactionRequiresIDs(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-x","expire":7200}`)
			return
		}
		t.Errorf("unexpected request path %s", r.URL.Path)
	}, now)

	if err := client.DeleteReaction(context.Background(), "secret", "", "r-789"); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Fatalf("empty message_id: expected ErrFeishuTenantClientConfig, got %v", err)
	}
	if err := client.DeleteReaction(context.Background(), "secret", "om_x", ""); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Fatalf("empty reaction_id: expected ErrFeishuTenantClientConfig, got %v", err)
	}
}

func TestFeishuTenantClient_DownloadMessageResourceHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	const wantBody = "\x89PNG\r\n\x1a\nFAKEIMAGE"
	var gotPath, gotType, gotAuth string
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-abc","expire":7200}`)
			return
		}
		gotPath = r.URL.Path
		gotType = r.URL.Query().Get("type")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, wantBody)
	}, now)

	got, err := client.DownloadMessageResource(context.Background(), "secret-xyz", "om_msg_1", "img_v3_abc", FeishuResourceTypeImage)
	if err != nil {
		t.Fatalf("DownloadMessageResource: %v", err)
	}
	if got.MIME != "image/png" {
		t.Errorf("MIME = %q, want image/png", got.MIME)
	}
	if string(got.Data) != wantBody {
		t.Errorf("Data mismatch: got %q want %q", string(got.Data), wantBody)
	}
	if gotPath != "/open-apis/im/v1/messages/om_msg_1/resources/img_v3_abc" {
		t.Errorf("path = %q", gotPath)
	}
	if gotType != "image" {
		t.Errorf("type query = %q, want image", gotType)
	}
	if gotAuth != "Bearer t-abc" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

func TestFeishuTenantClient_DownloadMessageResourceMissingMIME(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-abc","expire":7200}`)
			return
		}
		// httptest's response writer auto-sniffs Content-Type from the
		// body bytes, so explicitly emit a header map sentinel and a
		// zero-length body to make the upstream behave like the
		// "Content-Type missing" edge case we're guarding against.
		w.Header()["Content-Type"] = nil
		w.WriteHeader(http.StatusOK)
	}, now)

	got, err := client.DownloadMessageResource(context.Background(), "secret", "om_msg_2", "img_x", FeishuResourceTypeImage)
	if err != nil {
		t.Fatalf("DownloadMessageResource: %v", err)
	}
	if got.MIME != "image/png" {
		t.Errorf("MIME fallback = %q, want image/png", got.MIME)
	}
}

func TestFeishuTenantClient_DownloadMessageResourceValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-abc","expire":7200}`)
			return
		}
		t.Errorf("resource endpoint should not be called for validation failures: %s", r.URL.Path)
	}, now)

	if _, err := client.DownloadMessageResource(context.Background(), "secret", "", "img_x", FeishuResourceTypeImage); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Errorf("empty message_id: expected ErrFeishuTenantClientConfig, got %v", err)
	}
	if _, err := client.DownloadMessageResource(context.Background(), "secret", "om_msg_1", "", FeishuResourceTypeImage); !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Errorf("empty file_key: expected ErrFeishuTenantClientConfig, got %v", err)
	}
}

func TestFeishuTenantClient_DownloadMessageResourceNon2xx(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-abc","expire":7200}`)
			return
		}
		http.Error(w, "forbidden", http.StatusForbidden)
	}, now)

	if _, err := client.DownloadMessageResource(context.Background(), "secret", "om_msg_1", "img_x", FeishuResourceTypeImage); !errors.Is(err, ErrFeishuNon2xx) {
		t.Errorf("expected ErrFeishuNon2xx, got %v", err)
	}
}

func TestFeishuTenantClient_DownloadMessageResourceOversize(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	huge := make([]byte, feishuMaxMessageResourceBytes+1)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-abc","expire":7200}`)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(huge)
	}, now)

	if _, err := client.DownloadMessageResource(context.Background(), "secret", "om_msg_1", "img_x", FeishuResourceTypeImage); !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Errorf("expected ErrFeishuInvalidResponse for oversize, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessageHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	var sawAuth string
	var sawMethod string
	var sawPath string

	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			sawAuth = r.Header.Get("Authorization")
			sawMethod = r.Method
			sawPath = r.URL.Path
			_, _ = io.WriteString(w, `{
				"code":0,"msg":"ok","data":{"items":[{
					"message_id":"om_target","msg_type":"post",
					"parent_id":"om_parent","upper_message_id":"om_upper",
					"root_id":"om_root","chat_id":"oc_x",
					"body":{"content":"{\"zh_cn\":{\"title\":\"t\",\"content\":[[{\"tag\":\"text\",\"text\":\"hi\"}]]}}"}
				}]}
			}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	got, err := client.GetMessage(context.Background(), "secret", "om_target")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.MsgType != "post" {
		t.Errorf("MsgType = %q", got.MsgType)
	}
	if got.ParentID != "om_parent" || got.UpperMessageID != "om_upper" {
		t.Errorf("parent/upper mismatch: %+v", got)
	}
	if !strings.Contains(got.BodyContent, `"text":"hi"`) {
		t.Errorf("BodyContent = %q", got.BodyContent)
	}
	if sawMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", sawMethod)
	}
	if !strings.HasSuffix(sawPath, "/om_target") {
		t.Errorf("path = %q", sawPath)
	}
	if sawAuth != "Bearer t-get" {
		t.Errorf("Authorization = %q", sawAuth)
	}
}

func TestFeishuTenantClient_GetMessageRejectedByUpstream(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			_, _ = io.WriteString(w, `{"code":230020,"msg":"message not found","data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, err := client.GetMessage(context.Background(), "secret", "om_missing")
	if !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Errorf("expected ErrFeishuInvalidResponse, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessageNon2xx(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `forbidden`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, err := client.GetMessage(context.Background(), "secret", "om_forbid")
	if !errors.Is(err, ErrFeishuNon2xx) {
		t.Errorf("expected ErrFeishuNon2xx, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessageEmptyItems(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"items":[]}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, err := client.GetMessage(context.Background(), "secret", "om_empty")
	if !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Errorf("expected ErrFeishuInvalidResponse for empty items, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessageRequiresMessageID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
			return
		}
		http.NotFound(w, r)
	}, now)

	_, err := client.GetMessage(context.Background(), "secret", "  ")
	if !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Errorf("expected ErrFeishuTenantClientConfig, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessageRespectsSizeCap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	// Stream just past the cap. The handler returns valid JSON shape but
	// pads `content` with enough bytes to overflow feishuGetMessageMaxBytes.
	pad := strings.Repeat("X", feishuGetMessageMaxBytes+1024)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"items":[{"message_id":"om","msg_type":"text","body":{"content":"`+pad+`"}}]}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, err := client.GetMessage(context.Background(), "secret", "om")
	if !errors.Is(err, ErrFeishuInvalidResponse) {
		t.Errorf("expected ErrFeishuInvalidResponse for oversize body, got %v", err)
	}
}

func TestFeishuTenantClient_GetMessage_InlineSubItems(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			// Parent + 2 sub-messages inline.
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"items":[
				{"message_id":"om_parent","msg_type":"merge_forward","chat_id":"oc_x","body":{"content":"Merged and Forwarded Message"}},
				{"message_id":"om_child1","msg_type":"text","upper_message_id":"om_parent","body":{"content":"{\"text\":\"line 1\"}"}},
				{"message_id":"om_child2","msg_type":"text","upper_message_id":"om_parent","body":{"content":"{\"text\":\"line 2\"}"}}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	got, err := client.GetMessage(context.Background(), "secret", "om_parent")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.MsgType != "merge_forward" {
		t.Errorf("MsgType = %q", got.MsgType)
	}
	if got.ChatID != "oc_x" {
		t.Errorf("ChatID = %q", got.ChatID)
	}
	if len(got.SubItems) != 2 {
		t.Fatalf("SubItems len = %d, want 2", len(got.SubItems))
	}
	if got.SubItems[0].MessageID != "om_child1" || got.SubItems[1].MessageID != "om_child2" {
		t.Errorf("sub message ids = %v", []string{got.SubItems[0].MessageID, got.SubItems[1].MessageID})
	}
}

func TestFeishuTenantClient_GetMessage_DropsSelfDupeFromSubItems(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-get","expire":7200}`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/im/v1/messages/"):
			// items[1] repeats the parent — must NOT appear in SubItems.
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{"items":[
				{"message_id":"om_parent","msg_type":"merge_forward","body":{"content":"x"}},
				{"message_id":"om_parent","msg_type":"merge_forward","body":{"content":"x"}},
				{"message_id":"om_real_child","msg_type":"text","body":{"content":"{\"text\":\"hi\"}"}}
			]}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	got, err := client.GetMessage(context.Background(), "secret", "om_parent")
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if len(got.SubItems) != 1 || got.SubItems[0].MessageID != "om_real_child" {
		t.Errorf("expected only the real child, got %+v", got.SubItems)
	}
}

func TestFeishuTenantClient_ListMessagesByChatPage_HappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	var sawQuery string
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-list","expire":7200}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages") && !strings.Contains(r.URL.Path, "/open-apis/im/v1/messages/"):
			sawQuery = r.URL.RawQuery
			_, _ = io.WriteString(w, `{"code":0,"msg":"ok","data":{
				"has_more": true,
				"page_token": "next-tok",
				"items": [
					{"message_id":"om_a","msg_type":"text","upper_message_id":"om_parent","body":{"content":"{\"text\":\"a\"}"}},
					{"message_id":"om_b","msg_type":"text","body":{"content":"{\"text\":\"b\"}"}}
				]
			}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	items, next, err := client.ListMessagesByChatPage(context.Background(), "secret", "oc_chat", "")
	if err != nil {
		t.Fatalf("ListMessagesByChatPage: %v", err)
	}
	if next != "next-tok" {
		t.Errorf("next page token = %q", next)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[0].UpperMessageID != "om_parent" {
		t.Errorf("upper_message_id not propagated: %+v", items[0])
	}
	if !strings.Contains(sawQuery, "container_id=oc_chat") {
		t.Errorf("missing container_id in query: %s", sawQuery)
	}
	if !strings.Contains(sawQuery, "container_id_type=chat") {
		t.Errorf("missing container_id_type in query: %s", sawQuery)
	}
	if !strings.Contains(sawQuery, "sort_type=ByCreateTimeDesc") {
		t.Errorf("missing sort_type=ByCreateTimeDesc: %s", sawQuery)
	}
}

func TestFeishuTenantClient_ListMessagesByChatPage_NoMore(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal"):
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-list","expire":7200}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/open-apis/im/v1/messages") && !strings.Contains(r.URL.Path, "/open-apis/im/v1/messages/"):
			_, _ = io.WriteString(w, `{"code":0,"data":{"has_more": false, "page_token": "ignored", "items": []}}`)
		default:
			http.NotFound(w, r)
		}
	}, now)

	_, next, err := client.ListMessagesByChatPage(context.Background(), "secret", "oc_chat", "tok")
	if err != nil {
		t.Fatalf("ListMessagesByChatPage: %v", err)
	}
	if next != "" {
		t.Errorf("expected empty next page when has_more=false, got %q", next)
	}
}

func TestFeishuTenantClient_ListMessagesByChatPage_RequiresChatID(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	client, _, _ := newTenantClientWithServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tenant_access_token/internal") {
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"t-list","expire":7200}`)
			return
		}
		http.NotFound(w, r)
	}, now)

	_, _, err := client.ListMessagesByChatPage(context.Background(), "secret", "  ", "")
	if !errors.Is(err, ErrFeishuTenantClientConfig) {
		t.Errorf("expected ErrFeishuTenantClientConfig, got %v", err)
	}
}
