package store

import (
	"context"
	"strings"
	"testing"
)

func TestListWorkspaceAgentRunsFiltersByStatus(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	store := New(db)
	ids := mustSeedDevFixture(t, ctx, store)

	created, err := store.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
		ConversationTitle: "Demo Group",
		SenderEmail:       "admin@example.com",
		Text:              "@product-agent @backend-agent evaluate the API",
		Mentions:          []string{"@product-agent", "@backend-agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: created.RunIDs[0], Content: "product agent output"}); err != nil {
		t.Fatal(err)
	}

	queued, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"queued"}, 100, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued.Runs) != 1 || queued.Runs[0].Status != "queued" {
		t.Fatalf("expected one queued run, got %+v", queued.Runs)
	}
	if queued.Total != 1 {
		t.Fatalf("expected total=1 for queued filter, got %d", queued.Total)
	}

	completed, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"completed"}, 100, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Runs) != 1 || completed.Runs[0].Status != "completed" {
		t.Fatalf("expected one completed run, got %+v", completed.Runs)
	}

	// Union filter (running ∨ queued) keeps queued only; completed is excluded.
	union, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, []string{"running", "queued"}, 100, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(union.Runs) != 1 || union.Runs[0].Status != "queued" {
		t.Fatalf("expected union to keep queued only, got %+v", union.Runs)
	}
	if union.Total != 1 {
		t.Fatalf("expected union total=1, got %d", union.Total)
	}

	// nil statuses = no filter; verify DESC ordering and pagination.
	first, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 1, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Runs) != 1 {
		t.Fatalf("expected one row at offset=0, got %d", len(first.Runs))
	}
	if first.Total != 2 {
		t.Fatalf("expected total=2 across both rows, got %d", first.Total)
	}
	second, err := store.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 1, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Runs) != 1 {
		t.Fatalf("expected one row at offset=1, got %d", len(second.Runs))
	}
	if first.Runs[0].ID == second.Runs[0].ID {
		t.Fatalf("offset=0 and offset=1 returned the same row, paging is broken")
	}
	// Both rows share the same trigger message so created_at may tie;
	// id DESC tie-break still gives a stable order.
	if first.Runs[0].CreatedAt.Before(second.Runs[0].CreatedAt) {
		t.Fatalf("expected DESC order, got first=%s before second=%s", first.Runs[0].CreatedAt, second.Runs[0].CreatedAt)
	}
}

func TestListWorkspaceAgentRunsSearchBeforePagination(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	st := New(db)
	ids := mustSeedDevFixture(t, ctx, st)
	create := func(mention string) CreateInboundIMMessageResult {
		t.Helper()
		result, err := st.CreateInboundIMMessage(ctx, CreateInboundIMMessageInput{
			ConversationTitle: "Demo Group", SenderEmail: "admin@example.com",
			Text: mention + " search regression", Mentions: []string{mention},
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	oldest := create("@product-agent")
	if _, err := st.CompleteAgentRun(ctx, CompleteAgentRunInput{RunID: oldest.RunIDs[0], Content: "done"}); err != nil {
		t.Fatal(err)
	}
	for range 24 {
		create("@backend-agent")
	}
	page, err := st.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, nil, 20, 0, "")
	if err != nil || page.Total != 25 || len(page.Runs) != 20 {
		t.Fatalf("unfiltered page: %+v, %v", page, err)
	}
	for _, run := range page.Runs {
		if run.ID == oldest.RunIDs[0] {
			t.Fatal("fixture target must be beyond the first page")
		}
	}
	for _, tc := range []struct {
		name, search string
		statuses     []string
		offset       int32
		total        int64
		rows         int
	}{
		{"old run ID", oldest.RunIDs[0], nil, 0, 1, 1},
		{"uppercase run ID", strings.ToUpper(oldest.RunIDs[0]), nil, 0, 1, 1},
		{"Agent name", "  PRODUCT AGENT  ", nil, 0, 1, 1},
		{"Agent slug", "BACKEND-AGENT", nil, 20, 24, 4},
		{"conversation ID", oldest.ConversationID, nil, 0, 25, 20},
		{"matching status", oldest.RunIDs[0], []string{"completed"}, 0, 1, 1},
		{"excluded status", oldest.RunIDs[0], []string{"queued", "running"}, 0, 0, 0},
		{"literal percent", "%", nil, 0, 0, 0},
		{"literal underscore", "_", nil, 0, 0, 0},
		{"no match", "unknown-run", nil, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := st.ListWorkspaceAgentRuns(ctx, ids.WorkspaceID, tc.statuses, 20, tc.offset, tc.search)
			if err != nil || result.Total != tc.total || len(result.Runs) != tc.rows {
				t.Fatalf("search %q: total=%d rows=%d error=%v", tc.search, result.Total, len(result.Runs), err)
			}
			if tc.total == 1 && result.Runs[0].ID != oldest.RunIDs[0] {
				t.Fatalf("wrong search result: %s", result.Runs[0].ID)
			}
		})
	}
	other, err := st.CreateWorkspace(ctx, CreateWorkspaceInput{Name: "Other workspace", CreatedBy: ids.UserID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := st.ListWorkspaceAgentRuns(ctx, other.Workspace.ID, nil, 20, 0, oldest.RunIDs[0])
	if err != nil || result.Total != 0 || len(result.Runs) != 0 {
		t.Fatalf("cross-workspace search: %+v, %v", result, err)
	}
}
