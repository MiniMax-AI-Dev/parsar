package agentsapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
	"github.com/openai/openai-go/v3"
)

type projection struct {
	text             string
	thinking         string
	tools            map[string]bool
	sequence         uint64
	terminalRecorded bool
	failure          string
}

func (c *Connector) restore(ctx context.Context, runID string) (projection, error) {
	p := projection{tools: map[string]bool{}}
	var after int64
	for {
		events, err := c.store.ListCoreExecutionEvents(ctx, runID, after)
		if err != nil {
			return p, errPersistence
		}
		if len(events) == 0 {
			return p, nil
		}
		for _, event := range events {
			after = event.Sequence
			switch event.EventKind {
			case "run.completed", "run.failed":
				p.terminalRecorded = true
				if event.EventKind == "run.failed" {
					p.failure = textValue(event.Payload["error"])
					if p.failure == "" {
						p.failure = "Core execution failed"
					}
				}
			case "message.delta":
				p.text += textValue(event.Payload["delta"])
			case "message.thinking":
				p.thinking += textValue(event.Payload["thinking"])
			case "tool.call":
				p.tools[textValue(event.Payload["id"])+":before"] = true
			case "tool.result":
				p.tools[textValue(event.Payload["id"])+":after"] = true
			}
			if n, ok := event.Payload["sequence"].(float64); ok && uint64(n) > p.sequence {
				p.sequence = uint64(n)
			}
		}
	}
}

func (c *Connector) observe(ctx context.Context, in connector.PromptInput, run store.CoreRunBinding, ch chan<- connector.PromptEvent) error {
	p, err := c.restore(ctx, in.RunID)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(c.poll)
	defer ticker.Stop()
	unsupportedInteraction := false
	for {
		product, err := c.store.GetCoreExecutionStatus(ctx, in.RunID)
		if err != nil {
			return errPersistence
		}
		if run.TurnID == "" {
			params := openai.BetaAgentSessionTurnListParams{Order: "asc", Limit: openai.Int(2)}
			if run.PreviousTurnID != "" {
				params.After = openai.String(run.PreviousTurnID)
			}
			turns, err := c.sessions.Turns.List(ctx, run.SessionID, params)
			if err != nil {
				return err
			}
			if len(turns.Data) > 1 {
				return errors.New("Core returned ambiguous execution history")
			}
			if len(turns.Data) == 1 {
				run.TurnID = turns.Data[0].ID
				if err := c.store.BindCoreTurn(ctx, in.RunID, run.TurnID); err != nil {
					return errPersistence
				}
			}
		}
		if run.TurnID != "" {
			turn, err := c.sessions.Turns.Get(ctx, run.SessionID, run.TurnID)
			if err != nil {
				return err
			}
			if turn.Status == "waiting" {
				unsupportedInteraction = true
			}
			if (product == "cancelled" || product == "failed" || p.failure != "" || unsupportedInteraction) && !terminal(string(turn.Status)) {
				if err := c.submitCancel(ctx, connector.AbortInput{RunID: in.RunID}); err != nil {
					return err
				}
			}
			items, err := c.turnItems(ctx, run.SessionID, run.TurnID)
			if err != nil {
				return err
			}
			if err := p.update(ctx, ch, items); err != nil {
				return err
			}
			if terminal(string(turn.Status)) {
				metadata := map[string]any{"source": ConnectorType, "core_session_id": run.SessionID, "core_turn_id": run.TurnID, "core_status": turn.Status}
				if turn.Status != "completed" || p.failure != "" {
					metadata["error"] = "Core execution " + string(turn.Status)
					if unsupportedInteraction {
						metadata["error"] = "Core interaction is not supported by this product yet"
					}
					if p.failure != "" {
						metadata["error"] = p.failure
					}
					if !p.terminalRecorded {
						if err := emit(ctx, ch, connector.PromptEvent{Type: connector.EventError, Error: textValue(metadata["error"])}); err != nil {
							return err
						}
					}
				}
				usage := store.UsageInput{Provider: ConnectorType, Model: textValue(in.AgentConfig["model"])}
				if turn.JSON.Usage.Valid() {
					usage.InputTokens = int32(min(max(turn.Usage.InputTokens, 0), math.MaxInt32))
					usage.OutputTokens = int32(min(max(turn.Usage.OutputTokens, 0), math.MaxInt32))
					usage.Raw = map[string]any{"core": json.RawMessage(turn.Usage.RawJSON())}
				}
				if turn.JSON.Usage.Valid() {
					if err := c.store.RecordCoreUsage(ctx, in.RunID, usage); err != nil {
						return errPersistence
					}
				}
				p.sequence++
				if err := emit(ctx, ch, connector.PromptEvent{Type: connector.EventDone, Recovered: p.terminalRecorded, Sequence: p.sequence, Final: &connector.PromptOutput{Content: p.text, Usage: usage, Metadata: metadata}}); err != nil {
					return err
				}
				if err := c.store.SettleCoreRun(ctx, in.RunID); err != nil {
					return errPersistence
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *Connector) turnItems(ctx context.Context, sessionID, turnID string) ([]openai.AgentSessionItemUnion, error) {
	page := c.sessions.Items.ListAutoPaging(ctx, sessionID, openai.BetaAgentSessionItemListParams{Order: "desc", Limit: openai.Int(100)})
	var items []openai.AgentSessionItemUnion
	for page.Next() {
		item := page.Current()
		if item.TurnID != turnID {
			break
		}
		items = append(items, item)
	}
	if err := page.Err(); err != nil {
		return nil, err
	}
	slices.Reverse(items)
	return items, nil
}

func (p *projection) update(ctx context.Context, ch chan<- connector.PromptEvent, items []openai.AgentSessionItemUnion) error {
	var text, thinking strings.Builder
	// Function results are separate items linked by call_id, not by item ID.
	outputs := map[string]openai.AgentSessionItemUnion{}
	for _, item := range items {
		if item.Type == "function_call_output" {
			outputs[item.CallID] = item
		}
	}
	for _, item := range items {
		switch item.Type {
		case "message":
			if item.Role == "assistant" {
				for _, content := range item.Content.OfAgentSessionMessageContentArray {
					if content.Type == "output_text" {
						text.WriteString(content.Text)
					}
				}
			}
		case "reasoning":
			for _, summary := range item.Summary {
				thinking.WriteString(summary.Text)
			}
		case "command_execution", "mcp_call", "web_search_call", "function_call":
			stage := "before"
			if item.Status == "completed" || item.Status == "failed" || item.Status == "incomplete" {
				stage = "after"
			}
			result := item
			if item.Type == "function_call" {
				// A completed call only means its arguments are complete. Wait for
				// the separate result before projecting a terminal tool result.
				stage = "before"
				if output, ok := outputs[item.CallID]; ok {
					result, stage = output, "after"
				}
			}
			key := item.ID + ":" + stage
			if !p.tools[key] {
				var raw map[string]any
				if err := json.Unmarshal([]byte(result.RawJSON()), &raw); err != nil {
					return err
				}
				// Keep the protocol status and expose its unsuccessful outcome to
				// every product trace consumer, including persisted replay.
				if result.Status == "incomplete" {
					raw["is_error"] = true
				}
				name := item.Name
				if name == "" {
					name = item.Type
				}
				p.sequence++
				if err := emit(ctx, ch, connector.PromptEvent{Type: connector.EventToolCall, Sequence: p.sequence, Tool: &connector.ToolCallEvent{ID: item.ID, Name: name, Stage: stage, Args: map[string]any{"command": item.Command, "arguments": item.Arguments}, Result: raw}}); err != nil {
					return err
				}
				p.tools[key] = true
			}
		}
	}
	if err := p.textDelta(ctx, ch, text.String(), false); err != nil {
		return err
	}
	return p.textDelta(ctx, ch, thinking.String(), true)
}

func (p *projection) textDelta(ctx context.Context, ch chan<- connector.PromptEvent, next string, thinking bool) error {
	previous := p.text
	if thinking {
		previous = p.thinking
	}
	if !strings.HasPrefix(next, previous) {
		return errors.New("Core output disagrees with persisted product history")
	}
	if len(next) == len(previous) {
		return nil
	}
	p.sequence++
	event := connector.PromptEvent{Type: connector.EventDelta, Delta: next[len(previous):], Sequence: p.sequence}
	if thinking {
		event.Type, event.Thinking, event.Delta = connector.EventThinking, event.Delta, ""
	}
	if err := emit(ctx, ch, event); err != nil {
		return err
	}
	if thinking {
		p.thinking = next
	} else {
		p.text = next
	}
	return nil
}

func textValue(value any) string { text, _ := value.(string); return text }
