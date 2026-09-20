package store_test

import (
	"bytes"
	"encoding/json"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestModelExecutionHTTPWriteOnlyAndStrictAdmission(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, _ := credentialcrypto.New(bytes.Repeat([]byte{6}, 32))
	st := store.NewWithCredentialCipher(pool, cipher)
	tenant, token := uuid.NewString(), uuid.NewString()
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: uuid.NewString(), SubjectKind: "service_account", SubjectID: "catalog-test", TokenSHA256: device.HashCredential(token), TenantID: tenant}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := api.NewHandler(st, auth, "codex", api.WithHostedEnvironments(), api.WithExecution(st))
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("OpenAI-Beta", "agents=v1")
		r.Header.Set("Idempotency-Key", key)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), "model-http-canary") {
			t.Fatal("credential echoed in public response")
		}
		return w
	}
	body := `{"agent":{"model":"actual-model","x_agents_core":{"harness":"codex"}},"environment":{"type":"openai_hosted"},"x_agents_core":{"model_provider":{"protocol":"responses","base_url":"https://example.com/v1","api_key":"model-http-canary"}}}`
	key := uuid.NewString()
	w := call("POST", "/v1/agents/sessions", body, key)
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var session struct{ ID string }
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil || session.ID == "" {
		t.Fatal("missing Session", err)
	}
	if w := call("GET", "/v1/agents/sessions/"+session.ID, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("POST", "/v1/agents/sessions", body, key); w.Code != 200 {
		t.Fatal("creation retry failed", w.Code)
	}
	if w := call("POST", "/v1/agents/sessions", strings.Replace(body, "model-http-canary", "changed-key", 1), key); w.Code != 409 {
		t.Fatal("conflicting credentials accepted", w.Code)
	}
	for _, invalid := range []string{strings.Replace(body, `"protocol":"responses"`, `"protocol":"anthropic"`, 1), strings.Replace(body, `"api_key":"model-http-canary"`, `"api_key":"model-http-canary","unknown":true`, 1), strings.Replace(body, `"type":"openai_hosted"`, `"type":"none"`, 1), strings.Replace(body, `"api_key":"model-http-canary"`, `"api_key":null`, 1)} {
		if w := call("POST", "/v1/agents/sessions", invalid, uuid.NewString()); w.Code != 400 {
			t.Fatalf("invalid execution accepted: %d %s", w.Code, w.Body)
		}
	}
}
