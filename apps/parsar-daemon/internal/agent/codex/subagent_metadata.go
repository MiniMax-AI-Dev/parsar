package codex

import (
	"context"
	"encoding/json"
	"errors"
)

type subagentMetadata struct {
	ID             string          `json:"id"`
	ParentThreadID string          `json:"parentThreadId"`
	CreatedAt      int64           `json:"createdAt"`
	Source         json.RawMessage `json:"source"`
}

// Parent-filtered listing reads persisted metadata. thread/read may instead
// synthesize createdAt from a live snapshot before the first persistence flush.
func (s *Session) persistedSubagent(ctx context.Context, child, parent string, budget *int) (subagentMetadata, bool, error) {
	var cursor *string
	for range 4 {
		if *budget == 0 {
			return subagentMetadata{}, false, errors.New("metadata_query_budget")
		}
		*budget -= 1
		params := struct {
			ParentThreadID string   `json:"parentThreadId"`
			UseStateDBOnly bool     `json:"useStateDbOnly"`
			ModelProviders []string `json:"modelProviders"`
			SortKey        string   `json:"sortKey"`
			SortDirection  string   `json:"sortDirection"`
			Limit          int      `json:"limit"`
			Cursor         *string  `json:"cursor,omitempty"`
		}{parent, true, []string{}, "created_at", "desc", 100, cursor}
		raw, err := s.rpc.requestWithTimeout(ctx, "thread/list", params, func(frame any) error {
			return s.rpc.writeFrameContext(ctx, frame)
		}, 0, nil)
		if err != nil {
			return subagentMetadata{}, false, errors.New("metadata_lookup_unavailable")
		}
		var page struct {
			Data       []subagentMetadata `json:"data"`
			NextCursor *string            `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &page) != nil {
			return subagentMetadata{}, false, errors.New("metadata_invalid")
		}
		for _, row := range page.Data {
			if row.ID != child {
				continue
			}
			var source struct {
				SubAgent struct {
					Spawn struct {
						Parent string `json:"parent_thread_id"`
					} `json:"thread_spawn"`
				} `json:"subAgent"`
			}
			if json.Unmarshal(row.Source, &source) != nil || row.ParentThreadID != parent || source.SubAgent.Spawn.Parent != parent || row.CreatedAt <= 0 {
				return subagentMetadata{}, false, errors.New("metadata_parent_or_creation_invalid")
			}
			return row, true, nil
		}
		if page.NextCursor == nil {
			return subagentMetadata{}, false, nil
		}
		cursor = page.NextCursor
	}
	return subagentMetadata{}, false, errors.New("metadata_page_budget")
}
