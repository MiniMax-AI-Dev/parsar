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

func (f *credentialFixture) DeleteCredential(_ context.Context, tenant, vault, id string) (string, error) {
	f.tenant, f.vault, f.id, f.calls = tenant, vault, id, f.calls+1
	return f.credential.ID, f.err
}

func TestCredentialDeletionConfirmationAndScope(t *testing.T) {
	h, f, tenant := credentialHandler(t)
	w := credentialRequest(h, "DELETE", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, "")
	var got map[string]any
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal("deletion failed", w.Code)
	}
	want := map[string]any{"id": f.credential.ID, "deleted": true, "object": "vault.credential.deleted"}
	if !reflect.DeepEqual(got, want) || f.tenant != tenant || f.vault != f.credential.VaultID || f.id != f.credential.ID || f.calls != 1 {
		t.Fatal("deletion changed authenticated scope or confirmation")
	}
}

func TestCredentialDeletionRejectsBeforeMutation(t *testing.T) {
	for _, mode := range []string{"auth", "beta", "query", "body", "null", "vault", "credential", "zero"} {
		t.Run(mode, func(t *testing.T) {
			h, f, _ := credentialHandler(t)
			path := "/v1/vaults/" + f.credential.VaultID + "/credentials/" + f.credential.ID
			body, status := "", http.StatusBadRequest
			switch mode {
			case "auth":
				status = http.StatusUnauthorized
			case "query":
				path += "?tenant_id=untrusted"
			case "body":
				body = `{"token":"credential-canary"}`
			case "null":
				body = "null"
			case "vault":
				path, status = strings.Replace(path, f.credential.VaultID, "invalid", 1), http.StatusNotFound
			case "credential":
				path, status = strings.Replace(path, f.credential.ID, "invalid", 1), http.StatusNotFound
			case "zero":
				path, status = strings.Replace(path, f.credential.ID, uuid.Nil.String(), 1), http.StatusNotFound
			}
			r := httptest.NewRequest("DELETE", path, strings.NewReader(body))
			if mode != "auth" {
				r.Header.Set("Authorization", "Bearer test-api-key")
			}
			if mode != "beta" {
				r.Header.Set("OpenAI-Beta", "agents=v1")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != status || f.calls != 0 || strings.Contains(w.Body.String(), "credential-canary") {
				t.Fatal("invalid deletion reached storage or disclosed input", w.Code)
			}
		})
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{store.ErrNotFound, 404}, {errors.New("credential-canary"), 500}} {
		h, f, _ := credentialHandler(t)
		f.err = tc.err
		w := credentialRequest(h, "DELETE", "/v1/vaults/"+f.credential.VaultID+"/credentials/"+f.credential.ID, "")
		if w.Code != tc.status || strings.Contains(w.Body.String(), "credential-canary") {
			t.Fatal("deletion changed error mapping or disclosed storage detail")
		}
	}
}
