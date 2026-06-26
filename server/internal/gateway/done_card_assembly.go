package gateway

// done_card_assembly.go: pure helpers for the DoneCard assembly path
// (steps fold + model context window). Orchestration lives in
// feishuoutbound so package gateway stays free of store dependencies.

import (
	"strings"
	"time"
)

// ToolCallEvent is the trimmed shape StepsFromToolCallEvents reads.
// Callers convert their store.AgentRunEvent rows into this — taking
// store directly would create an import cycle. OccurredAt is the
// agent_run_events row timestamp; needed so the renderer can compute
// per-step duration off paired tool.call/tool.result events.
type ToolCallEvent struct {
	EventKind  string
	Payload    map[string]any
	OccurredAt time.Time
}

// StepsFromToolCallEvents folds a stream of agent_run_events into
// StepInfo. Filters on EventKind=="tool.call" for the call+args view
// and pairs each call with the matching tool.result (by payload.id)
// so EndedAt is backfilled — leaving EndedAt zero is the renderer's
// signal to fall back to live-clock duration.
func StepsFromToolCallEvents(events []ToolCallEvent) []StepInfo {
	out := make([]StepInfo, 0, len(events))
	// id → index in out. Empty id skips pairing.
	byID := make(map[string]int, len(events))
	for _, ev := range events {
		payload := ev.Payload
		if payload == nil {
			continue
		}
		switch ev.EventKind {
		case "tool.call":
			// "before" stage only — tool.result events arrive separately;
			// this is mostly defensive against duplicates.
			if stage, ok := payload["stage"].(string); ok && stage != "" && stage != "before" {
				continue
			}
			name, _ := payload["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			id, _ := payload["id"].(string)
			id = strings.TrimSpace(id)
			label := name
			if args, ok := payload["args"].(map[string]any); ok {
				if hint := summariseToolArgsForCard(name, args); hint != "" {
					label = name + " · " + hint
				}
			}
			out = append(out, StepInfo{Tool: name, Label: label, ID: id, StartedAt: ev.OccurredAt})
			if id != "" {
				byID[id] = len(out) - 1
			}
		case "tool.result":
			id, _ := payload["id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				continue
			}
			if idx, ok := byID[id]; ok && out[idx].EndedAt.IsZero() {
				out[idx].EndedAt = ev.OccurredAt
			}
		}
	}
	return out
}

// summariseToolArgsForCard mirrors feishuoutbound.summariseToolArgs.
// Renderer truncates further if a row overflows.
func summariseToolArgsForCard(tool string, args map[string]any) string {
	switch tool {
	case "Read", "Edit", "Write", "NotebookEdit":
		if path, ok := args["file_path"].(string); ok && path != "" {
			return trimMiddleForCard(path, 60)
		}
	case "Bash":
		if cmd, ok := args["command"].(string); ok && cmd != "" {
			first := cmd
			if idx := strings.IndexAny(first, "\r\n"); idx > 0 {
				first = first[:idx]
			}
			return trimMiddleForCard(first, 60)
		}
	case "Grep", "Glob":
		if pattern, ok := args["pattern"].(string); ok && pattern != "" {
			return trimMiddleForCard(pattern, 60)
		}
	case "WebFetch", "WebSearch":
		if url, ok := args["url"].(string); ok && url != "" {
			return trimMiddleForCard(url, 60)
		}
		if q, ok := args["query"].(string); ok && q != "" {
			return trimMiddleForCard(q, 60)
		}
	case "Skill":
		// Skill tool's primary arg is the skill name. Without this
		// case the row collapses to a bare "Skill" — the user can't
		// tell which skill ran (issue: card showed `Skill 0s` only).
		if name, ok := args["skill"].(string); ok && name != "" {
			return trimMiddleForCard(name, 60)
		}
	}
	return ""
}

// trimMiddleForCard keeps prefix + ellipsis + suffix so long
// paths/commands stay recognisable.
func trimMiddleForCard(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	half := (n - 1) / 2
	return s[:half] + "…" + s[len(s)-half:]
}

// modelContextWindowTable maps a model id prefix to the documented
// context window. Prefix-matched because the runtime sometimes appends
// suffixes like `[1m]` or `-thinking-max` that don't change the window.
// Numbers track upstream docs and intentionally under-report rather
// than over-report so the footer percentage doesn't lie.
var modelContextWindowTable = []struct {
	prefix string
	window int
}{
	// Longer prefixes first so `claude-3-5-haiku` doesn't match a
	// `claude-3` row.
	{"claude-opus-4", 200_000},
	{"claude-sonnet-4", 200_000},
	{"claude-haiku-4", 200_000},
	{"claude-3-7-sonnet", 200_000},
	{"claude-3-5-sonnet", 200_000},
	{"claude-3-5-haiku", 200_000},
	{"claude-3-opus", 200_000},
	{"claude-3-sonnet", 200_000},
	{"claude-3-haiku", 200_000},
	{"claude-", 200_000},

	{"gpt-5", 128_000},
	{"gpt-4o", 128_000},
	{"gpt-4-turbo", 128_000},
	{"gpt-4", 8_192},
	{"o1", 200_000},
	{"o3", 200_000},

	{"deepseek", 65_536},
	{"qwen", 32_768},
}

// ContextWindowForModel returns the documented context window for a
// model id, or 0 when unknown. Prefix-matched.
func ContextWindowForModel(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return 0
	}
	for _, row := range modelContextWindowTable {
		if strings.HasPrefix(model, row.prefix) {
			return row.window
		}
	}
	return 0
}
