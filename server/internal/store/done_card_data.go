package store

// done_card_data.go owns the read-side helpers the IM outbound path needs
// to assemble the terminal DoneCard footer: elapsed, the latest usage_log
// row for the run, and the cumulative cost across the run.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	sqlc "github.com/MiniMax-AI-Dev/parsar/server/internal/db/sqlc"
)

// DoneCardRunData is everything the DoneCard footer needs about a run.
// All fields degrade gracefully: an empty Model + zero ContextUsed
// causes the renderer to fall back to the short `Ns · N steps` line.
type DoneCardRunData struct {
	// FinishedAt may be zero when the row is still in flight (caller
	// falls back to time.Now()).
	StartedAt  time.Time
	FinishedAt time.Time
	// Model is the LAST usage_log row's model — the one used on the next
	// turn. Multi-model runs surface only the most recent.
	Model string
	// ContextUsedTokens is the LAST usage_log row's total prompt
	// occupancy: input + cache_read + cache_creation + output. cache_*
	// fields are pulled from the raw jsonb (apps/parsar-daemon/.../parser.go
	// writes them into usage.Raw).
	//
	// A model's "context window" is the size of the PROMPT it can
	// accept, not new tokens minus cached. cache_read still occupies
	// prompt positions; skipping it reports e.g. 169 tokens for a real
	// ~30k-token turn and renders as 0%.
	ContextUsedTokens int
	// CostUSD is the SUM over every usage_log row in the run.
	CostUSD float64
	// HasUsage flips true when at least one usage_log row exists for
	// this run — distinguishes "run too fast for usage to land" from
	// "genuinely empty".
	HasUsage bool
	// AgentName is the display name of the Agent bound to this run.
	// Empty when the binding is missing/soft-deleted; the renderer
	// falls back to the brand title.
	AgentName string
}

// LoadDoneCardRunData fetches everything the DoneCard footer needs.
// workspaceID is required for multi-tenant safety on ListUsageLogsByRun.
// An empty runID returns a zero DoneCardRunData with no error so the
// caller can render a best-effort fallback.
func (s *Store) LoadDoneCardRunData(ctx context.Context, workspaceID, runID string) (DoneCardRunData, error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return DoneCardRunData{}, nil
	}
	runUUID, err := uuid(runID)
	if err != nil {
		return DoneCardRunData{}, err
	}
	queries := sqlc.New(s.db)

	// ErrNoRows here means a bogus runID; treat as "no data" and let
	// the footer fall back rather than crashing the card path.
	runRow, err := queries.GetAgentRunForRead(ctx, runUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return DoneCardRunData{}, nil
		}
		return DoneCardRunData{}, fmt.Errorf("LoadDoneCardRunData: get run: %w", err)
	}
	out := DoneCardRunData{
		StartedAt:  pgTime(runRow.StartedAt),
		FinishedAt: pgTime(runRow.FinishedAt),
		AgentName:  strings.TrimSpace(runRow.AgentName),
	}

	wsUUID, err := uuid(workspaceID)
	if err != nil {
		return DoneCardRunData{}, err
	}
	usageRows, err := queries.ListUsageLogsByRun(ctx, sqlc.ListUsageLogsByRunParams{
		AgentRunID:  runUUID,
		WorkspaceID: wsUUID,
		ItemLimit:   200,
	})
	if err != nil {
		return DoneCardRunData{}, fmt.Errorf("LoadDoneCardRunData: list usage: %w", err)
	}
	for i, row := range usageRows {
		out.HasUsage = true
		out.CostUSD += numericFloat64(row.CostUsd)
		// ORDER BY created_at DESC — first row is the most recent turn.
		if i == 0 {
			out.Model = row.Model
			cacheRead, cacheCreation := cacheTokensFromRaw(row.Raw)
			out.ContextUsedTokens = int(row.InputTokens) + int(row.OutputTokens) + cacheRead + cacheCreation
		}
	}
	return out, nil
}

// cacheTokensFromRaw extracts prompt-cache token counts the daemon
// stamps into usage_logs.raw. The daemon only sets these keys when at
// least one is non-zero, so absence means "no cache used". Returns
// (0, 0) on empty / malformed raw — the footer is opportunistic.
// Returns (cache_read, cache_creation) in tokens.
func cacheTokensFromRaw(raw []byte) (int, int) {
	if len(raw) == 0 {
		return 0, 0
	}
	var blob map[string]any
	if err := json.Unmarshal(raw, &blob); err != nil {
		return 0, 0
	}
	return readIntKey(blob, "cache_read_input_tokens"), readIntKey(blob, "cache_creation_input_tokens")
}

// readIntKey decodes a key that may be encoded as a JSON number
// (float64 after Unmarshal into any) or int. Returns 0 on any mismatch.
func readIntKey(blob map[string]any, key string) int {
	if blob == nil {
		return 0
	}
	raw, ok := blob[key]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case float64:
		if v < 0 {
			return 0
		}
		return int(v)
	case int:
		if v < 0 {
			return 0
		}
		return v
	case int32:
		if v < 0 {
			return 0
		}
		return int(v)
	case int64:
		if v < 0 {
			return 0
		}
		return int(v)
	}
	return 0
}
