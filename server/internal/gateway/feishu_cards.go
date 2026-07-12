package gateway

// Feishu interactive card builders. Builders return `map[string]any`
// so callers can compose / wrap them; MarshalCard handles serialization.

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// FeishuCardWideScreen turns on Feishu's wide layout for desktop.
const FeishuCardWideScreen = true

// formCardConfig is the `config` block for any card whose body is a
// Feishu `form` container (credential form, AskUserQuestion form, …).
//
// `update_multi: true` is required by PATCH /im/v1/messages for cards
// that get patched in place. Without it, PATCH returns 200 + code=0
// but recipients keep seeing the original card — post-submit
// confirmation is silently lost. The AskUserQuestion path currently
// pins canonical state via ackToastWithCard rather than PATCH, but
// stamping update_multi up front means a future stale-sweep / timeout
// patch will Just Work without anyone re-deriving this rule.
func formCardConfig() map[string]any {
	return map[string]any{
		"wide_screen_mode": FeishuCardWideScreen,
		"update_multi":     true,
	}
}

// FeishuCardTitle is the fallback header title used when the caller
// could not resolve a specific Agent name. resolveCardTitle handles
// the trim + fallback.
const FeishuCardTitle = "Parsar Agent"

// resolveCardTitle picks the header title for a card: caller-supplied
// Agent name when non-empty, otherwise FeishuCardTitle.
func resolveCardTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return FeishuCardTitle
	}
	return trimmed
}

// StepInfo is one execution step shown on Working / Done cards.
type StepInfo struct {
	// Tool is the canonical tool name (`Bash`, `Read`, `Edit`,
	// `mcp__<server>__<tool>`, …). Used to pick an icon + color.
	Tool string
	// Label is the short user-facing line under the icon. Falls back
	// to Tool when empty.
	Label string
	// ID is the tool_call id from agent_run_events. Used to pair a
	// tool.call with its tool.result so EndedAt can be backfilled.
	// Empty when the event arrived without an id (defensive fallback).
	ID string
	// StartedAt is when the tool.call event landed. Zero when the step
	// was reconstructed from an old slot that pre-dated this field.
	StartedAt time.Time
	// EndedAt is when the paired tool.result landed. Zero while the
	// tool is still running — renderers treat it as "duration so far".
	EndedAt time.Time
}

// Duration returns the wall-clock the tool took, or how long it has
// been running. Returns 0 when StartedAt is unknown (old slot fallback)
// so callers can suppress the right-label entirely instead of showing
// a misleading "0s".
func (s StepInfo) Duration(now time.Time) time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	end := s.EndedAt
	if end.IsZero() {
		end = now
	}
	if !end.After(s.StartedAt) {
		return 0
	}
	return end.Sub(s.StartedAt)
}

// UsageStats is the optional model-usage footer rendered on a DoneCard.
type UsageStats struct {
	CostUSD       float64
	ContextUsed   int
	ContextWindow int
	Model         string
}

// toolIconTable maps canonical tool names to Feishu standard_icon
// tokens. Tools not in the table fall back to `app_default_outlined`.
var toolIconTable = map[string]string{
	"Bash":           "code_outlined",
	"Read":           "file_outlined",
	"Edit":           "edit_outlined",
	"Write":          "edit_outlined",
	"Grep":           "search_outlined",
	"Glob":           "search_outlined",
	"WebFetch":       "link_outlined",
	"WebSearch":      "search_outlined",
	"LSP":            "code_outlined",
	"Skill":          "robot_outlined",
	"Agent":          "code_outlined",
	"Workflow":       "robot_outlined",
	"NotebookEdit":   "edit_outlined",
	"TodoWrite":      "list_outlined",
	"EnterPlanMode":  "list_outlined",
	"ExitPlanMode":   "list_outlined",
	"EnterWorktree":  "code_outlined",
	"ExitWorktree":   "code_outlined",
	"CronCreate":     "time_outlined",
	"CronDelete":     "time_outlined",
	"CronList":       "time_outlined",
	"ScheduleWakeup": "time_outlined",
	"TaskOutput":     "code_outlined",
	"TaskStop":       "code_outlined",
}

// toolColorTable mirrors toolIconTable.
var toolColorTable = map[string]string{
	"Bash":           "blue",
	"Read":           "carmine",
	"Edit":           "violet",
	"Write":          "violet",
	"Grep":           "yellow",
	"Glob":           "yellow",
	"WebFetch":       "wathet",
	"WebSearch":      "green",
	"LSP":            "purple",
	"Skill":          "indigo",
	"Agent":          "blue",
	"Workflow":       "indigo",
	"NotebookEdit":   "orange",
	"EnterWorktree":  "purple",
	"ExitWorktree":   "purple",
	"CronCreate":     "green",
	"CronDelete":     "green",
	"CronList":       "green",
	"ScheduleWakeup": "green",
	"TaskOutput":     "wathet",
	"TaskStop":       "wathet",
}

// toolIcon picks a Feishu standard_icon token. Returns "" for tools
// that should render without a leading icon (AskUserQuestion's step
// row reads cleaner as plain text). MCP tools share a single token.
func toolIcon(tool string) string {
	if tool == "AskUserQuestion" {
		return ""
	}
	if isMCPTool(tool) {
		return "robot_outlined"
	}
	if token, ok := toolIconTable[tool]; ok {
		return token
	}
	return "app_default_outlined"
}

// toolColor picks an accent color. MCP tools share a single color.
func toolColor(tool string) string {
	if isMCPTool(tool) {
		return "turquoise"
	}
	if c, ok := toolColorTable[tool]; ok {
		return c
	}
	return "grey"
}

// isMCPTool matches both `mcp__<server>__<tool>` invocations and the
// built-in MCP meta tools (ListMcpResourcesTool, ReadMcpResourceTool)
// so they share the same robot icon and colour as other MCP traffic.
func isMCPTool(tool string) bool {
	if strings.HasPrefix(tool, "mcp__") {
		return true
	}
	switch tool {
	case "ListMcpResourcesTool", "ReadMcpResourceTool":
		return true
	}
	return false
}

// FormatElapsed renders a duration as `Ns` / `NmMs`. Sub-second runs
// collapse to `0s`. Exported so the feishuoutbound ping helper can
// produce the same "took 17s" suffix the DoneCard footer uses.
func FormatElapsed(d time.Duration) string {
	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	rem := seconds % 60
	if rem == 0 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dm%ds", minutes, rem)
}

// stepDiv renders one StepInfo as a Feishu `div` element with an icon
// and a single-line lark_md label. `now` is used to compute the
// duration suffix for steps still running (EndedAt zero); pass
// `time.Time{}` from the Done card (every step is already ended) to
// signal "skip the live-clock branch".
//
// When toolIcon(s.Tool) returns "" the div is rendered without an
// icon field — Feishu otherwise falls back to a default placeholder
// glyph we don't want.
func stepDiv(s StepInfo, now time.Time) map[string]any {
	label := strings.TrimSpace(s.Label)
	if label == "" {
		label = s.Tool
	}
	if label == "" {
		label = "…"
	}
	// Escape lark_md so a `*` or `_` in a Bash command doesn't render
	// as bold/italic. Done after the trim/fallback so the duration
	// suffix below stays out of the escape.
	content := escapeLarkMd(label)
	if d := s.Duration(now); d > 0 {
		content = content + "  <font color='grey'>" + FormatElapsed(d) + "</font>"
	}
	div := map[string]any{
		"tag": "div",
		"text": map[string]any{
			"tag":     "lark_md",
			"content": content,
		},
	}
	if token := toolIcon(s.Tool); token != "" {
		div["icon"] = map[string]any{
			"tag":   "standard_icon",
			"token": token,
			"color": toolColor(s.Tool),
		}
	}
	return div
}

// escapeLarkMd backslash-escapes the lark_md special characters so a
// raw step label (file path, Bash command, …) renders verbatim.
func escapeLarkMd(s string) string {
	const specials = "\\*_~`<>[]"
	if !strings.ContainsAny(s, specials) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for _, r := range s {
		if strings.ContainsRune(specials, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// makeCollapsibleHistory wraps a list of past steps in Feishu's
// collapsible_panel so the card stays compact while preserving the
// full trace. Cap is enforced by the caller so the header label can
// reflect "showing recent N".
func makeCollapsibleHistory(steps []StepInfo, label string, now time.Time) map[string]any {
	elements := make([]map[string]any, 0, len(steps))
	for _, s := range steps {
		elements = append(elements, stepDiv(s, now))
	}
	return map[string]any{
		"tag":      "collapsible_panel",
		"expanded": false,
		"header": map[string]any{
			"title":          map[string]any{"tag": "plain_text", "content": label},
			"vertical_align": "center",
			"padding":        "4px 0px 4px 8px",
			"icon": map[string]any{
				"tag":   "standard_icon",
				"token": "down-small-ccm_outlined",
				"color": "grey",
				"size":  "16px 16px",
			},
			"icon_position":       "right",
			"icon_expanded_angle": 180,
		},
		"vertical_spacing": "2px",
		"background_color": "default",
		"border":           map[string]any{"color": "grey", "corner_radius": "5px"},
		"elements":         elements,
	}
}

// stepsHistoryCap caps displayed past steps; older steps drop off
// and the header reflects "showing recent N".
const stepsHistoryCap = 50

// toFeishuMarkdown normalizes for Feishu's renderer: heading markers
// become bold, blockquote markers strip (Feishu's markdown doesn't
// support `#` headings or `>` blockquotes natively).
func toFeishuMarkdown(md string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(md, "\n") {
		stripped := strings.TrimLeft(line, " \t")
		switch {
		case strings.HasPrefix(stripped, "# ") || strings.HasPrefix(stripped, "## ") ||
			strings.HasPrefix(stripped, "### ") || strings.HasPrefix(stripped, "#### ") ||
			strings.HasPrefix(stripped, "##### ") || strings.HasPrefix(stripped, "###### "):
			// Strip leading #s + spaces and bold the rest.
			idx := strings.Index(stripped, " ")
			if idx > 0 && idx < len(stripped)-1 {
				b.WriteString("**")
				b.WriteString(strings.TrimSpace(stripped[idx+1:]))
				b.WriteString("**")
			} else {
				b.WriteString(stripped)
			}
		case strings.HasPrefix(stripped, "> "):
			b.WriteString(strings.TrimPrefix(stripped, "> "))
		case stripped == ">":
			// drop bare blockquote marker
		default:
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// markdownBodyCap bounds the body markdown so a runaway Agent reply
// doesn't push the card over Feishu's element limits. Beyond this the
// body is middle-truncated with a "…(content truncated)…" marker so both the
// head (gives the user a quick read-in) and the tail (where streaming
// runs always have their latest cue) survive.
const markdownBodyCap = 20000

// truncateMarkdownMarker is the visible cue inserted between the
// preserved head and tail when truncation kicks in. Both Feishu
// markdown and plain rendering treat the bytes verbatim.
const truncateMarkdownMarker = "\n\n…(content truncated)…\n\n"

// truncateMarkdown keeps the head + tail of the input when it exceeds
// markdownBodyCap. The marker takes up a slice of the cap so the
// returned string still respects the original bound.
//
// Why head + tail, not head-only:
//   - StreamingCard renders the running reply; the user's "what is
//     the model saying RIGHT NOW" cue lives at the tail. A head-only
//     truncate freezes the visible body at the early prefix forever.
//   - DoneCard renders the final reply; models commonly produce
//     conclusions at the end, so the tail carries the punchline.
//
// Why rune-safe slicing:
//   - markdownBodyCap is a BYTE cap (matches the Feishu element-size
//     limit), but slicing on a raw byte index can land inside a
//     multi-byte UTF-8 codepoint and produce mojibake. We walk back
//     to the nearest rune boundary in each direction.
func truncateMarkdown(s string) string {
	if len(s) <= markdownBodyCap {
		return s
	}
	budget := markdownBodyCap - len(truncateMarkdownMarker)
	if budget <= 0 {
		// Pathological: cap is so small the marker alone overflows.
		// Fall back to the legacy head-only behavior so we still cap.
		return safeRuneSlice(s, markdownBodyCap)
	}
	// Tail weighted slightly heavier than head — for streaming the
	// latest tokens carry the most signal; for done cards the
	// conclusion usually sits at the end.
	headBudget := budget / 3
	tailBudget := budget - headBudget
	head := safeRuneSlice(s, headBudget)
	tail := safeRuneSliceSuffix(s, tailBudget)
	return head + truncateMarkdownMarker + tail
}

// safeRuneSlice returns the longest prefix of s whose byte length is
// ≤ n, never splitting a multi-byte rune.
func safeRuneSlice(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	// Walk back from byte n until we land on a rune start (top two
	// bits are NOT 10 — i.e. byte is not a UTF-8 continuation).
	for n > 0 && n < len(s) && (s[n]&0xC0) == 0x80 {
		n--
	}
	return s[:n]
}

// safeRuneSliceSuffix returns the longest suffix of s whose byte
// length is ≤ n, never splitting a multi-byte rune.
func safeRuneSliceSuffix(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	start := len(s) - n
	for start < len(s) && (s[start]&0xC0) == 0x80 {
		start++
	}
	return s[start:]
}

// ----- Card builders -----

// BuildRunningCard renders the mid-run card that replaces the previous
// Working / Streaming pair. Layout (top → bottom) mirrors DoneCard so
// the disclosures look like siblings as a run transitions:
//
//	streamingText (markdown, omitted when empty)
//	── hr ── (only when BOTH streamingText AND steps are present)
//	[current step div]    (current tool, live duration)
//	▾ collapsible "Run log — N steps"
//
// Header tag is "Running" until the model emits any message.delta —
// after that the run is functionally "generating" and we flip to
// "Generating…". The single-card shape stays identical so PATCHing
// in-place is just a content swap, not a layout reshape.
//
// `now` is used to compute live duration on the still-running step
// (the one whose tool.result hasn't landed). Tests pass a fixed value
// to keep the golden shape reproducible.
func BuildRunningCard(title string, steps []StepInfo, streamingText string, elapsed time.Duration, now time.Time) map[string]any {
	body := strings.TrimSpace(streamingText)
	if body != "" {
		body = truncateMarkdown(toFeishuMarkdown(body))
	}

	subtitle := fmt.Sprintf("%d steps", len(steps))
	if elapsed > 0 {
		subtitle = subtitle + " · " + FormatElapsed(elapsed)
	}

	statusTag := "Running"
	if body != "" {
		statusTag = "Generating…"
	}

	elements := make([]map[string]any, 0, 4)
	if body != "" {
		elements = append(elements, map[string]any{"tag": "markdown", "content": body})
	}
	if len(steps) > 0 {
		if len(elements) > 0 {
			elements = append(elements, map[string]any{"tag": "hr"})
		}
		elements = append(elements, stepDiv(steps[len(steps)-1], now))
		past := steps[:len(steps)-1]
		if len(past) > 0 {
			displayed := past
			label := fmt.Sprintf("Run log — %d steps", len(past))
			if len(past) > stepsHistoryCap {
				displayed = past[len(past)-stepsHistoryCap:]
				label = fmt.Sprintf("Run log — %d steps (showing recent %d)", len(past), stepsHistoryCap)
			}
			elements = append(elements, makeCollapsibleHistory(displayed, label, now))
		}
	}
	if len(elements) == 0 {
		// No streaming text yet AND no tool.call seen — placeholder so
		// the body element list is never empty (Feishu rejects empty
		// bodies on some card versions).
		elements = append(elements, map[string]any{"tag": "markdown", "content": "…"})
	}

	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"subtitle": map[string]any{"tag": "plain_text", "content": subtitle},
			"template": "indigo",
			"icon":     map[string]any{"tag": "standard_icon", "token": "loading_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": statusTag},
				"color": "blue",
			}},
		},
		"body": map[string]any{"elements": elements},
	}
}

// BuildDoneCard renders the final "completed" card: reply body, an
// optional collapsible "Thinking" panel (model reasoning trace, only
// when thinkingText is non-empty), the collapsible execution history,
// then a grey usage footer.
func BuildDoneCard(title, content string, steps []StepInfo, thinkingText string, elapsed time.Duration, usage *UsageStats) map[string]any {
	processed := toFeishuMarkdown(content)
	body := truncateMarkdown(processed)
	elements := make([]map[string]any, 0, 5)
	// Thinking renders ABOVE the body so the card reads reasoning-
	// then-answer. Execution history stays AFTER the body as a
	// post-hoc receipt.
	if t := strings.TrimSpace(thinkingText); t != "" {
		elements = append(elements, makeThinkingPanel(t))
	}
	if body != "" {
		if len(elements) > 0 {
			elements = append(elements, map[string]any{"tag": "hr"})
		}
		elements = append(elements, map[string]any{"tag": "markdown", "content": body})
	}
	if len(steps) > 0 {
		if len(elements) > 0 {
			elements = append(elements, map[string]any{"tag": "hr"})
		}
		displayed := steps
		label := fmt.Sprintf("Run log — %d steps", len(steps))
		if len(steps) > stepsHistoryCap {
			displayed = steps[len(steps)-stepsHistoryCap:]
			label = fmt.Sprintf("Run log — %d steps (showing recent %d)", len(steps), stepsHistoryCap)
		}
		// DoneCard: every step is already ended, so the "now" argument
		// is unused by stepDiv. Zero time silences the live-clock
		// branch defensively in case a step somehow lacks EndedAt.
		elements = append(elements, makeCollapsibleHistory(displayed, label, time.Time{}))
	}
	// Footer renders even with no steps so the card signals "this
	// was a real Agent run, here's what it cost".
	elements = append(elements, doneFooterElement(elapsed, len(steps), usage))
	if len(elements) == 0 {
		// Defence in depth — footer is always appended above.
		elements = append(elements, map[string]any{"tag": "markdown", "content": "<font color='grey'>done</font>"})
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen, "streaming_mode": false},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "green",
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Completed"},
				"color": "green",
			}},
		},
		"body": map[string]any{"elements": elements},
	}
}

// makeThinkingPanel wraps the model's reasoning trace in a Feishu
// collapsible_panel (defaults to collapsed). No truncation — the panel
// is hidden by default so length is not a UX problem.
func makeThinkingPanel(text string) map[string]any {
	return map[string]any{
		"tag":      "collapsible_panel",
		"expanded": false,
		"header": map[string]any{
			"title": map[string]any{
				"tag":     "plain_text",
				"content": "Thinking",
			},
			"vertical_align": "center",
			"padding":        "4px 0px 4px 8px",
			// Without an explicit icon Feishu renders the panel as a
			// flat row with no chevron / visible affordance. Matching
			// makeCollapsibleHistory so the disclosures look like
			// siblings.
			"icon": map[string]any{
				"tag":   "standard_icon",
				"token": "down-small-ccm_outlined",
				"color": "grey",
				"size":  "16px 16px",
			},
			"icon_position":       "right",
			"icon_expanded_angle": 180,
		},
		"vertical_spacing": "2px",
		"background_color": "default",
		"border":           map[string]any{"color": "grey", "corner_radius": "5px"},
		"elements": []map[string]any{
			{"tag": "markdown", "content": text},
		},
	}
}

func doneFooterElement(elapsed time.Duration, stepCount int, usage *UsageStats) map[string]any {
	elapsedStr := FormatElapsed(elapsed)
	if usage != nil && usage.ContextWindow > 0 {
		// Both numerator and denominator render in the SAME unit
		// (always `k`) to avoid "169/200k" reading as 169k vs 200k
		// (~85%) when the math is 169 vs 200000 (~0.08%).
		//
		// Anything rounding to 0% but non-zero renders as `<1%`.
		ctxK := formatTokensK(usage.ContextWindow)
		usedK := formatTokensK(usage.ContextUsed)
		pct := 0
		if usage.ContextWindow > 0 {
			pct = int(float64(usage.ContextUsed) / float64(usage.ContextWindow) * 100)
		}
		pctStr := fmt.Sprintf("%d%%", pct)
		if pct == 0 && usage.ContextUsed > 0 {
			pctStr = "<1%"
		}
		model := strings.TrimSuffix(strings.TrimPrefix(usage.Model, "claude-"), "-thinking-max")
		cost := ""
		if usage.CostUSD > 0 {
			cost = fmt.Sprintf(" | $%.2f", usage.CostUSD)
		}
		line := fmt.Sprintf("%s%s | %s/%s (%s) | %s", elapsedStr, cost, usedK, ctxK, pctStr, model)
		return map[string]any{"tag": "markdown", "content": fmt.Sprintf("<font color='grey'>%s</font>", line)}
	}
	return map[string]any{"tag": "markdown", "content": fmt.Sprintf("<font color='grey'>%s · %d steps</font>", elapsedStr, stepCount)}
}

// formatTokensK renders a token count using a consistent `k` unit so
// a footer like `2.5k/200k` doesn't degenerate into `169/200k` for
// small values (which misleads as 169k vs 200k).
//
//   - 0     → "0k"
//   - <1k   → "<1k"
//   - <10k  → "1.2k" (one decimal)
//   - >=10k → "152k" (integer)
func formatTokensK(n int) string {
	switch {
	case n <= 0:
		return "0k"
	case n < 1000:
		return "<1k"
	case n < 10_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%dk", n/1000)
	}
}

// BuildPermissionCard renders the orange "pending approval" card with
// Allow / Deny buttons. Button `value` carries the
// permission_request_id so handleCardAction can route the decision back
// to connector.SubmitPermission. `toolInput` is shown verbatim in a
// code block (truncated at 500 chars).
func BuildPermissionCard(title, toolName, toolInput, permissionRequestID string) map[string]any {
	elements := []map[string]any{{
		"tag":     "markdown",
		"content": fmt.Sprintf("**Pending**\n\nAgent is requesting to use tool: **%s**", toolName),
	}}
	if trimmed := strings.TrimSpace(toolInput); trimmed != "" {
		preview := trimmed
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		elements = append(elements, map[string]any{
			"tag":     "markdown",
			"content": "```\n" + preview + "\n```",
		})
	}
	elements = append(elements, map[string]any{"tag": "hr"})
	elements = append(elements, map[string]any{
		"tag":       "column_set",
		"flex_mode": "flow",
		"columns": []map[string]any{
			{
				"tag":   "column",
				"width": "auto",
				"elements": []map[string]any{{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "Allow"},
					"type": "primary",
					"value": map[string]any{
						"action":                "permission_allow",
						"permission_request_id": permissionRequestID,
					},
					"behaviors": []map[string]any{{
						"type": "callback",
						"value": map[string]any{
							"action":                "permission_allow",
							"permission_request_id": permissionRequestID,
						},
					}},
				}},
			},
			{
				"tag":   "column",
				"width": "auto",
				"elements": []map[string]any{{
					"tag":  "button",
					"text": map[string]any{"tag": "plain_text", "content": "Reject"},
					"type": "danger",
					"value": map[string]any{
						"action":                "permission_deny",
						"permission_request_id": permissionRequestID,
					},
					"behaviors": []map[string]any{{
						"type": "callback",
						"value": map[string]any{
							"action":                "permission_deny",
							"permission_request_id": permissionRequestID,
						},
					}},
				}},
			},
		},
	})
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "orange",
			"icon":     map[string]any{"tag": "standard_icon", "token": "safe_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Pending"},
				"color": "orange",
			}},
		},
		"body": map[string]any{"elements": elements},
	}
}

// BuildPermissionResultCard renders the verdict that replaces a
// PermissionCard after the user clicks Allow / Deny. Green when
// allowed, red when denied.
func BuildPermissionResultCard(title string, allowed bool) map[string]any {
	label := "**Rejected**"
	template := "red"
	statusText := "Rejected"
	statusColor := "red"
	if allowed {
		label = "**Allowed**, continuing…"
		template = "green"
		statusText = "Allowed"
		statusColor = "green"
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": template,
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": statusText},
				"color": statusColor,
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{"tag": "markdown", "content": label}},
		},
	}
}

// NoticeColor is the small set of template colors NoticeCard accepts:
// info (grey), success (blue), warning (orange).
type NoticeColor string

const (
	NoticeColorInfo    NoticeColor = "grey"
	NoticeColorSuccess NoticeColor = "blue"
	NoticeColorWarning NoticeColor = "orange"
)

// BuildNoticeCard renders the "informational" card used by command
// echoes (/list, /help, /select) and guest hints.
func BuildNoticeCard(title, body string, color NoticeColor) map[string]any {
	header := resolveCardTitle(title)
	template := string(color)
	if template == "" {
		template = string(NoticeColorInfo)
	}
	content := strings.TrimSpace(body)
	if content == "" {
		content = " "
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": header},
			"template": template,
		},
		"body": map[string]any{
			"elements": []map[string]any{{"tag": "markdown", "content": content}},
		},
	}
}

// BuildQueueCard renders a one-shot "Queued (position N)" placeholder
// sent when a freshly created agent_run is blocked behind an inflight
// sibling. Never PATCHed; the inflight driver sends a fresh working
// card once the run is dequeued.
//
// Position counts QUEUED siblings only (1-indexed) — the running
// sibling holding the lane is NOT counted. Position ≤ 1 omits the
// suffix; passing 0 degrades to a bare "Queued".
func BuildQueueCard(title string, position int) map[string]any {
	label := "Queued"
	if position > 1 {
		label = fmt.Sprintf("Queued (position %d)", position)
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "grey",
			"icon":     map[string]any{"tag": "standard_icon", "token": "time_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Queue"},
				"color": "neutral",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{
				{"tag": "markdown", "content": label},
				{"tag": "markdown", "content": "_This message will start processing automatically once the previous task completes._"},
			},
		},
	}
}

// errorCardRawDetailLimit caps the raw error excerpt appended under
// the generic mapped messages. Feishu mobile wraps around 4-5 lines
// at this width; longer text gets middle-truncated rather than wall.
const errorCardRawDetailLimit = 160

// BuildErrorCard renders the red "failed" card. Routed when the
// outbound worker detects metadata.message_type=run_failure. Shows
// only the FIRST LINE of `message` (stack traces leak internal paths
// and are unreadable in a card). detailURL appends a "View this round"
// link when non-empty; "" degrades to just the first-line body.
//
// rawError is the un-mapped error string from the run.failed event;
// when message is one of the generic "Expand this round for error details" copies
// (which by themselves tell the user nothing actionable), the first
// line of rawError is appended as a dimmed italic excerpt so the
// reader can see the real failure without opening the run detail.
// Other mapped messages ignore rawError because they already carry a
// specific actionable hint and raw text would just add noise.
//
// guestHint is the "go register" suffix stamped by VisibilityGate when
// an unregistered user @-ed a public agent (see gateway/visibility.go).
// Public agents let guests in, but capability-credential gaps then
// crash the run with a generic error and the credential-form card
// path can't recover an inbound for a sender_type='external' message.
// Surfacing the hint on the terminal card is the only channel guests
// have to learn they need to register. Empty for registered users.
func BuildErrorCard(title, message, rawError, detailURL, guestHint string) map[string]any {
	body := firstLine(strings.TrimSpace(message))
	if body == "" {
		body = "Agent run failed. Please retry later."
	}
	if excerpt := rawErrorExcerpt(body, rawError); excerpt != "" {
		body = body + "\n*Error details: " + excerpt + "*"
	}
	if hint := strings.TrimSpace(guestHint); hint != "" {
		body = body + "\n\n" + hint
	}
	if url := strings.TrimSpace(detailURL); url != "" {
		body = body + "\n\n[View this round](" + url + ")"
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "red",
			"icon":     map[string]any{"tag": "standard_icon", "token": "warning_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Failed"},
				"color": "red",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{"tag": "markdown", "content": body}},
		},
	}
}

// rawErrorExcerpt returns the trimmed first line of raw (capped at
// errorCardRawDetailLimit runes, marked with "…" on truncation) only
// when body matches a generic "Expand this round for error details" copy. Returns
// "" when raw is empty, equal to body, or body already carries a
// specific hint — in those cases the excerpt would be noise.
func rawErrorExcerpt(body, raw string) string {
	first := firstLine(strings.TrimSpace(raw))
	if first == "" || first == body {
		return ""
	}
	if !strings.Contains(body, "Expand this round for error details") {
		return ""
	}
	runes := []rune(first)
	if len(runes) > errorCardRawDetailLimit {
		return string(runes[:errorCardRawDetailLimit]) + "…"
	}
	return first
}

// firstLine returns s up to (but not including) the first '\n' or
// '\r'. Used to keep ErrorCard bodies one-line: backend errors often
// carry "msg\nstack trace\nat foo.go:42" which renders as a wall.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return strings.TrimRightFunc(s[:i], unicode.IsSpace)
	}
	return s
}

// MarshalCard serializes a builder result into the JSON string the
// outbound Sender expects for MsgType="interactive".
func MarshalCard(card map[string]any) (string, error) {
	raw, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// CredentialFormField describes one credential gap on the form card.
//
//   - Kind is the credential_kinds.code.
//   - Label is the human-readable name.
//   - CapabilityName names the MCP that needs it.
type CredentialFormField struct {
	Kind           string
	Label          string
	CapabilityName string
	Placeholder    string
}

// BuildCredentialFormCard renders the orange "need credentials" card
// with one input per missing credential and a submit button. Fired when
// ResolveCapabilitiesForRuntime returns a non-empty Disabled list.
//
// qkey is the Redis key under which the original raw query was stashed
// before this card was sent. It MUST round-trip verbatim through the
// form submit so the handler can fetch and re-enqueue the original
// message (the user did not re-type — auto-retry is the UX guarantee).
func BuildCredentialFormCard(title string, fields []CredentialFormField, qkey string) map[string]any {
	formElements := []map[string]any{{
		"tag":     "markdown",
		"content": "**Credentials required**\n\nThis conversation needs a secret to run (model API key or MCP credentials). It will resume automatically after you submit.",
	}}
	if len(fields) == 0 {
		formElements = append(formElements, map[string]any{
			"tag":     "markdown",
			"content": "(no credential fields)",
		})
	}
	seenCaps := map[string]struct{}{}
	for _, f := range fields {
		// Lead-in line per credential so the user sees which MCP it
		// belongs to.
		capLine := strings.TrimSpace(f.CapabilityName)
		if capLine != "" {
			seenCaps[capLine] = struct{}{}
			formElements = append(formElements, map[string]any{
				"tag":     "markdown",
				"content": fmt.Sprintf("**%s · %s**", capLine, defaultIfEmpty(f.Label, f.Kind)),
			})
		}
		placeholder := strings.TrimSpace(f.Placeholder)
		if placeholder == "" {
			placeholder = "Paste " + defaultIfEmpty(f.Label, f.Kind)
		}
		formElements = append(formElements, map[string]any{
			"tag":            "input",
			"name":           "credential_" + f.Kind,
			"placeholder":    map[string]any{"tag": "plain_text", "content": placeholder},
			"required":       true,
			"input_type":     "password",
			"label":          map[string]any{"tag": "plain_text", "content": defaultIfEmpty(f.Label, f.Kind)},
			"label_position": "top",
		})
	}
	if len(fields) > 0 {
		formElements = append(formElements, map[string]any{
			"tag":  "button",
			"name": "credential_form_submit",
			"text": map[string]any{
				"tag":     "plain_text",
				"content": "Submit and continue",
			},
			"type":             "primary",
			"action_type":      "form_submit",
			"form_action_type": "submit",
			"value": map[string]any{
				"action": "credential_form_submit",
				"qkey":   qkey,
			},
			"behaviors": []map[string]any{{
				"type": "callback",
				"value": map[string]any{
					"action": "credential_form_submit",
					"qkey":   qkey,
				},
			}},
		})
	}

	return map[string]any{
		"schema": FeishuCardSchema,
		"config": formCardConfig(),
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "orange",
			"icon":     map[string]any{"tag": "standard_icon", "token": "lock_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Credentials required"},
				"color": "orange",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{
				// One form wraps every input + submit button so the
				// callback receives a single form_value map and we can
				// write every credential atomically.
				"tag":      "form",
				"name":     "credential_form",
				"elements": formElements,
			}},
		},
	}
}

// BuildCredentialFormSubmittedCard replaces BuildCredentialFormCard
// after the user submits. Green = "Received, resuming the conversation".
//
// Feishu's PATCH /im/v1/messages has THREE runtime constraints when
// patching FROM a form-container card:
//
//  1. Schema-shape preservation: a card containing `tag: form` cannot
//     be PATCHed into one without it (returns 200 + code=0 but the
//     client snaps back to the original orange form).
//
//  2. Name-bearing components: every interactive component inside a
//     form container must have a non-empty `name` (code 200530).
//
//  3. Submit-button requirement: every form container must hold at
//     least one `action_type: "form_submit"` button (code 230099 /
//     ErrCode 300123). A plain `type: default` does NOT count.
//
// The green card therefore keeps the form shell AND embeds a
// placeholder `form_submit` button. handleCardAction's
// "credential_form_acknowledged" branch toasts and no-ops on re-click;
// the qkey was already consumed by the original submit.
func BuildCredentialFormSubmittedCard(title string) map[string]any {
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": formCardConfig(),
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "green",
			"icon":     map[string]any{"tag": "standard_icon", "token": "done_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Saved"},
				"color": "green",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{
				"tag":  "form",
				"name": "credential_form",
				"elements": []any{
					map[string]any{
						"tag":     "markdown",
						"content": "**Credentials saved**\n\nThe conversation will resume with the newly bound credentials; no need to resend the message.",
					},
					// Placeholder submit button required by Feishu's
					// form schema (code 230099 / ErrCode 300123).
					// handleCardAction's
					// "credential_form_acknowledged" branch toasts
					// and no-ops; the qkey was already consumed.
					map[string]any{
						"tag":  "button",
						"name": "credential_form_acknowledged",
						"text": map[string]any{
							"tag":     "plain_text",
							"content": "Saved",
						},
						"type":             "default",
						"action_type":      "form_submit",
						"form_action_type": "submit",
						"value": map[string]any{
							"action": "credential_form_acknowledged",
						},
						"behaviors": []map[string]any{{
							"type": "callback",
							"value": map[string]any{
								"action": "credential_form_acknowledged",
							},
						}},
					},
				},
			}},
		},
	}
}

// CredentialFormRejectReason discriminates the explanation on the red
// rejection card. The qkey is consumed by the failed claim either way
// (single-shot semantics guard against attacker callback spam), so the
// initiator needs the card to turn terminal rather than stare at an
// unchanged orange form.
type CredentialFormRejectReason string

const (
	// CredentialFormRejectOperatorMismatch: an open_id other than the
	// inbound sender clicked submit. Common cause: chat-group member
	// trying the form before the initiator does.
	CredentialFormRejectOperatorMismatch CredentialFormRejectReason = "operator_mismatch"
	// CredentialFormRejectChatMismatch: the click came from a
	// different open_chat than the form was posted to (leaked qkey
	// replayed). Tighter cross-chat check on top of the operator check.
	CredentialFormRejectChatMismatch CredentialFormRejectReason = "chat_mismatch"
)

// BuildCredentialFormRejectedCard replaces the orange form with a red
// terminal card. The qkey is already consumed (auth check fires AFTER
// ClaimAndDelete), so the card MUST flip terminal; otherwise the
// legitimate initiator sees an "expired" toast with no explanation.
//
// Body keeps the `tag: form` shell + placeholder submit button — same
// Feishu PATCH constraints as BuildCredentialFormSubmittedCard.
func BuildCredentialFormRejectedCard(title string, reason CredentialFormRejectReason) map[string]any {
	var body string
	switch reason {
	case CredentialFormRejectOperatorMismatch:
		body = "**This card can only be submitted by the original requester**\n\nClicks from other members were ignored. The conversation window for this card has expired; please @-mention the bot again to start a new round."
	case CredentialFormRejectChatMismatch:
		body = "**This card can only be submitted in the original conversation**\n\nThe window for this card has expired; return to the original conversation and @-mention the bot again to start a new round."
	default:
		body = "**Credential submission rejected**\n\nThe window for this card has expired; please @-mention the bot again to start a new round."
	}
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": formCardConfig(),
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "red",
			"icon":     map[string]any{"tag": "standard_icon", "token": "error_outlined"},
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Rejected"},
				"color": "red",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{
				"tag":  "form",
				"name": "credential_form",
				"elements": []any{
					map[string]any{
						"tag":     "markdown",
						"content": body,
					},
					// Placeholder submit button — Feishu requires every
					// form container to hold at least one
					// `action_type: form_submit` button (code 230099 /
					// ErrCode 300123). handleCardAction's
					// "credential_form_acknowledged" branch toasts
					// and no-ops.
					map[string]any{
						"tag":  "button",
						"name": "credential_form_acknowledged",
						"text": map[string]any{
							"tag":     "plain_text",
							"content": "Ended",
						},
						"type":             "default",
						"action_type":      "form_submit",
						"form_action_type": "submit",
						"value": map[string]any{
							"action": "credential_form_acknowledged",
						},
						"behaviors": []map[string]any{{
							"type": "callback",
							"value": map[string]any{
								"action": "credential_form_acknowledged",
							},
						}},
					},
				},
			}},
		},
	}
}

// defaultIfEmpty returns def when s trims to empty, else s.
func defaultIfEmpty(s, def string) string {
	if strings.TrimSpace(s) == "" {
		return def
	}
	return s
}

// PromptForUserChoiceCardOption is the gateway-level snapshot of one
// selectable answer. Mirrors store.PromptForUserChoiceOption (and the
// daemon proto) so the outbound driver can pass them straight in.
type PromptForUserChoiceCardOption struct {
	Label       string
	Description string
}

// PromptForUserChoiceCardQuestion is one question rendered in a multi-
// question AskUserQuestion card. Mirrors store.PromptForUserChoiceQuestion.
type PromptForUserChoiceCardQuestion struct {
	Header      string
	Question    string
	MultiSelect bool
	Options     []PromptForUserChoiceCardOption
}

// promptForUserChoiceQuestionFieldName returns the stable field name
// used for one question inside the card's form. Form callbacks come
// back keyed by this name; the inbound handler reverses the mapping by
// stripping the "q<i>_" prefix or matching the header verbatim.
func promptForUserChoiceQuestionFieldName(index int) string {
	return fmt.Sprintf("q%d", index)
}

// BuildPromptForUserChoiceCard renders the interactive card the human
// picks answers from. The card wraps every question + a single
// submit button in a Feishu form so multi-question calls resolve in
// one round-trip rather than N click → patch cycles. Each question's
// answer rides on form_value["q<i>"]; the submit button carries the
// request_id.
//
// Single-question calls go through the same form path — one question
// is just len(questions)==1. Keeping one renderer means the inbound
// callback path doesn't need a fork for "old" vs "new" cards.
func BuildPromptForUserChoiceCard(title string, questions []PromptForUserChoiceCardQuestion, requestID string) map[string]any {
	formElements := []map[string]any{}

	for idx, q := range questions {
		header := strings.TrimSpace(q.Header)
		question := strings.TrimSpace(q.Question)
		if header == "" {
			header = fmt.Sprintf("Question %d", idx+1)
		}
		// Question prompt. Use lark_md (not markdown) so headers
		// inside the form container render — Feishu's form elements
		// list only accepts whitelisted tags.
		formElements = append(formElements, map[string]any{
			"tag":     "markdown",
			"content": fmt.Sprintf("**%s**\n\n%s", header, question),
		})

		fieldName := promptForUserChoiceQuestionFieldName(idx)

		// Options are rendered as a select_static (single-select) or
		// multi_select_static (multi-select). Form_value keys off
		// `name`. Empty options list shouldn't happen but renders as
		// a free-text input so the card still works.
		if len(q.Options) == 0 {
			formElements = append(formElements, map[string]any{
				"tag":            "input",
				"name":           fieldName,
				"placeholder":    map[string]any{"tag": "plain_text", "content": "Enter your answer"},
				"label":          map[string]any{"tag": "plain_text", "content": "Answer"},
				"label_position": "top",
			})
		} else {
			options := make([]map[string]any, 0, len(q.Options))
			for _, opt := range q.Options {
				label := strings.TrimSpace(opt.Label)
				if label == "" {
					continue
				}
				options = append(options, map[string]any{
					"text":  map[string]any{"tag": "plain_text", "content": label},
					"value": label,
				})
			}
			selectTag := "select_static"
			placeholder := "Select an option"
			if q.MultiSelect {
				selectTag = "multi_select_static"
				placeholder = "Multi-select"
			}
			formElements = append(formElements, map[string]any{
				"tag":         selectTag,
				"name":        fieldName,
				"placeholder": map[string]any{"tag": "plain_text", "content": placeholder},
				"options":     options,
				"width":       "fill",
			})
		}

		// Per-option description hints render as a single grey
		// markdown block under the select, when any of the options
		// has a description.
		hints := make([]string, 0, len(q.Options))
		for _, opt := range q.Options {
			label := strings.TrimSpace(opt.Label)
			desc := strings.TrimSpace(opt.Description)
			if label == "" || desc == "" {
				continue
			}
			hints = append(hints, fmt.Sprintf("**%s** — %s", label, desc))
		}
		if len(hints) > 0 {
			formElements = append(formElements, map[string]any{
				"tag":     "markdown",
				"content": "<font color='grey'>" + strings.Join(hints, "\n") + "</font>",
			})
		}

		// Visual separator between questions; skip after the last.
		if idx < len(questions)-1 {
			formElements = append(formElements, map[string]any{"tag": "hr"})
		}
	}

	// Submit button — form_action_type=submit makes Feishu bundle
	// every field's form_value into the callback.
	formElements = append(formElements, map[string]any{
		"tag":              "button",
		"name":             "ask_user_choice_submit",
		"text":             map[string]any{"tag": "plain_text", "content": "Submit"},
		"type":             "primary",
		"action_type":      "form_submit",
		"form_action_type": "submit",
		"value": map[string]any{
			"action":     "ask_user_choice_submit",
			"request_id": requestID,
		},
		"behaviors": []map[string]any{{
			"type": "callback",
			"value": map[string]any{
				"action":     "ask_user_choice_submit",
				"request_id": requestID,
			},
		}},
	})

	form := map[string]any{
		"tag":      "form",
		"name":     "ask_user_choice_form",
		"elements": formElements,
	}

	return map[string]any{
		"schema": FeishuCardSchema,
		"config": formCardConfig(),
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "blue",
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Awaiting reply"},
				"color": "blue",
			}},
		},
		"body": map[string]any{"elements": []map[string]any{form}},
	}
}

// PromptForUserChoiceCardAnswer carries one question's answer for the
// done card. Header pairs with the question shown on the original card.
type PromptForUserChoiceCardAnswer struct {
	Header string
	Answer string
}

// BuildPromptForUserChoiceDoneCard renders the post-answer card the
// inbound handler PATCHes the original message into. Green status tag;
// echoes every (header, answer) so the chat log keeps a record of what
// was chosen across all questions.
func BuildPromptForUserChoiceDoneCard(title string, answers []PromptForUserChoiceCardAnswer) map[string]any {
	parts := make([]string, 0, len(answers))
	for idx, a := range answers {
		header := strings.TrimSpace(a.Header)
		if header == "" {
			header = fmt.Sprintf("Question %d", idx+1)
		}
		answer := strings.TrimSpace(a.Answer)
		if answer == "" {
			answer = "(no selection)"
		}
		parts = append(parts, fmt.Sprintf("**%s**: %s", header, answer))
	}
	if len(parts) == 0 {
		parts = []string{"(no selection)"}
	}
	body := strings.Join(parts, "\n\n")
	return map[string]any{
		"schema": FeishuCardSchema,
		"config": map[string]any{"wide_screen_mode": FeishuCardWideScreen},
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": resolveCardTitle(title)},
			"template": "green",
			"text_tag_list": []map[string]any{{
				"tag":   "text_tag",
				"text":  map[string]any{"tag": "plain_text", "content": "Answered"},
				"color": "green",
			}},
		},
		"body": map[string]any{
			"elements": []map[string]any{{"tag": "markdown", "content": body}},
		},
	}
}
