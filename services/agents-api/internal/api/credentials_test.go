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
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type credentialFixture struct {
	ResourceStore
	credential        store.Credential
	input             store.CreateStaticCredentialInput
	tenant, vault, id string
	calls             int
	err               error
}

func (f *credentialFixture) CreateStaticCredential(_ context.Context, tenant, vault string, input store.CreateStaticCredentialInput) (store.Credential, error) {
	f.tenant, f.vault, f.input, f.calls = tenant, vault, input, f.calls+1
	f.credential.Name, f.credential.MCPServerURL = input.Name, input.MCPServerURL
	return f.credential, f.err
}

func (f *credentialFixture) GetCredential(_ context.Context, tenant, vault, id string) (store.Credential, error) {
	f.tenant, f.vault, f.id, f.calls = tenant, vault, id, f.calls+1
	return f.credential, f.err
}

func credentialHandler(t *testing.T) (http.Handler, *credentialFixture, string) {
	t.Helper()
	h, s, tenant := testHandler(t)
	f := &credentialFixture{credential: store.Credential{ID: uuid.NewString(), VaultID: uuid.NewString(), AuthType: "static_bearer", CreatedAt: time.Unix(1700000000, 0), UpdatedAt: time.Unix(1700000000, 0)}}
	s.ResourceStore = f
	return h, f, tenant
}

func credentialRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer test-api-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("X-Tenant-ID", "untrusted")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCredentialSafeProjectionAndOpaqueInput(t *testing.T) {
	h, f, tenant := credentialHandler(t)
	path := "/v1/vaults/" + f.credential.VaultID + "/credentials"
	w := credentialRequest(h, "POST", path, `{"name":" Vault credential \n","auth":{"type":"static_bearer","mcp_server_url":"https://example.invalid/mcp?q=x","token":" \tcredential-canary\n雪 "}}`)
	var got map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal(w.Code, w.Body)
	}
	want := map[string]any{"id": f.credential.ID, "vault_id": f.credential.VaultID, "name": "Vault credential", "object": "vault.credential", "created_at": float64(1700000000), "updated_at": float64(1700000000), "auth": map[string]any{"type": "static_bearer", "mcp_server_url": "https://example.invalid/mcp?q=x"}}
	if !reflect.DeepEqual(got, want) || f.tenant != tenant || f.vault != f.credential.VaultID || f.input.Token != " \tcredential-canary\n雪 " {
		t.Fatal("resource projection or authenticated request changed")
	}
	read := credentialRequest(h, "GET", path+"/"+f.credential.ID, "")
	if read.Code != 200 || read.Body.String() != w.Body.String() || f.id != f.credential.ID || f.calls != 2 {
		t.Fatal("safe retrieve changed", read.Code)
	}
}

func TestCredentialInvalidRequestsNeverReachStorage(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{} {}`, `{}`, `{"name":null,"auth":{}}`,
		`{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://example.invalid","token":null}}`,
		`{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://credential-canary@example.invalid","token":"credential-canary"}}`,
		`{"name":"n","auth":{"type":"mcp_oauth","access_token":"credential-canary"}}`,
		`{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"http://example.invalid","token":"credential-canary"}}`,
		`{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://example.invalid#fragment","token":"credential-canary"}}`,
	} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials", body)
		if w.Code != 400 || f.calls != 0 || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("invalid request reached storage or leaked input", w.Code, f.calls)
		}
	}
	for _, id := range []string{"invalid", uuid.Nil.String()} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "GET", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+id, "")
		if w.Code != 404 || f.calls != 0 {
			t.Fatal("malformed ID reached storage", w.Code, f.calls)
		}
	}
}

func TestCredentialStorageErrorsStaySafe(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
	}{{store.ErrNotFound, 404}, {store.ErrCredentialStorageUnavailable, 503}, {errors.New("credential-canary"), 500}} {
		h, f, _ := credentialHandler(t)
		f.err = test.err
		w := credentialRequest(h, "POST", "/v1/vaults/"+f.credential.VaultID+"/credentials", `{"name":"n","auth":{"type":"static_bearer","mcp_server_url":"https://example.invalid","token":"credential-canary"}}`)
		if w.Code != test.status || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("unsafe storage error", w.Code)
		}
	}
}
