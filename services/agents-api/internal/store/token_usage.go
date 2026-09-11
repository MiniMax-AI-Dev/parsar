package store

import (
	"context"
	"encoding/json"
	v1 "github.com/MiniMax-AI-Dev/parsar/contracts/agents-api/v1"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/db/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
	"math"
	"strings"
)

func projectSource(ctx context.Context, q *sqlc.Queries, session, turn pgtype.UUID, kind string, sequence int64, raw json.RawMessage, created pgtype.Timestamptz) error {
	if usage := measuredUsage(kind, raw); usage != nil {
		payload, err := json.Marshal(usage)
		if err != nil {
			return err
		}
		if err = q.PutTurnUsage(ctx, sqlc.PutTurnUsageParams{SessionID: session, ID: turn, TokenUsage: payload}); err != nil {
			return err
		}
	}
	return projectItemSource(ctx, q, session, turn, kind, sequence, raw, created)
}

func measuredUsage(kind string, raw json.RawMessage) *v1.TokenUsage {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	if strings.HasPrefix(kind, "execution_") {
		if json.Unmarshal(object["done"], &object) != nil {
			return nil
		}
		kind = "done"
	}
	if kind == "done" {
		if json.Unmarshal(object["usage"], &object) != nil {
			return nil
		}
	} else if kind != "usage" {
		return nil
	}
	var tokens map[string]*int64
	if json.Unmarshal(object["tokens"], &tokens) != nil {
		return nil
	}
	for _, key := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_output_tokens", "total_tokens"} {
		if tokens[key] == nil || *tokens[key] < 0 {
			return nil
		}
	}
	input, output, cached, reasoning, total := *tokens["input_tokens"], *tokens["output_tokens"], *tokens["cached_input_tokens"], *tokens["reasoning_output_tokens"], *tokens["total_tokens"]
	if cached > input || reasoning > output || input > math.MaxInt64-output || total != input+output {
		return nil
	}
	return &v1.TokenUsage{InputTokens: input, OutputTokens: output, TotalTokens: total, InputTokensDetails: v1.InputTokenDetails{CachedTokens: cached}, OutputTokensDetails: v1.OutputTokenDetails{ReasoningTokens: reasoning}}
}
