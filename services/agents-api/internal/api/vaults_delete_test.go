package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func (f *vaultResourceFixture) DeleteVault(_ context.Context, tenant, id string) (string, error) {
	f.tenant, f.id, f.calls = tenant, id, f.calls+1
	return f.vault.ID, f.err
}

func TestVaultDeletionConfirmationAndScope(t *testing.T) {
	h, f := vaultResourceHandler(t)
	w := vaultRequest(h, "DELETE", "/v1/vaults/"+f.vault.ID, "")
	var got map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal("deletion failed", w.Code)
	}
	want := map[string]any{"id": f.vault.ID, "deleted": true, "object": "vault.deleted"}
	if !reflect.DeepEqual(got, want) || f.tenant != f.vault.TenantID || f.id != f.vault.ID || f.calls != 1 {
		t.Fatal("deletion changed authenticated scope or confirmation")
	}
}

func TestVaultDeletionRejectsBeforeMutation(t *testing.T) {
	for _, mode := range []string{"auth", "beta", "query", "body", "null", "invalid", "zero"} {
		t.Run(mode, func(t *testing.T) {
			h, f := vaultResourceHandler(t)
			path, body, status := "/v1/vaults/"+f.vault.ID, "", 400
			switch mode {
			case "auth":
				status = 401
			case "query":
				path += "?tenant_id=untrusted"
			case "body":
				body = `{"token":"vault-delete-canary"}`
			case "null":
				body = "null"
			case "invalid":
				path, status = "/v1/vaults/invalid", 404
			case "zero":
				path, status = "/v1/vaults/"+uuid.Nil.String(), 404
			}
			r := httptest.NewRequest("DELETE", path, strings.NewReader(body))
			if mode != "auth" {
				r.Header.Set("Authorization", "Bearer vault-key")
			}
			if mode != "beta" {
				r.Header.Set("OpenAI-Beta", "agents=v1")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != status || f.calls != 0 || strings.Contains(w.Body.String(), "vault-delete-canary") {
				t.Fatal("invalid deletion reached storage or exposed input", w.Code)
			}
		})
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{store.ErrNotFound, 404}, {errors.New("vault-delete-canary"), 500}} {
		h, f := vaultResourceHandler(t)
		f.err = tc.err
		w := vaultRequest(h, "DELETE", "/v1/vaults/"+f.vault.ID, "")
		if w.Code != tc.status || strings.Contains(w.Body.String(), "vault-delete-canary") {
			t.Fatal("deletion changed error mapping or exposed storage detail")
		}
	}
}
