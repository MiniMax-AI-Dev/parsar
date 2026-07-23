package mcpoauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOAuthDiscoveryRegistrationExchangeAndRefresh(t *testing.T) {
	var server *httptest.Server
	var tokenGrantTypes []string
	handler := http.NewServeMux()
	handler.HandleFunc("/.well-known/oauth-protected-resource/mcp", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"resource":              server.URL + "/mcp",
			"authorization_servers": []string{server.URL},
			"scopes_supported":      []string{"openid", "offline_access"},
		})
	})
	handler.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/authorize",
			"token_endpoint":                   server.URL + "/token",
			"registration_endpoint":            server.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	handler.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["token_endpoint_auth_method"] != "none" {
			t.Fatalf("registration auth method = %v", body["token_endpoint_auth_method"])
		}
		writeTestJSON(t, w, http.StatusCreated, map[string]any{
			"client_id":                  "client-1",
			"token_endpoint_auth_method": "none",
		})
	})
	handler.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		tokenGrantTypes = append(tokenGrantTypes, r.Form.Get("grant_type"))
		if r.Form.Get("client_id") != "client-1" || r.Form.Get("resource") != server.URL+"/mcp" {
			t.Fatalf("unexpected token form: %v", r.Form)
		}
		if r.Form.Get("grant_type") == "authorization_code" {
			if r.Form.Get("code_verifier") == "" || r.Form.Get("code") != "code-1" {
				t.Fatalf("missing PKCE exchange values: %v", r.Form)
			}
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"access_token":  "access-1",
				"refresh_token": "refresh-1",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
			return
		}
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"access_token": "access-2",
			"token_type":   "Bearer",
			"expires_in":   "7200",
		})
	})
	server = httptest.NewTLSServer(handler)
	defer server.Close()

	client := New(server.Client())
	now := time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC)
	client.now = func() time.Time { return now }
	redirectURI := "http://127.0.0.1:18080/oauth/callback"
	transaction, authorizeURL, err := client.Begin(t.Context(), server.URL+"/mcp", redirectURI)
	if err != nil {
		t.Fatal(err)
	}
	parsedAuthorizeURL, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	query := parsedAuthorizeURL.Query()
	if query.Get("client_id") != "client-1" || query.Get("redirect_uri") != redirectURI || query.Get("resource") != server.URL+"/mcp" || query.Get("scope") != "openid offline_access" {
		t.Fatalf("unexpected authorize query: %v", query)
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("state") == "" {
		t.Fatalf("missing PKCE authorize query: %v", query)
	}

	credential, err := client.Exchange(t.Context(), transaction, "code-1")
	if err != nil {
		t.Fatal(err)
	}
	if credential.AccessToken != "access-1" || credential.RefreshToken != "refresh-1" || !credential.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("unexpected credential: %+v", credential)
	}

	refreshed, err := client.Refresh(t.Context(), credential)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "access-2" || refreshed.RefreshToken != "refresh-1" || !refreshed.ExpiresAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("unexpected refreshed credential: %+v", refreshed)
	}
	if strings.Join(tokenGrantTypes, ",") != "authorization_code,refresh_token" {
		t.Fatalf("grant types = %v", tokenGrantTypes)
	}
}

func TestOAuthDiscoveryFallsBackToOpenIDConfiguration(t *testing.T) {
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"resource":              server.URL,
			"authorization_servers": []string{server.URL},
		})
	})
	handler.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	})
	handler.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusOK, map[string]any{
			"issuer":                           server.URL,
			"authorization_endpoint":           server.URL + "/authorize",
			"token_endpoint":                   server.URL + "/token",
			"registration_endpoint":            server.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	handler.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(t, w, http.StatusCreated, map[string]any{
			"client_id":                  "openid-client",
			"token_endpoint_auth_method": "none",
		})
	})
	server = httptest.NewTLSServer(handler)
	defer server.Close()

	transaction, authorizeURL, err := New(server.Client()).Begin(
		t.Context(),
		server.URL,
		"http://127.0.0.1:18080/oauth/callback",
	)
	if err != nil {
		t.Fatal(err)
	}
	if transaction.ClientID != "openid-client" || !strings.HasPrefix(authorizeURL, server.URL+"/authorize?") {
		t.Fatalf("transaction=%+v authorizeURL=%q", transaction, authorizeURL)
	}
}

func TestBeginRejectsNonHTTPSResource(t *testing.T) {
	_, _, err := New(nil).Begin(t.Context(), "http://example.com/mcp", "http://127.0.0.1/callback")
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("error = %v", err)
	}
}

func TestProbeInitializesAndListsTools(t *testing.T) {
	var calls []string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer access-1" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		var request struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, request.Method)
		switch request.Method {
		case "initialize":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Mcp-Session-Id", "session-1")
			_, _ = w.Write([]byte("event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"protocolVersion\":\"2025-06-18\",\"serverInfo\":{\"name\":\"Notion\",\"version\":\"1.2.3\"}}}\n\n"))
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				t.Fatalf("session id = %q", r.Header.Get("Mcp-Session-Id"))
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeTestJSON(t, w, http.StatusOK, map[string]any{
				"jsonrpc": "2.0",
				"id":      2,
				"result": map[string]any{
					"tools": []map[string]any{{"name": "notion-search"}, {"name": "notion-fetch"}},
				},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	result, err := New(server.Client()).Probe(t.Context(), server.URL, "access-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.ServerName != "Notion" || result.ServerVersion != "1.2.3" || result.ToolCount != 2 {
		t.Fatalf("result = %+v", result)
	}
	if strings.Join(calls, ",") != "initialize,notifications/initialized,tools/list" {
		t.Fatalf("calls = %v", calls)
	}
}

func TestProbeReportsRejectedAuthorization(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	_, err := New(server.Client()).Probe(t.Context(), server.URL, "revoked")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error = %v", err)
	}
}

func TestCredentialPayloadRoundTrip(t *testing.T) {
	want := Credential{
		AccessToken:             "access",
		RefreshToken:            "refresh",
		TokenType:               "Bearer",
		ExpiresAt:               time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
		ClientID:                "client",
		TokenEndpointAuthMethod: "none",
		TokenEndpoint:           "https://example.com/token",
		Resource:                "https://example.com/mcp",
	}
	got, ok, err := CredentialFromPayload(want.Payload())
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, status int, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
