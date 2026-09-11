package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type recordingStore struct {
	SessionStore
	tenant string
	input  store.CreateSessionInput
}

func (s *recordingStore) CreateSession(_ context.Context, tenant string, input store.CreateSessionInput) (store.Session, error) {
	s.tenant, s.input = tenant, input
	return store.Session{ID: uuid.NewString(), TenantID: tenant, Metadata: input.Metadata, Configuration: input.Configuration, CreatedAt: time.Unix(1700000000, 0)}, nil
}

func testHandler(t *testing.T) (http.Handler, *recordingStore, string) {
	t.Helper()
	tenant := uuid.NewString()
	hash := sha256.Sum256([]byte("test-api-key"))
	auth, err := NewAuthenticator([]APIKey{{TokenSHA256: hex.EncodeToString(hash[:]), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	s := &recordingStore{}
	h, err := NewHandler(s, auth, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	return h, s, tenant
}

func TestHTTPConfigurationAndTenantIdentity(t *testing.T) {
	h, s, tenant := testHandler(t)
	body := `{"agent":{"model":"requested-model","instructions":"Keep this."},"environment":{"type":"none"},"metadata":{"tenant_id":"untrusted-tenant"}}`
	request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer test-api-key")
	request.Header.Set("OpenAI-Beta", "agents=v1")
	request.Header.Set("Idempotency-Key", "retry-key")
	request.Header.Set("X-Tenant-ID", "untrusted-tenant")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != http.StatusOK || s.tenant != tenant || s.input.Engine != "claude_code" || s.input.IdempotencyKey != "retry-key" {
		t.Fatalf("request = %d %s; tenant=%s, engine=%s", w.Code, w.Body, s.tenant, s.input.Engine)
	}
	var response v1.Session
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Agent.Model != "requested-model" || response.Object != "agent.session" || response.Status != "idle" || response.CreatedAt != 1700000000 {
		t.Fatalf("invalid response: %s, %v", w.Body, err)
	}
	if response.RequiredActions == nil || response.VaultIDs == nil || response.Agent.Tools == nil {
		t.Fatal("upstream list fields must be empty arrays, not null")
	}
}

func TestHTTPRejectsUntrustedOrUnsupportedRequests(t *testing.T) {
	valid := `{"agent":{"model":"example"},"environment":{"type":"none"}}`
	for _, test := range []struct {
		name, auth, beta, path, body string
		status                       int
	}{
		{"missing auth", "", "agents=v1", "/v1/agents/sessions", valid, 401},
		{"invalid auth", "Bearer wrong", "agents=v1", "/v1/agents/sessions", valid, 401},
		{"missing beta", "Bearer test-api-key", "", "/v1/agents/sessions", valid, 400},
		{"tenant query", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions?tenant_id=other", valid, 400},
		{"tenant body", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"tenant_id":"other","agent":`, 1), 400},
		{"hosted environment", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"none"`, `"openai_hosted"`, 1), 400},
		{"initial input", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"input":"run it","agent":`, 1), 400},
		{"stream", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"stream":true,"agent":`, 1), 400},
		{"saved agent", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"agent":`, `"agent_id":"saved","agent":`, 1), 400},
		{"unknown agent option", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", strings.Replace(valid, `"model":`, `"tools":[{}],"model":`, 1), 400},
		{"multiple objects", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", valid + `{}`, 400},
		{"no object", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", `null`, 400},
		{"large body", "Bearer test-api-key", "agents=v1", "/v1/agents/sessions", `{"agent":{"model":"` + strings.Repeat("x", 1024*1024) + `"}}`, 413},
	} {
		t.Run(test.name, func(t *testing.T) {
			h, s, _ := testHandler(t)
			r := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(test.body))
			r.Header.Set("Authorization", test.auth)
			r.Header.Set("OpenAI-Beta", test.beta)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			var response v1.ErrorResponse
			if w.Code != test.status || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error.Code == "" || s.tenant != "" {
				t.Fatalf("response = %d %s, stored tenant = %s", w.Code, w.Body, s.tenant)
			}
		})
	}
}

func TestAuthenticatorRejectsInvalidBindings(t *testing.T) {
	digest := strings.Repeat("a", 64)
	tenant := uuid.NewString()
	for _, keys := range [][]APIKey{nil, {{TenantID: tenant, TokenSHA256: "bad"}}, {{TenantID: "not-a-tenant", TokenSHA256: digest}}, {{TenantID: tenant, TokenSHA256: digest}, {TenantID: uuid.NewString(), TokenSHA256: digest}}} {
		if _, err := NewAuthenticator(keys); err == nil {
			t.Fatal("invalid authentication bindings accepted")
		}
	}
}
