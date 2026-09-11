package store

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTurnPaginationRetainsScopeAndOrder(t *testing.T) {
	s, pool := testStore(t)
	tenant, session := newTurnSession(t, s)
	ctx := context.Background()
	ids := make([]string, 0, 4)
	for i := 0; i < 4; i++ {
		receipt := submitMessage(t, s, tenant, session.ID, uuid.NewString())
		transition(t, s, tenant, session.ID, receipt.TurnID, TurnQueued, TurnCancelled)
		ids = append(ids, receipt.TurnID)
	}
	// Equal creation times exercise the ID tie-breaker across page boundaries.
	if _, err := pool.Exec(ctx, "UPDATE turns SET created_at=$1 WHERE session_id=$2", time.Unix(1700000000, 0), session.ID); err != nil {
		t.Fatal(err)
	}
	sort.Strings(ids)
	s = New(pool)
	for _, ascending := range []bool{true, false} {
		cursor := ""
		for i := 0; i < len(ids); i++ {
			page, err := s.ListTurns(ctx, tenant, session.ID, cursor, 1, ascending)
			at := i
			if !ascending {
				at = len(ids) - 1 - i
			}
			if err != nil || len(page.Turns) != 1 || page.Turns[0].ID != ids[at] {
				t.Fatalf("page %d: %+v %v", i, page, err)
			}
			if (page.NextCursor != "") != (i < len(ids)-1) {
				t.Fatalf("incorrect has_more: %+v", page)
			}
			cursor = page.Turns[0].ID
		}
		page, err := s.ListTurns(ctx, tenant, session.ID, cursor, 1, ascending)
		if err != nil || len(page.Turns) != 0 || page.Turns == nil || page.NextCursor != "" {
			t.Fatalf("end: %+v %v", page, err)
		}
	}
	otherTenant, otherSession := newTurnSession(t, s)
	sameTenantSession, err := s.CreateSession(ctx, tenant, CreateSessionInput{Engine: "codex", IdempotencyKey: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][2]string{{otherTenant, session.ID}, {tenant, otherSession.ID}, {tenant, uuid.NewString()}, {tenant, sameTenantSession.ID}} {
		_, err := s.ListTurns(ctx, scope[0], scope[1], ids[0], 1, true)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign cursor/session accepted: %v", err)
		}
	}
	empty, err := s.ListTurns(ctx, tenant, sameTenantSession.ID, "", 20, false)
	if err != nil || empty.Turns == nil || len(empty.Turns) != 0 {
		t.Fatalf("empty session: %+v %v", empty, err)
	}
	for _, limit := range []int{0, 101} {
		if _, err := s.ListTurns(ctx, tenant, session.ID, "", limit, false); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit accepted: %v", err)
		}
	}
}
