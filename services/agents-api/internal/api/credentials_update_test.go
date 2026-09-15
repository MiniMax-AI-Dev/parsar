package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func (f *credentialFixture) UpdateStaticCredential(_ context.Context, tenant, vault, id string, input store.UpdateStaticCredentialInput) (store.Credential, error) {
	f.tenant, f.vault, f.id, f.replacement, f.calls = tenant, vault, id, input, f.calls+1
	return f.credential, f.err
}

func TestCredentialUpdatePreservesOpaqueInputAndSafeProjection(t *testing.T) {
	for _, token := range []string{"", " \tcredential-canary\n雪 ", "credential-canary"} {
		h, f, tenant := credentialHandler(t)
		f.credential.Name, f.credential.MCPServerURL = "Retained name", "https://example.invalid/mcp?q=x"
		body, _ := json.Marshal(map[string]any{"auth": map[string]string{"type": "static_bearer", "token": token}})
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, string(body))
		var got map[string]any
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal("update failed", w.Code)
		}
		want := map[string]any{"id": f.credential.ID, "vault_id": f.credential.VaultID, "name": f.credential.Name,
			"object": "vault.credential", "created_at": float64(f.credential.CreatedAt.Unix()), "updated_at": float64(f.credential.UpdatedAt.Unix()),
			"auth": map[string]any{"type": "static_bearer", "mcp_server_url": f.credential.MCPServerURL}}
		if !reflect.DeepEqual(got, want) || f.tenant != tenant || f.vault != f.credential.VaultID || f.id != f.credential.ID || f.replacement.Token != token || f.calls != 1 || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("update changed scope, secret bytes or safe resource shape")
		}
	}
}

func TestCredentialUpdateRejectsInvalidBodiesBeforeStorage(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{} {}`, `{}`, `{"auth":null}`, `{"auth":[]}`, `{"auth":{}}`,
		`{"auth":{"type":"static_bearer"}}`, `{"auth":{"token":"credential-canary"}}`,
		`{"auth":{"type":null,"token":"credential-canary"}}`, `{"auth":{"type":3,"token":"credential-canary"}}`,
		`{"auth":{"type":"static_bearer","token":null}}`, `{"auth":{"type":"static_bearer","token":3}}`,
		`{"auth":{"type":"static_bearer","token":{}}}`, `{"auth":{"type":"static_bearer","token":[]}}`,
		`{"auth":{"type":"mcp_oauth","access_token":"credential-canary"}}`,
		`{"auth":{"type":"static_bearer","token":"credential-canary","mcp_server_url":"https://other.invalid"}}`,
		`{"auth":{"type":"static_bearer","token":"credential-canary"},"name":"other"}`,
		`{"auth":{"type":"static_bearer","token":"credential-canary"},"vault_id":"other"}`,
	} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, body)
		if w.Code != http.StatusBadRequest || f.calls != 0 || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("invalid update reached storage or disclosed input", w.Code)
		}
	}
}

func TestCredentialUpdateUsesExistingBoundariesAndSafeErrors(t *testing.T) {
	const body = `{"auth":{"type":"static_bearer","token":"credential-canary"}}`
	for _, mode := range []string{"missing auth", "missing beta", "method", "query", "invalid Vault", "invalid Credential", "zero ID"} {
		h, f, _ := credentialHandler(t)
		path := "/v1/vaults/" + f.credential.VaultID + "/credentials/" + f.credential.ID
		method, status := "POST", http.StatusBadRequest
		switch mode {
		case "method":
			method, status = "PATCH", http.StatusMethodNotAllowed
		case "query":
			path += "?unknown=1"
		case "invalid Vault":
			path, status = strings.Replace(path, f.credential.VaultID, "invalid", 1), http.StatusNotFound
		case "invalid Credential":
			path, status = strings.Replace(path, f.credential.ID, "invalid", 1), http.StatusNotFound
		case "zero ID":
			path, status = strings.Replace(path, f.credential.ID, uuid.Nil.String(), 1), http.StatusNotFound
		case "missing auth":
			status = http.StatusUnauthorized
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if mode != "missing auth" {
			r.Header.Set("Authorization", "Bearer test-api-key")
		}
		if mode != "missing beta" {
			r.Header.Set("OpenAI-Beta", "agents=v1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status || f.calls != 0 {
			t.Fatal("update boundary changed", mode, w.Code)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{store.ErrNotFound, 404}, {store.ErrCredentialStorageUnavailable, 503}, {errors.New("credential-canary"), 500}} {
		h, f, _ := credentialHandler(t)
		f.err = tc.err
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, body)
		if w.Code != tc.status || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("update exposed a storage error", w.Code)
		}
	}
}
