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

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

type vaultResourceFixture struct {
	ResourceStore
	vault      store.Vault
	err        error
	tenant, id string
	calls      int
}

func (f *vaultResourceFixture) CreateVault(_ context.Context, tenant string, input store.CreateVaultInput) (store.Vault, error) {
	f.tenant, f.calls = tenant, f.calls+1
	f.vault.Name, f.vault.Metadata = input.Name, input.Metadata
	return f.vault, f.err
}

func (f *vaultResourceFixture) GetVault(_ context.Context, tenant, id string) (store.Vault, error) {
	f.tenant, f.id, f.calls = tenant, id, f.calls+1
	return f.vault, f.err
}

func vaultResourceHandler(t *testing.T) (http.Handler, *vaultResourceFixture) {
	t.Helper()
	f := &vaultResourceFixture{vault: store.Vault{ID: uuid.NewString(), TenantID: uuid.NewString(), Metadata: map[string]string{}, CreatedAt: time.Unix(1700000000, 0)}}
	auth, err := NewAuthenticator([]APIKey{{OrganizationID: "vault-org", ProjectID: "vault-project", SubjectKind: "user", SubjectID: "vault-owner", TokenSHA256: device.HashCredential("vault-key"), TenantID: f.vault.TenantID}})
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(f, auth, "claude_code")
	if err != nil {
		t.Fatal(err)
	}
	return h, f
}

func vaultRequest(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer vault-key")
	r.Header.Set("OpenAI-Beta", "agents=v1")
	r.Header.Set("X-Tenant-ID", "untrusted-tenant")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestVaultResourceProjectionWithoutExecution(t *testing.T) {
	for _, test := range []struct {
		body     string
		name     any
		metadata map[string]any
	}{
		{`{}`, nil, map[string]any{}},
		{`{"metadata":null}`, nil, map[string]any{}},
		{`{"name":"  凭据库 \n","metadata":{"team":"engineering"}}`, "凭据库", map[string]any{"team": "engineering"}},
		{`{"name":" ` + strings.Repeat("界", 85) + `x "}`, strings.Repeat("界", 85) + "x", map[string]any{}},
	} {
		h, f := vaultResourceHandler(t)
		w := vaultRequest(h, "POST", "/v1/vaults", test.body)
		var got map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal(w.Code, w.Body)
		}
		want := map[string]any{"id": f.vault.ID, "object": "vault", "created_at": float64(1700000000), "name": test.name, "metadata": test.metadata}
		if !reflect.DeepEqual(got, want) || f.tenant != f.vault.TenantID || f.calls != 1 {
			t.Fatal("incorrect resource or authenticated owner", got, f)
		}
		read := vaultRequest(h, "GET", "/v1/vaults/"+f.vault.ID, "")
		if read.Code != 200 || read.Body.String() != w.Body.String() || f.id != f.vault.ID || f.calls != 2 {
			t.Fatal("retrieve changed resource", read.Code, read.Body, f)
		}
	}
}

func TestVaultResourceInvalidRequestsDoNotReachStore(t *testing.T) {
	for _, body := range []string{
		`null`, `[]`, `{} {}`, `{"name":null}`, `{"name":1}`, `{"name":""}`, `{"name":" \n\t "}`,
		`{"name":"` + strings.Repeat("界", 85) + `xx"}`, `{"metadata":[]}`, `{"metadata":{"key":null}}`, `{"metadata":{"key":1}}`,
		`{"tenant_id":"untrusted"}`, `{"credentials":[]}`,
	} {
		h, f := vaultResourceHandler(t)
		w := vaultRequest(h, "POST", "/v1/vaults", body)
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("invalid request reached storage", body, w.Code, f.calls)
		}
	}
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"POST", "/v1/vaults?tenant_id=foreign", 400},
		{"GET", "/v1/vaults/not-a-vault", 404},
		{"GET", "/v1/vaults/" + uuid.Nil.String(), 404},
		{"GET", "/v1/vaults/" + uuid.NewString() + "?tenant_id=foreign", 400},
		{"POST", "/v1/agents/vaults", 404},
	} {
		h, f := vaultResourceHandler(t)
		w := vaultRequest(h, test.method, test.path, `{}`)
		if w.Code != test.status || f.calls != 0 {
			t.Fatal(test, w.Code, f.calls)
		}
	}
}

func TestVaultResourceUsesSharedAuthenticationAndErrors(t *testing.T) {
	for _, method := range []string{"POST", "GET"} {
		for _, test := range []struct {
			auth, beta string
			status     int
		}{{"", "agents=v1", 401}, {"Bearer invalid", "agents=v1", 401}, {"Bearer vault-key", "", 400}} {
			h, f := vaultResourceHandler(t)
			path := "/v1/vaults"
			if method == "GET" {
				path += "/" + f.vault.ID
			}
			r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			r.Header.Set("Authorization", test.auth)
			r.Header.Set("OpenAI-Beta", test.beta)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status || f.calls != 0 {
				t.Fatal(method, test, w.Code, f.calls)
			}
		}
	}
	for _, test := range []struct {
		err    error
		status int
	}{{store.ErrNotFound, 404}, {errors.New("private-vault-backend"), 500}} {
		h, f := vaultResourceHandler(t)
		f.err = test.err
		w := vaultRequest(h, "GET", "/v1/vaults/"+f.vault.ID, "")
		if w.Code != test.status || strings.Contains(w.Body.String(), "private-vault-backend") {
			t.Fatal(w.Code, w.Body)
		}
	}
}
