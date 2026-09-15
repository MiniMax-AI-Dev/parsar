package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func (f *vaultResourceFixture) ListVaults(_ context.Context, tenant, after string, limit int, ascending bool, statuses []string) (store.VaultPage, error) {
	f.tenant, f.calls = tenant, f.calls+1
	f.options, f.statuses = pageOptions{after: after, limit: limit, ascending: ascending}, statuses
	return f.page, f.err
}

func TestVaultListParameters(t *testing.T) {
	for _, tc := range []struct {
		query    string
		limit    int
		statuses []string
	}{
		{"", 20, nil}, {"?limit=0", 1, nil}, {"?limit=-8", 1, nil}, {"?limit=3", 3, nil},
		{"?limit=101", 100, nil}, {"?limit=9999999999999999999999999", 100, nil},
		{"?limit=-9999999999999999999999999", 1, nil},
		{"?status=active", 20, []string{"active"}}, {"?status=archived", 20, []string{"archived"}},
		{"?status[]=archived&status[]=active", 20, []string{"archived", "active"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			h, f := vaultResourceHandler(t)
			w := vaultRequest(h, http.MethodGet, "/v1/vaults"+tc.query, "")
			if w.Code != 200 || f.calls != 1 || f.tenant != f.vault.TenantID || f.options.limit != tc.limit || !reflect.DeepEqual(f.statuses, tc.statuses) {
				t.Fatalf("list: %d %s; options %+v statuses %v owner %s", w.Code, w.Body.String(), f.options, f.statuses, f.tenant)
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"object": "list", "data": []any{}, "has_more": false, "first_id": nil, "last_id": nil}
			if !reflect.DeepEqual(body, want) {
				t.Fatalf("empty envelope: %s", w.Body.String())
			}
		})
	}
	for _, query := range []string{"limit=", "limit=null", "limit=1.5", "limit=1&limit=2", "order=invalid", "status=", "status=deleted", "status[]=active&status[]=invalid", "status=active&status=archived", "status=active&status[]=archived", "tenant_id=foreign", "after=a&after=b"} {
		h, f := vaultResourceHandler(t)
		w := vaultRequest(h, http.MethodGet, "/v1/vaults?"+query, "")
		if w.Code != 400 || f.calls != 0 {
			t.Fatalf("invalid %s: %d %s calls=%d", query, w.Code, w.Body.String(), f.calls)
		}
	}
}

func TestVaultListSafeProjectionAndCursor(t *testing.T) {
	h, f := vaultResourceHandler(t)
	f.page = store.VaultPage{Vaults: []store.Vault{f.vault}, NextCursor: f.vault.ID}
	w := vaultRequest(h, http.MethodGet, "/v1/vaults?order=asc&after="+f.vault.ID, "")
	var body v1.VaultList
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != 200 {
		t.Fatal(w.Code, w.Body.String(), err)
	}
	if !reflect.DeepEqual(body.Data, []v1.Vault{vaultResponse(f.vault)}) || !body.HasMore || body.FirstID == nil || *body.FirstID != f.vault.ID || body.LastID == nil || *body.LastID != f.vault.ID || !f.options.ascending || f.options.after != f.vault.ID {
		t.Fatalf("page changed: %+v, %+v", body, f.options)
	}
	f.err = store.ErrNotFound
	if w = vaultRequest(h, http.MethodGet, "/v1/vaults?after="+f.vault.ID, ""); w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
}
