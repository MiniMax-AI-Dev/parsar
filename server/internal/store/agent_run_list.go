package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// ListWorkspaceAgentRunsResult bundles a page of agent_run rows with the
// total row count under the same filter.
type ListWorkspaceAgentRunsResult struct {
	Runs  []AgentRunBriefRead
	Total int64
}

// ListWorkspaceAgentRuns returns a page of agent runs for an active workspace,
// newest first. `statuses` is an OR filter (nil/empty means no filter).
func (s *Store) ListWorkspaceAgentRuns(ctx context.Context, workspaceID string, statuses []string, limit, offset int32, search string) (ListWorkspaceAgentRunsResult, error) {
	if limit <= 0 {
		limit = defaultReadLimit
	}
	if offset < 0 {
		offset = 0
	}
	queries := sqlc.New(s.db)
	workspaceUUID, err := uuid(workspaceID)
	if err != nil {
		return ListWorkspaceAgentRunsResult{}, err
	}

	exists, err := queries.ActiveWorkspaceExists(ctx, workspaceUUID)
	if err != nil {
		return ListWorkspaceAgentRunsResult{}, err
	}
	if !exists {
		return ListWorkspaceAgentRunsResult{}, fmt.Errorf("%w: %s", ErrUnknownWorkspace, workspaceID)
	}

	// sqlc + pgx/v5 treat a nil []string as NULL, which would break
	// `cardinality(NULL::text[]) = 0`. Normalise to an empty slice so
	// the "no filter" branch always evaluates to 0 cardinality.
	if statuses == nil {
		statuses = []string{}
	}

	rows, err := queries.ListWorkspaceAgentRunsPage(ctx, sqlc.ListWorkspaceAgentRunsPageParams{
		WorkspaceID: workspaceUUID,
		Statuses:    statuses,
		Search:      strings.TrimSpace(search),
		ItemOffset:  offset,
		ItemLimit:   limit,
	})
	if err != nil {
		return ListWorkspaceAgentRunsResult{}, err
	}
	total, err := queries.CountWorkspaceAgentRuns(ctx, sqlc.CountWorkspaceAgentRunsParams{
		WorkspaceID: workspaceUUID,
		Statuses:    statuses,
		Search:      strings.TrimSpace(search),
	})
	if err != nil {
		return ListWorkspaceAgentRunsResult{}, err
	}

	runs := make([]AgentRunBriefRead, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, agentRunBriefFromWorkspacePageRow(row))
	}
	return ListWorkspaceAgentRunsResult{Runs: runs, Total: total}, nil
}
