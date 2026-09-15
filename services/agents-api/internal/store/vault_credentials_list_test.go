package store

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/google/uuid"
)

func TestCredentialListFilteringOwnershipAndKeylessReconnect(t *testing.T) {
	reader, pool := testStore(t)
	ctx := t.Context()
	tenant, foreign := uuid.NewString(), uuid.NewString()
	var vaults []Vault
	for _, owner := range []string{tenant, tenant, foreign, tenant} {
		vault, err := reader.CreateVault(ctx, owner, CreateVaultInput{})
		if err != nil {
			t.Fatal(err)
		}
		vaults = append(vaults, vault)
	}
	// Parent classification does not classify its Credentials.
	if _, err := pool.Exec(ctx, "UPDATE vaults SET status='archived' WHERE id=$1", vaults[0].ID); err != nil {
		t.Fatal(err)
	}
	cipher, err := credentialcrypto.New(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	writer := NewWithCredentialCipher(pool, cipher)
	create := func(owner, vault string) Credential {
		t.Helper()
		c, err := writer.CreateStaticCredential(ctx, owner, vault, CreateStaticCredentialInput{Name: "List fixture", MCPServerURL: "https://example.invalid/mcp", Token: "synthetic-token-not-public"})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var all []Credential
	archived := map[string]bool{}
	for i := range 105 {
		c := create(tenant, vaults[0].ID)
		status := "active"
		if i%3 == 0 {
			status, archived[c.ID] = "archived", true
		}
		if _, err := pool.Exec(ctx, "UPDATE vault_credentials SET status=$1, created_at=$2 WHERE id=$3", status, time.Unix(1700000000+int64(i%2), 0), c.ID); err != nil {
			t.Fatal(err)
		}
		c, err = reader.GetCredential(ctx, tenant, vaults[0].ID, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, c)
	}
	slices.SortFunc(all, func(a, b Credential) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	otherVault, otherProject := create(tenant, vaults[1].ID), create(foreign, vaults[2].ID)
	snapshot := func(s *Store) string {
		t.Helper()
		var value string
		if err := s.pool.QueryRow(ctx, "SELECT jsonb_agg(to_jsonb(c) ORDER BY id)::text FROM vault_credentials c WHERE vault_id=$1", vaults[0].ID).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot(reader)
	read := func(s *Store, ascending bool, statuses []string, size int) []Credential {
		t.Helper()
		actual := []Credential{}
		cursor := ""
		for {
			page, err := s.ListCredentials(ctx, tenant, vaults[0].ID, cursor, size, ascending, statuses)
			if err != nil || len(page.Credentials) == 0 || len(page.Credentials) > size {
				t.Fatal("invalid page", err)
			}
			actual = append(actual, page.Credentials...)
			if len(actual) > len(all) {
				t.Fatal("repeated pagination")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Credentials[len(page.Credentials)-1].ID {
				t.Fatal("cursor is not last included Credential")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	for _, ascending := range []bool{true, false} {
		for _, statuses := range [][]string{nil, {"active"}, {"archived"}, {"active", "archived"}} {
			want := []Credential{}
			for _, c := range all {
				if len(statuses) != 1 || archived[c.ID] == (statuses[0] == "archived") {
					want = append(want, c)
				}
			}
			if !ascending {
				slices.Reverse(want)
			}
			for _, size := range []int{20, 100} {
				if got := read(reader, ascending, statuses, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("metadata/filter/order mismatch: ascending=%t statuses=%v size=%d", ascending, statuses, size)
				}
			}
		}
	}
	for _, tc := range []struct{ owner, vault, cursor string }{
		{tenant, vaults[0].ID, otherVault.ID}, {tenant, vaults[0].ID, otherProject.ID}, {tenant, vaults[0].ID, uuid.NewString()},
		{tenant, vaults[2].ID, ""}, {foreign, vaults[0].ID, ""}, {tenant, uuid.NewString(), ""},
	} {
		if _, err := reader.ListCredentials(ctx, tc.owner, tc.vault, tc.cursor, 20, false, nil); !errors.Is(err, ErrNotFound) {
			t.Fatal("unowned/unknown parent or cursor accepted", err)
		}
	}
	for _, tc := range []struct{ vault, cursor string }{{vaults[0].ID, all[len(all)-1].ID}, {vaults[3].ID, ""}} {
		page, err := reader.ListCredentials(ctx, tenant, tc.vault, tc.cursor, 20, true, nil)
		if err != nil || page.Credentials == nil || len(page.Credentials) != 0 || page.NextCursor != "" {
			t.Fatal("empty/terminal page", err)
		}
	}
	for _, tc := range []struct {
		owner, vault, cursor string
		limit                int
		statuses             []string
	}{{"invalid", vaults[0].ID, "", 20, nil}, {tenant, "invalid", "", 20, nil}, {tenant, vaults[0].ID, "invalid", 20, nil}, {tenant, vaults[0].ID, "", 0, nil}, {tenant, vaults[0].ID, "", 101, nil}, {tenant, vaults[0].ID, "", 20, []string{"deleted"}}} {
		if _, err := reader.ListCredentials(ctx, tc.owner, tc.vault, tc.cursor, tc.limit, false, tc.statuses); !errors.Is(err, ErrInvalidInput) {
			t.Fatal("invalid internal query accepted", err)
		}
	}
	pool.Close()
	reopened, _ := testStore(t)
	if got := read(reopened, true, nil, 20); !reflect.DeepEqual(got, all) || snapshot(reopened) != before {
		t.Fatal("keyless reads/restart changed metadata, classification or ciphertext")
	}
}
