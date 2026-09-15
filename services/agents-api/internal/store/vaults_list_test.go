package store

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVaultListFilteringPaginationAndReconnect(t *testing.T) {
	s, pool := testStore(t)
	ctx := t.Context()
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := s.ListVaults(ctx, tenant, "", 20, false, nil)
	if err != nil || empty.Vaults == nil || len(empty.Vaults) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}
	var all []Vault
	archived := map[string]bool{}
	for i := range 105 {
		vault, err := s.CreateVault(ctx, tenant, CreateVaultInput{Metadata: map[string]string{"purpose": "safe list fixture"}})
		if err != nil {
			t.Fatal(err)
		}
		status := "active"
		if i%3 == 0 {
			status, archived[vault.ID] = "archived", true
		}
		vault.CreatedAt = time.Unix(1700000000+int64(i%2), 0).UTC()
		// Synthetic classifications exercise reads, not a public archive lifecycle.
		if _, err := pool.Exec(ctx, "UPDATE vaults SET created_at=$1, status=$2 WHERE tenant_id=$3 AND id=$4", vault.CreatedAt, status, tenant, vault.ID); err != nil {
			t.Fatal(err)
		}
		vault, err = s.GetVault(ctx, tenant, vault.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, vault)
	}
	slices.SortFunc(all, func(a, b Vault) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	foreign, err := s.CreateVault(ctx, other, CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	read := func(s *Store, ascending bool, statuses []string, size int) []Vault {
		t.Helper()
		actual := []Vault{}
		cursor := ""
		for {
			page, err := s.ListVaults(ctx, tenant, cursor, size, ascending, statuses)
			if err != nil || len(page.Vaults) == 0 || len(page.Vaults) > size {
				t.Fatalf("page: %+v, %v", page, err)
			}
			actual = append(actual, page.Vaults...)
			if len(actual) > len(all) {
				t.Fatal("pagination repeated records")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Vaults[len(page.Vaults)-1].ID {
				t.Fatal("cursor is not the last included resource")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	for _, ascending := range []bool{true, false} {
		for _, statuses := range [][]string{nil, {"active"}, {"archived"}, {"active", "archived"}} {
			want := []Vault{}
			for _, vault := range all {
				if len(statuses) != 1 || archived[vault.ID] == (statuses[0] == "archived") {
					want = append(want, vault)
				}
			}
			if !ascending {
				slices.Reverse(want)
			}
			for _, size := range []int{20, 100} {
				if got := read(s, ascending, statuses, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("filtered ordering/projection mismatch: ascending=%t statuses=%v size=%d got=%d want=%d", ascending, statuses, size, len(got), len(want))
				}
			}
		}
	}
	for _, cursor := range []string{foreign.ID, uuid.NewString()} {
		if _, err := s.ListVaults(ctx, tenant, cursor, 20, true, []string{"archived"}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign/unknown cursor: %v", err)
		}
	}
	for _, tc := range []struct {
		tenant, cursor string
		limit          int
		statuses       []string
	}{{"invalid", "", 20, nil}, {tenant, "invalid", 20, nil}, {tenant, "", 0, nil}, {tenant, "", 101, nil}, {tenant, "", 20, []string{"deleted"}}} {
		if _, err := s.ListVaults(ctx, tc.tenant, tc.cursor, tc.limit, true, tc.statuses); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid store query: %v", err)
		}
	}
	tail, err := s.ListVaults(ctx, tenant, all[len(all)-1].ID, 100, true, nil)
	if err != nil || tail.Vaults == nil || len(tail.Vaults) != 0 || tail.NextCursor != "" {
		t.Fatalf("terminal page: %+v, %v", tail, err)
	}
	page, err := s.ListVaults(ctx, other, "", 100, false, nil)
	if err != nil || !reflect.DeepEqual(page.Vaults, []Vault{foreign}) || page.NextCursor != "" {
		t.Fatalf("project isolation: %+v, %v", page, err)
	}
	pool.Close()
	reopened, _ := testStore(t)
	if got := read(reopened, true, nil, 20); !reflect.DeepEqual(got, all) {
		t.Fatal("listing changed after reconnect")
	}
}
