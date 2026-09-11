package v1_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	client "github.com/MiniMax-AI-Dev/parsar/packages/agents-client/v1"
	"github.com/openai/openai-go/v3"
)

func TestExplicitServiceIdentityAndNoSDKRetries(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "unrelated-key")
	t.Setenv("OPENAI_BASE_URL", "http://unrelated.invalid")
	t.Setenv("OPENAI_ORG_ID", "unrelated-org")
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/prefix/v1/agents/sessions/session-1" || r.Header.Get("Authorization") != "Bearer service-key" || r.Header.Get("OpenAI-Beta") != "agents=v1" || r.Header.Get("OpenAI-Organization") != "" {
			t.Error("incorrect service address or identity")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"unavailable","message":"try later","type":"server_error"}}`))
	}))
	defer s.Close()
	c, err := client.New(client.Config{BaseURL: s.URL + "/prefix/v1", APIKey: "service-key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "session-1")
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 || apiErr.Code != "unavailable" || calls != 1 {
		t.Fatalf("expected one request and the SDK error: %v", err)
	}
}

func TestRedirectsAndProductCookies(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/unexpected" || r.Header.Get("Cookie") != "" {
			t.Error("followed redirect or sent product cookies")
		}
		w.Header().Set("Location", "/unexpected")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer s.Close()
	jar, _ := cookiejar.New(nil)
	u, _ := url.Parse(s.URL)
	jar.SetCookies(u, []*http.Cookie{{Name: "product-session", Value: "private"}})
	h := &http.Client{Jar: jar, Timeout: time.Second}
	c, err := client.New(client.Config{BaseURL: s.URL + "/v1/", APIKey: "service-key", HTTPClient: h})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get(context.Background(), "session-1")
	if err == nil || !strings.Contains(err.Error(), "redirects are disabled") || h.Jar != jar || h.CheckRedirect != nil {
		t.Fatal("redirect policy failed or modified the caller's client")
	}
}

func TestConfigurationAndContext(t *testing.T) {
	for _, base := range []string{"relative", "ftp://host/v1", "http://user:secret@host/v1", "http://host/v1?key=secret"} {
		if _, err := client.New(client.Config{BaseURL: base, APIKey: "key"}); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("invalid base URL accepted or exposed")
		}
	}
	if _, err := client.New(client.Config{BaseURL: "http://localhost/v1"}); err == nil {
		t.Fatal("accepted missing service key")
	}
	c, err := client.New(client.Config{BaseURL: "http://localhost/v1", APIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Get(ctx, "session-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost context cancellation: %v", err)
	}
}
