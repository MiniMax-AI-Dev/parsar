package api

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (f *credentialFixture) ListCredentials(_ context.Context, tenant, vault, after string, limit int, ascending bool, statuses []string) (store.CredentialPage, error) {
	f.tenant, f.vault, f.calls = tenant, vault, f.calls+1
	f.options, f.statuses = pageOptions{after: after, limit: limit, ascending: ascending}, statuses
	return f.page, f.err
}

func TestCredentialListScopeProjectionAndParameters(t *testing.T) {
	for _, tc := range []struct {
		query    string
		limit    int
		statuses []string
	}{
		{"", 20, nil}, {"?limit=0", 1, nil}, {"?limit=101", 100, nil},
		{"?status=archived", 20, []string{"archived"}},
		{"?status[]=active&status[]=archived", 20, []string{"active", "archived"}},
	} {
		h, f, tenant := credentialHandler(t)
		f.page = store.CredentialPage{Credentials: []store.Credential{f.credential}, NextCursor: f.credential.ID}
		w := credentialRequest(h, "GET", "/v1/vaults/"+f.credential.VaultID+"/credentials"+tc.query, "")
		var body v1.CredentialList
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if f.calls != 1 || f.tenant != tenant || f.vault != f.credential.VaultID || f.options.limit != tc.limit || !reflect.DeepEqual(f.statuses, tc.statuses) {
			t.Fatal("list scope or parameters changed")
		}
		if !reflect.DeepEqual(body.Data, []v1.Credential{credentialResponse(f.credential)}) || !body.HasMore || body.FirstID == nil || *body.FirstID != f.credential.ID || body.LastID == nil || *body.LastID != f.credential.ID {
			t.Fatal("list projection or cursor changed")
		}
	}
	h, f, _ := credentialHandler(t)
	path := "/v1/vaults/" + f.credential.VaultID + "/credentials"
	w := credentialRequest(h, "GET", path+"?order=asc&after="+f.credential.ID, "")
	var empty map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &empty) != nil || !reflect.DeepEqual(empty, map[string]any{"object": "list", "data": []any{}, "has_more": false, "first_id": nil, "last_id": nil}) || !f.options.ascending || f.options.after != f.credential.ID {
		t.Fatal("empty page or cursor parsing changed")
	}
	f.err = store.ErrNotFound
	if w = credentialRequest(h, "GET", path, ""); w.Code != 404 {
		t.Fatal("missing parent must not be an empty collection")
	}
}

func TestCredentialListRejectsInvalidInputBeforeStorage(t *testing.T) {
	for _, suffix := range []string{"?status=deleted", "?status=active&status[]=archived", "?limit=1.5", "?limit=1&limit=2", "?tenant_id=foreign"} {
		h, f, _ := credentialHandler(t)
		w := credentialRequest(h, "GET", "/v1/vaults/"+f.credential.VaultID+"/credentials"+suffix, "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatal("invalid query reached storage", suffix, w.Code)
		}
	}
	h, f, _ := credentialHandler(t)
	if w := credentialRequest(h, "GET", "/v1/vaults/invalid/credentials", ""); w.Code != 404 || f.calls != 0 {
		t.Fatal("invalid parent reached storage")
	}
}
