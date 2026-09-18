package store

import (
	"cmp"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSourceFileListPaginationIsolationAndReconnect(t *testing.T) {
	s, pool := testStore(t)
	tenant, other := uuid.NewString(), uuid.NewString()
	empty, err := s.ListSourceFiles(t.Context(), tenant, "", 10000, false, nil)
	if err != nil || empty.Files == nil || len(empty.Files) != 0 || empty.NextCursor != "" {
		t.Fatalf("empty page: %+v, %v", empty, err)
	}

	all := make([]SourceFile, 0, 105)
	for i := range 105 {
		file, err := s.CreateSourceFile(t.Context(), tenant, uploadSource([]byte{byte(i)}))
		if err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1700000000+int64(i%2), 0).UTC()
		if _, err := pool.Exec(t.Context(), "UPDATE source_files SET created_at=$1 WHERE tenant_id=$2 AND id=$3", stamp, tenant, strings.TrimPrefix(file.ID, "file-")); err != nil {
			t.Fatal(err)
		}
		file, err = s.GetSourceFile(t.Context(), tenant, file.ID)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, file)
	}
	slices.SortFunc(all, func(a, b SourceFile) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	foreign, err := s.CreateSourceFile(t.Context(), other, uploadSource([]byte("foreign")))
	if err != nil {
		t.Fatal(err)
	}

	read := func(current *Store, ascending bool, purpose *string, size int) []SourceFile {
		t.Helper()
		actual := []SourceFile{}
		cursor := ""
		for {
			page, err := current.ListSourceFiles(t.Context(), tenant, cursor, size, ascending, purpose)
			if err != nil || len(page.Files) == 0 || len(page.Files) > size {
				t.Fatalf("page: %+v, %v", page, err)
			}
			actual = append(actual, page.Files...)
			if len(actual) > len(all) {
				t.Fatal("pagination repeated files")
			}
			if page.NextCursor == "" {
				break
			}
			if page.NextCursor != page.Files[len(page.Files)-1].ID {
				t.Fatal("cursor is not the last included file")
			}
			cursor = page.NextCursor
		}
		return actual
	}
	userData, otherPurpose := "user_data", "batch"
	for _, ascending := range []bool{true, false} {
		want := slices.Clone(all)
		if !ascending {
			slices.Reverse(want)
		}
		for _, purpose := range []*string{nil, &userData} {
			for _, size := range []int{17, 10000} {
				if got := read(s, ascending, purpose, size); !reflect.DeepEqual(got, want) {
					t.Fatalf("ordered page mismatch: ascending=%t size=%d", ascending, size)
				}
			}
		}
	}
	filtered, err := s.ListSourceFiles(t.Context(), tenant, "", 10000, false, &otherPurpose)
	if err != nil || filtered.Files == nil || len(filtered.Files) != 0 || filtered.NextCursor != "" {
		t.Fatalf("purpose filter: %+v, %v", filtered, err)
	}
	for _, cursor := range []string{foreign.ID, "file-" + uuid.NewString(), "invalid"} {
		if _, err := s.ListSourceFiles(t.Context(), tenant, cursor, 20, true, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign/unknown cursor %q: %v", cursor, err)
		}
	}
	for _, tc := range []struct {
		tenant  string
		limit   int
		purpose *string
	}{{"invalid", 20, nil}, {tenant, 0, nil}, {tenant, 10001, nil}, {tenant, 20, sourceFilePurposePtr("bad\x00purpose")}} {
		if _, err := s.ListSourceFiles(t.Context(), tc.tenant, "", tc.limit, true, tc.purpose); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("invalid store query: %v", err)
		}
	}
	tail, err := s.ListSourceFiles(t.Context(), tenant, all[len(all)-1].ID, 10000, true, nil)
	if err != nil || tail.Files == nil || len(tail.Files) != 0 || tail.NextCursor != "" {
		t.Fatalf("terminal page: %+v, %v", tail, err)
	}
	foreignPage, err := s.ListSourceFiles(t.Context(), other, "", 10000, false, nil)
	if err != nil || !reflect.DeepEqual(foreignPage.Files, []SourceFile{foreign}) || foreignPage.NextCursor != "" {
		t.Fatalf("project isolation: %+v, %v", foreignPage, err)
	}
	deleted, err := s.CreateSourceFile(t.Context(), tenant, uploadSource(nil))
	if err != nil || s.DeleteSourceFile(t.Context(), tenant, deleted.ID) != nil {
		t.Fatal(err)
	}
	if _, err := s.ListSourceFiles(t.Context(), tenant, deleted.ID, 20, false, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted cursor accepted: %v", err)
	}

	pool.Close()
	reopened, _ := testStore(t)
	if got := read(reopened, true, nil, 17); !reflect.DeepEqual(got, all) {
		t.Fatal("listing changed after reconnect")
	}
}

func sourceFilePurposePtr(value string) *string { return &value }
