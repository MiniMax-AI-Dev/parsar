package gateway

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestBuildDoneCard_Shape locks the shape of the green "已完成" card so
// drift in the builder is caught at PR time, not when a user notices
// their card looks wrong in Feishu. We assert structural keys + the
// template / tag values that drive the visual treatment; we don't
// fingerprint every leaf string since some (icon tokens, color
// constants) are tuning-prone.
func TestBuildDoneCard_Shape(t *testing.T) {
	steps := []StepInfo{{Tool: "Bash", Label: "ls -la"}, {Tool: "Read", Label: "main.go"}}
	usage := &UsageStats{CostUSD: 0.12, ContextUsed: 1200, ContextWindow: 200000, Model: "claude-sonnet-4-5"}
	card := BuildDoneCard("", "hello world", steps, "", 12*time.Second, usage)

	assertString(t, card, "schema", "2.0")
	header := mapField(t, card, "header")
	assertString(t, header, "template", "green")

	tags := sliceField(t, header, "text_tag_list")
	if len(tags) == 0 {
		t.Fatalf("BuildDoneCard: text_tag_list empty")
	}
	tag, ok := tags[0].(map[string]any)
	if !ok {
		t.Fatalf("BuildDoneCard: tag[0] not a map: %T", tags[0])
	}
	if got := mapField(t, tag, "text")["content"]; got != "已完成" {
		t.Errorf("BuildDoneCard: tag content = %v, want 已完成", got)
	}

	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	// Expect at least: body markdown, hr separator, collapsible history, footer markdown
	if len(elements) < 4 {
		t.Errorf("BuildDoneCard: elements = %d, want >= 4 (body+hr+history+footer)", len(elements))
	}
	// Footer must be the last markdown with the elapsed/usage line.
	last, ok := elements[len(elements)-1].(map[string]any)
	if !ok {
		t.Fatalf("BuildDoneCard: last element not a map")
	}
	footerContent, _ := last["content"].(string)
	if !strings.Contains(footerContent, "12s") {
		t.Errorf("BuildDoneCard: footer missing elapsed, got %q", footerContent)
	}
	if !strings.Contains(footerContent, "$0.12") {
		t.Errorf("BuildDoneCard: footer missing cost, got %q", footerContent)
	}
}

// TestBuildDoneCard_EmptySteps verifies the degenerate case the
// outbound worker hits in P1: agent reply with no steps / no usage.
// Should still emit a valid green card with body + footer.
func TestBuildDoneCard_EmptySteps(t *testing.T) {
	card := BuildDoneCard("", "hi", nil, "", 0, nil)
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	if len(elements) < 2 {
		t.Fatalf("BuildDoneCard empty: elements = %d, want >= 2 (body+footer)", len(elements))
	}
	last, _ := elements[len(elements)-1].(map[string]any)
	footer, _ := last["content"].(string)
	if !strings.Contains(footer, "0 steps") {
		t.Errorf("BuildDoneCard empty: footer missing 0 steps, got %q", footer)
	}
}

// TestBuildDoneCard_ThinkingPanelCollapsedByDefault verifies that
// when thinkingText is non-empty the renderer drops in a
// collapsible_panel ABOVE the steps panel, defaulting to collapsed.
// User-facing requirement: the model's reasoning trace is hidden
// behind a disclosure so the reply stays uncluttered.
func TestBuildDoneCard_ThinkingPanelCollapsedByDefault(t *testing.T) {
	steps := []StepInfo{{Tool: "Bash", Label: "ls"}}
	card := BuildDoneCard("", "hi", steps, "The user said hi. I should reply.", 3*time.Second, nil)
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")

	var thinking, history map[string]any
	for _, raw := range elements {
		el, _ := raw.(map[string]any)
		tag, _ := el["tag"].(string)
		if tag != "collapsible_panel" {
			continue
		}
		header, _ := el["header"].(map[string]any)
		title, _ := header["title"].(map[string]any)
		content, _ := title["content"].(string)
		if content == "Thinking" {
			thinking = el
		} else if strings.HasPrefix(content, "执行记录") {
			history = el
		}
	}
	if thinking == nil {
		t.Fatal("BuildDoneCard: no Thinking collapsible_panel emitted")
	}
	if history == nil {
		t.Fatal("BuildDoneCard: expected execution history panel to coexist with thinking")
	}
	if exp, ok := thinking["expanded"].(bool); !ok || exp {
		t.Errorf("Thinking panel expanded = %v, want false (collapsed by default)", thinking["expanded"])
	}
	// Thinking body element should carry the reasoning markdown.
	inner, _ := thinking["elements"].([]map[string]any)
	if len(inner) == 0 {
		t.Fatal("Thinking panel has no inner elements")
	}
	md, _ := inner[0]["content"].(string)
	if !strings.Contains(md, "The user said hi") {
		t.Errorf("Thinking panel content = %q, want to contain reasoning text", md)
	}
}

// TestBuildDoneCard_ThinkingPrecedesBody pins the section order:
// Thinking sits ABOVE the markdown body so the card reads
// reasoning-then-answer, matching every other Claude surface. Easy
// to regress without a test — the previous order was body-first.
func TestBuildDoneCard_ThinkingPrecedesBody(t *testing.T) {
	card := BuildDoneCard("", "the answer", nil, "the reasoning", 0, nil)
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")

	thinkingIdx, bodyIdx := -1, -1
	for i, raw := range elements {
		el, _ := raw.(map[string]any)
		tag, _ := el["tag"].(string)
		switch tag {
		case "collapsible_panel":
			header, _ := el["header"].(map[string]any)
			title, _ := header["title"].(map[string]any)
			if c, _ := title["content"].(string); c == "Thinking" && thinkingIdx < 0 {
				thinkingIdx = i
			}
		case "markdown":
			if c, _ := el["content"].(string); strings.Contains(c, "the answer") && bodyIdx < 0 {
				bodyIdx = i
			}
		}
	}
	if thinkingIdx < 0 || bodyIdx < 0 {
		t.Fatalf("missing element: thinkingIdx=%d bodyIdx=%d", thinkingIdx, bodyIdx)
	}
	if thinkingIdx >= bodyIdx {
		t.Errorf("Thinking at %d, body at %d — Thinking must precede body", thinkingIdx, bodyIdx)
	}
}

// TestBuildDoneCard_NoThinkingPanelWhenEmpty confirms the renderer
// omits the disclosure entirely (rather than emit an empty
// collapsible_panel) when no reasoning was captured.
func TestBuildDoneCard_NoThinkingPanelWhenEmpty(t *testing.T) {
	card := BuildDoneCard("", "hi", nil, "   ", 0, nil)
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	for _, raw := range elements {
		el, _ := raw.(map[string]any)
		if tag, _ := el["tag"].(string); tag == "collapsible_panel" {
			header, _ := el["header"].(map[string]any)
			title, _ := header["title"].(map[string]any)
			if content, _ := title["content"].(string); content == "Thinking" {
				t.Errorf("BuildDoneCard: emitted Thinking panel for whitespace-only text")
			}
		}
	}
}

// TestBuildRunningCard_StepsOnly is the "just started, no model
// reply yet" shape — header tag must stay "执行中" and the body has
// no markdown / hr. Replaces the old TestBuildWorkingCard_Shape.
func TestBuildRunningCard_StepsOnly(t *testing.T) {
	steps := []StepInfo{{Tool: "Bash", Label: "running tests"}}
	card := BuildRunningCard("", steps, "", 5*time.Second, time.Time{})
	header := mapField(t, card, "header")
	assertString(t, header, "template", "indigo")
	tags := sliceField(t, header, "text_tag_list")
	tag, _ := tags[0].(map[string]any)
	if got := mapField(t, tag, "text")["content"]; got != "执行中" {
		t.Errorf("status tag = %v, want 执行中 (no streaming text)", got)
	}
	subtitle := mapField(t, header, "subtitle")
	if got := subtitle["content"]; got != "1 steps · 5s" {
		t.Errorf("subtitle = %v, want '1 steps · 5s'", got)
	}
	elements := sliceField(t, mapField(t, card, "body"), "elements")
	for _, el := range elements {
		m, _ := el.(map[string]any)
		if m["tag"] == "markdown" {
			t.Errorf("steps-only RunningCard must not render a markdown body, got %v", m["content"])
		}
		if m["tag"] == "hr" {
			t.Errorf("steps-only RunningCard must not render hr separator")
		}
	}
}

// TestBuildRunningCard_StreamingFlipsStatusTag pins the visible cue
// that the model has started producing a reply: as soon as
// streamingText is non-empty, the status tag flips "执行中" → "生成中…".
func TestBuildRunningCard_StreamingFlipsStatusTag(t *testing.T) {
	steps := []StepInfo{{Tool: "Bash", Label: "running tests"}}
	card := BuildRunningCard("", steps, "partial reply", 5*time.Second, time.Time{})
	tag, _ := sliceField(t, mapField(t, card, "header"), "text_tag_list")[0].(map[string]any)
	if got := mapField(t, tag, "text")["content"]; got != "生成中…" {
		t.Errorf("status tag = %v, want 生成中…", got)
	}
}

// TestBuildRunningCard_LayoutOrder pins the top-down element order
// when both streaming text and tool steps are present: markdown body
// FIRST, hr, current step div, collapsible history. This is the core
// fix — previously a message.delta event would render a StreamingCard
// that DROPPED the tool steps entirely; the new shape keeps both.
func TestBuildRunningCard_LayoutOrder(t *testing.T) {
	started := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	steps := []StepInfo{
		{Tool: "Read", Label: "Read · file.go", StartedAt: started, EndedAt: started.Add(1 * time.Second)},
		{Tool: "Edit", Label: "Edit · file.go", StartedAt: started.Add(2 * time.Second), EndedAt: started.Add(3 * time.Second)},
		{Tool: "Bash", Label: "Bash · go test", StartedAt: started.Add(4 * time.Second)},
	}
	card := BuildRunningCard("", steps, "我来排查这个 session。", 10*time.Second, started.Add(7*time.Second))
	elements := sliceField(t, mapField(t, card, "body"), "elements")
	if len(elements) != 4 {
		t.Fatalf("elements = %d, want 4 (markdown + hr + current step + collapsible)", len(elements))
	}
	tags := make([]any, 0, 4)
	for _, el := range elements {
		m, _ := el.(map[string]any)
		tags = append(tags, m["tag"])
	}
	want := []any{"markdown", "hr", "div", "collapsible_panel"}
	for i := range want {
		if tags[i] != want[i] {
			t.Errorf("element[%d].tag = %v, want %v (full order: %v)", i, tags[i], want[i], tags)
			break
		}
	}
	// History label uses the DoneCard convention so all three card
	// states render the same disclosure.
	panel, _ := elements[3].(map[string]any)
	panelHeader, _ := panel["header"].(map[string]any)
	headerTitle, _ := panelHeader["title"].(map[string]any)
	if got, _ := headerTitle["content"].(string); !strings.Contains(got, "执行记录") {
		t.Errorf("history label = %q, want it to start with '执行记录' (DoneCard parity)", got)
	}
}

// TestBuildRunningCard_RunningStepShowsLiveDuration is the live-clock
// cue — the still-running step (no paired tool.result yet) renders
// (now - StartedAt) as a grey duration suffix. Tick by tick this is
// what users see "in motion".
func TestBuildRunningCard_RunningStepShowsLiveDuration(t *testing.T) {
	started := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	now := started.Add(4 * time.Second)
	steps := []StepInfo{{Tool: "Bash", Label: "running tests", StartedAt: started}}
	card := BuildRunningCard("", steps, "", 10*time.Second, now)
	elements := sliceField(t, mapField(t, card, "body"), "elements")
	div, _ := elements[0].(map[string]any)
	text := mapField(t, div, "text")
	content, _ := text["content"].(string)
	if !strings.Contains(content, "4s") {
		t.Errorf("current-step content = %q, want it to contain '4s'", content)
	}
	if got := text["tag"]; got != "lark_md" {
		t.Errorf("text tag = %v, want lark_md (for grey font)", got)
	}
}

// TestBuildRunningCard_FinishedStepShowsPairedDuration asserts a
// step with both StartedAt + EndedAt renders its wall-clock duration,
// not a live clock against `now`.
func TestBuildRunningCard_FinishedStepShowsPairedDuration(t *testing.T) {
	started := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	steps := []StepInfo{
		{Tool: "Bash", Label: "done step", StartedAt: started, EndedAt: started.Add(3 * time.Second)},
	}
	card := BuildRunningCard("", steps, "", 0, started.Add(1*time.Hour))
	div, _ := sliceField(t, mapField(t, card, "body"), "elements")[0].(map[string]any)
	content, _ := mapField(t, div, "text")["content"].(string)
	if !strings.Contains(content, "3s") {
		t.Errorf("finished-step content = %q, want it to contain '3s'", content)
	}
	if strings.Contains(content, "1h") || strings.Contains(content, "60m") {
		t.Errorf("finished-step content = %q leaked live-clock fallback", content)
	}
}

// TestBuildRunningCard_EmptyEmitsPlaceholder is the degenerate case
// before any event has landed: no steps, no streaming text. The body
// element list must NOT be empty because Feishu rejects empty bodies.
func TestBuildRunningCard_EmptyEmitsPlaceholder(t *testing.T) {
	card := BuildRunningCard("", nil, "", 0, time.Time{})
	elements := sliceField(t, mapField(t, card, "body"), "elements")
	if len(elements) != 1 {
		t.Fatalf("elements = %d, want 1 placeholder", len(elements))
	}
}

func TestBuildPermissionCard_HasButtons(t *testing.T) {
	card := BuildPermissionCard("", "Bash", "rm -rf /tmp/cache", "perm-123")
	header := mapField(t, card, "header")
	assertString(t, header, "template", "orange")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	// Find the column_set element and confirm two buttons with the
	// expected action / permission_request_id.
	var columnSet map[string]any
	for _, raw := range elements {
		el, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if el["tag"] == "column_set" {
			columnSet = el
			break
		}
	}
	if columnSet == nil {
		t.Fatalf("BuildPermissionCard: column_set missing")
	}
	columns := sliceField(t, columnSet, "columns")
	if len(columns) != 2 {
		t.Fatalf("BuildPermissionCard: columns = %d, want 2", len(columns))
	}
	// First button: allow
	col0, _ := columns[0].(map[string]any)
	btnElements := sliceField(t, col0, "elements")
	if len(btnElements) == 0 {
		t.Fatalf("BuildPermissionCard: column[0] no elements")
	}
	btn0, _ := btnElements[0].(map[string]any)
	val0 := mapField(t, btn0, "value")
	if val0["action"] != "permission_allow" {
		t.Errorf("BuildPermissionCard: allow action = %v", val0["action"])
	}
	if val0["permission_request_id"] != "perm-123" {
		t.Errorf("BuildPermissionCard: allow perm_id = %v", val0["permission_request_id"])
	}
	// Second button: deny
	col1, _ := columns[1].(map[string]any)
	btnElements1 := sliceField(t, col1, "elements")
	btn1, _ := btnElements1[0].(map[string]any)
	val1 := mapField(t, btn1, "value")
	if val1["action"] != "permission_deny" {
		t.Errorf("BuildPermissionCard: deny action = %v", val1["action"])
	}
}

func TestBuildPermissionResultCard_Colors(t *testing.T) {
	allowed := BuildPermissionResultCard("", true)
	assertString(t, mapField(t, allowed, "header"), "template", "green")

	denied := BuildPermissionResultCard("", false)
	assertString(t, mapField(t, denied, "header"), "template", "red")
}

func TestBuildNoticeCard_Defaults(t *testing.T) {
	card := BuildNoticeCard("", "hello", NoticeColorInfo)
	header := mapField(t, card, "header")
	if got := mapField(t, header, "title")["content"]; got != FeishuCardTitle {
		t.Errorf("BuildNoticeCard: empty title fallback = %v, want %q", got, FeishuCardTitle)
	}
	assertString(t, header, "template", string(NoticeColorInfo))
}

// TestCardTitle_HonoursAgentName covers the 10-card title-parameter
// contract in one go. Each builder must (a) render the resolveCardTitle
// fallback (FeishuCardTitle) on empty / whitespace title and (b) put
// the trimmed caller-supplied Agent name into header.title.content when
// one is provided. Pinned in a single table-driven test rather than
// 20 individual tests because the contract is uniform — drift on any
// single builder is what we want to catch.
func TestCardTitle_HonoursAgentName(t *testing.T) {
	cards := map[string]struct {
		empty map[string]any
		named map[string]any
	}{
		"running":              {BuildRunningCard("", nil, "", 0, time.Time{}), BuildRunningCard("  Parsar  ", nil, "", 0, time.Time{})},
		"done":                 {BuildDoneCard("", "x", nil, "", 0, nil), BuildDoneCard("Parsar", "x", nil, "", 0, nil)},
		"error":                {BuildErrorCard("", "boom", "", "", ""), BuildErrorCard("Parsar", "boom", "", "", "")},
		"queue":                {BuildQueueCard("", 0), BuildQueueCard("Parsar", 0)},
		"notice":               {BuildNoticeCard("", "hi", NoticeColorInfo), BuildNoticeCard("Parsar", "hi", NoticeColorInfo)},
		"permission":           {BuildPermissionCard("", "Bash", "", "req-1"), BuildPermissionCard("Parsar", "Bash", "", "req-1")},
		"permission_result":    {BuildPermissionResultCard("", true), BuildPermissionResultCard("Parsar", true)},
		"credential_form":      {BuildCredentialFormCard("", []CredentialFormField{{Kind: "x"}}, "qkey"), BuildCredentialFormCard("Parsar", []CredentialFormField{{Kind: "x"}}, "qkey")},
		"credential_submitted": {BuildCredentialFormSubmittedCard(""), BuildCredentialFormSubmittedCard("Parsar")},
		"credential_rejected":  {BuildCredentialFormRejectedCard("", CredentialFormRejectOperatorMismatch), BuildCredentialFormRejectedCard("Parsar", CredentialFormRejectOperatorMismatch)},
	}
	for kind, pair := range cards {
		t.Run(kind, func(t *testing.T) {
			if got := mapField(t, mapField(t, pair.empty, "header"), "title")["content"]; got != FeishuCardTitle {
				t.Errorf("empty title: header.title.content = %v, want %q", got, FeishuCardTitle)
			}
			if got := mapField(t, mapField(t, pair.named, "header"), "title")["content"]; got != "Parsar" {
				t.Errorf("named title: header.title.content = %v, want %q", got, "Parsar")
			}
		})
	}
}

func TestBuildErrorCard_Shape(t *testing.T) {
	card := BuildErrorCard("", "runtime crashed", "", "", "")
	header := mapField(t, card, "header")
	assertString(t, header, "template", "red")
	tags := sliceField(t, header, "text_tag_list")
	tag, _ := tags[0].(map[string]any)
	if got := mapField(t, tag, "text")["content"]; got != "失败" {
		t.Errorf("BuildErrorCard: tag content = %v, want 失败", got)
	}
}

// TestBuildErrorCard_FirstLineOnly pins the "kill multi-line errors"
// behaviour: only the first line of `message` reaches the card body.
// A backend error string like "boom\nat foo.go:42\nat bar.go:17" would
// otherwise render as a wall of stack trace inside a tiny Feishu card,
// AND would leak internal file paths to whoever sees the chat.
func TestBuildErrorCard_FirstLineOnly(t *testing.T) {
	card := BuildErrorCard("", "boom\nat foo.go:42\nat bar.go:17", "", "", "")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	first, _ := elements[0].(map[string]any)
	got, _ := first["content"].(string)
	if got != "boom" {
		t.Errorf("BuildErrorCard body = %q, want %q (first line only)", got, "boom")
	}
}

// TestBuildErrorCard_DetailLink pins the deep-link path: when
// detailURL is non-empty, a markdown "查看本轮详情" link is appended
// beneath the first-line body so users can jump from the Feishu card
// to the full run page in the Parsar web UI. Empty detailURL must
// degrade gracefully to body-only (no broken/empty link).
func TestBuildErrorCard_DetailLink(t *testing.T) {
	card := BuildErrorCard("", "boom", "", "https://parsar.example.com/?admin=runs&id=run-x", "")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	first, _ := elements[0].(map[string]any)
	got, _ := first["content"].(string)
	wantSub := "[查看本轮详情](https://parsar.example.com/?admin=runs&id=run-x)"
	if !strings.Contains(got, wantSub) {
		t.Errorf("BuildErrorCard body = %q, want substring %q", got, wantSub)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("BuildErrorCard body = %q, want first-line %q preserved", got, "boom")
	}

	// Empty detailURL: link must NOT render.
	card2 := BuildErrorCard("", "boom", "", "", "")
	body2 := mapField(t, card2, "body")
	elements2 := sliceField(t, body2, "elements")
	first2, _ := elements2[0].(map[string]any)
	got2, _ := first2["content"].(string)
	if strings.Contains(got2, "查看本轮详情") {
		t.Errorf("BuildErrorCard body = %q, want no link when detailURL is empty", got2)
	}
}

// TestBuildErrorCard_GuestHint locks the contract that the
// unregistered-user "go register" hint stamped by VisibilityGate gets
// surfaced under the error body, appearing BEFORE the detail link so
// the call-to-action sits next to the failure rather than being buried
// under a markdown link. Empty hint must not introduce stray
// whitespace.
func TestBuildErrorCard_GuestHint(t *testing.T) {
	hint := "您还未绑定账号，请前往 Parsar 网页端完成绑定后再使用机器人。"
	card := BuildErrorCard("", "boom", "", "https://parsar.example.com/?admin=runs&id=run-x", hint)
	body := mapField(t, card, "body")
	first, _ := sliceField(t, body, "elements")[0].(map[string]any)
	got, _ := first["content"].(string)
	if !strings.Contains(got, hint) {
		t.Errorf("BuildErrorCard body = %q, want guest hint %q included", got, hint)
	}
	if idxHint, idxLink := strings.Index(got, hint), strings.Index(got, "[查看本轮详情]"); idxHint < 0 || idxLink < 0 || idxHint > idxLink {
		t.Errorf("BuildErrorCard body = %q: hint must precede the detail link (hint@%d link@%d)", got, idxHint, idxLink)
	}

	// Empty hint: body must NOT introduce stray blank-line padding that
	// would render as an empty paragraph in Feishu.
	cardNo := BuildErrorCard("", "boom", "", "", "")
	bodyNo := mapField(t, cardNo, "body")
	firstNo, _ := sliceField(t, bodyNo, "elements")[0].(map[string]any)
	gotNo, _ := firstNo["content"].(string)
	if strings.Contains(gotNo, "\n\n") {
		t.Errorf("BuildErrorCard body = %q, want no stray double-newline when hint is empty", gotNo)
	}
}

// TestBuildErrorCard_RawErrorOnlyOnGenericCopy locks the contract
// that the un-mapped error excerpt surfaces ONLY when message is one
// of the generic "请展开本轮错误详情..." copies. Other mapped messages
// carry an actionable hint already (401, rate-limit, …) and appending
// raw connector text would be noise / could leak internal jargon.
func TestBuildErrorCard_RawErrorOnlyOnGenericCopy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		message  string
		raw      string
		wantSeen bool
	}{
		{"generic default", "Agent 执行失败，请展开本轮错误详情查看具体原因。", "opencode: malformed JSON at step 7", true},
		{"opencode generic", "Agent 本地执行失败，请展开本轮错误详情查看原因。", "opencode exec exit status 2", true},
		{"specific 401", "模型服务身份验证失败，请确认 Secret 配置。", "401 unauthorized: invalid api key", false},
		{"specific rate limit", "模型服务被限流，请稍后重试。", "429 too many requests", false},
		{"empty raw", "Agent 执行失败，请展开本轮错误详情查看具体原因。", "", false},
		{"raw equal to body", "Agent 执行失败，请展开本轮错误详情查看具体原因。", "Agent 执行失败，请展开本轮错误详情查看具体原因。", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := BuildErrorCard("", tc.message, tc.raw, "", "")
			body := mapField(t, card, "body")
			elements := sliceField(t, body, "elements")
			first, _ := elements[0].(map[string]any)
			got, _ := first["content"].(string)
			has := strings.Contains(got, "错误详情:")
			if has != tc.wantSeen {
				t.Errorf("BuildErrorCard body = %q\n got 错误详情 prefix? %v, want %v", got, has, tc.wantSeen)
			}
		})
	}
}

// TestBuildErrorCard_RawErrorTruncatesAndKeepsFirstLineOnly pins two
// safety rails on the raw excerpt: only the first line surfaces (kill
// stack traces), and runes past the cap are middle-dropped with a "…"
// marker (kill wall-of-text on Feishu mobile).
func TestBuildErrorCard_RawErrorTruncatesAndKeepsFirstLineOnly(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", errorCardRawDetailLimit+50)
	rawMulti := long + "\nstack: foo.go:42\nstack: bar.go:17"
	card := BuildErrorCard("", "Agent 执行失败，请展开本轮错误详情查看具体原因。", rawMulti, "", "")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	first, _ := elements[0].(map[string]any)
	got, _ := first["content"].(string)
	if strings.Contains(got, "stack: foo.go") {
		t.Errorf("BuildErrorCard body leaked stack trace line: %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("BuildErrorCard body missing truncation marker: %q", got)
	}
	// Capped excerpt must not push body past detail-link region: a
	// non-empty detailURL still appends after the excerpt.
	cardWithLink := BuildErrorCard("", "Agent 执行失败，请展开本轮错误详情查看具体原因。", rawMulti, "https://parsar.example.com/?admin=runs&id=run-x", "")
	body2 := mapField(t, cardWithLink, "body")
	elements2 := sliceField(t, body2, "elements")
	first2, _ := elements2[0].(map[string]any)
	got2, _ := first2["content"].(string)
	if !strings.Contains(got2, "[查看本轮详情]") {
		t.Errorf("BuildErrorCard body lost detail link after raw excerpt: %q", got2)
	}
}

func TestBuildFeishuDoneCardContent_RoundTrip(t *testing.T) {
	got, err := BuildFeishuDoneCardContent("", "hello", nil, "", 1*time.Second, nil)
	if err != nil {
		t.Fatalf("BuildFeishuDoneCardContent err = %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	if parsed["schema"] != "2.0" {
		t.Errorf("schema = %v, want 2.0", parsed["schema"])
	}
}

func TestToolColor_MCPPrefix(t *testing.T) {
	if got := toolColor("mcp__github__create_issue"); got != "turquoise" {
		t.Errorf("toolColor mcp = %q, want turquoise", got)
	}
	if got := toolIcon("mcp__github__create_issue"); got != "robot_outlined" {
		t.Errorf("toolIcon mcp = %q, want robot_outlined", got)
	}
}

func TestFormatElapsed(t *testing.T) {
	cases := map[time.Duration]string{
		3 * time.Second:               "3s",
		59 * time.Second:              "59s",
		60 * time.Second:              "1m",
		90 * time.Second:              "1m30s",
		3*time.Minute + 5*time.Second: "3m5s",
		0:                             "0s",
	}
	for d, want := range cases {
		if got := FormatElapsed(d); got != want {
			t.Errorf("FormatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestToFeishuMarkdown_StripsBlockquoteAndHeadings(t *testing.T) {
	in := "# Title\n## Sub\nbody\n> quote\nplain"
	got := toFeishuMarkdown(in)
	for _, want := range []string{"**Title**", "**Sub**", "body", "quote", "plain"} {
		if !strings.Contains(got, want) {
			t.Errorf("toFeishuMarkdown missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "# ") || strings.Contains(got, "> ") {
		t.Errorf("toFeishuMarkdown did not strip markers: %q", got)
	}
}

// --- helpers ---

func mapField(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("missing key %q in %v", key, m)
	}
	out, ok := raw.(map[string]any)
	if !ok {
		t.Fatalf("key %q is %T, not map", key, raw)
	}
	return out
}

func sliceField(t *testing.T, m map[string]any, key string) []any {
	t.Helper()
	raw, ok := m[key]
	if !ok {
		t.Fatalf("missing slice key %q in %v", key, m)
	}
	// Two shapes show up depending on whether builder used
	// []map[string]any or []any literals. Normalize.
	switch v := raw.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, x := range v {
			out = append(out, x)
		}
		return out
	default:
		t.Fatalf("key %q is %T, not slice", key, raw)
		return nil
	}
}

func assertString(t *testing.T, m map[string]any, key, want string) {
	t.Helper()
	got, _ := m[key].(string)
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

// TestFormatTokensK pins the unit-consistency rules for the
// done-card footer's `<used>/<window>` rendering.
//
// The bug this guards against: the old footer formatted small
// values bare-int and the window always-k, producing strings like
// `169/200k` that visually parse as "169k vs 200k" (~85%) when the
// real ratio was 169/200000 (~0.08%). Anyone reading the screenshot
// got the wrong impression of how close the run was to the limit.
//
// Rules:
//   - 0 tokens     → "0k"   (still wears the unit so the / line parses uniformly)
//   - 1..999       → "<1k"  (don't drop the unit; signal "small but real")
//   - 1k..<10k     → "1.5k" (one decimal — the difference between 1k and 2k matters at this scale)
//   - 10k+         → "152k" (integer — sub-thousand resolution is noise here)
func TestFormatTokensK(t *testing.T) {
	cases := []struct {
		name string
		in   int
		want string
	}{
		{"zero", 0, "0k"},
		{"negative_clamps_to_zero", -5, "0k"},
		{"sub_1k_is_lt1k", 169, "<1k"},
		{"sub_1k_boundary_high", 999, "<1k"},
		{"exact_1k_one_decimal", 1000, "1.0k"},
		{"low_thousands_one_decimal", 1500, "1.5k"},
		{"just_under_10k_one_decimal", 9999, "10.0k"}, // 9999/1000 = 9.999 -> rounds to "10.0k" via float formatting
		{"exact_10k_integer", 10000, "10k"},
		{"large_integer", 152_000, "152k"},
		{"window_200k", 200_000, "200k"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatTokensK(tc.in)
			if got != tc.want {
				t.Errorf("formatTokensK(%d) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestDoneFooterElement_UnitConsistencyAndLt1Percent covers the
// rendering bug the user reported: a 169-token turn showed up as
// `169/200k (0%)`, which reads as either "85% full" (unit mismatch)
// or "no data" (0% rounding). The fix must produce a string that:
//
//   - uses the SAME unit on both sides of the slash
//   - reports `<1%` for any non-zero usage that rounds to 0% (so
//     "we have a value, it just rounds away" is unambiguous)
func TestDoneFooterElement_UnitConsistencyAndLt1Percent(t *testing.T) {
	// 169 tokens against a 200k window — exactly the bug screenshot.
	footer := doneFooterElement(8*time.Second, 0, &UsageStats{
		CostUSD:       0.62,
		ContextUsed:   169,
		ContextWindow: 200_000,
		Model:         "claude-opus-4-7-thinking-medium",
	})
	content, _ := footer["content"].(string)
	if !strings.Contains(content, "<1k/200k") {
		t.Errorf("footer should render `<1k/200k` (matching units), got %q", content)
	}
	if !strings.Contains(content, "(<1%)") {
		t.Errorf("non-zero usage rounding to 0%% should render `(<1%%)`, got %q", content)
	}
	if strings.Contains(content, "(0%)") {
		t.Errorf("footer must not render `(0%%)` for non-zero usage — the user can't tell missing-data from tiny, got %q", content)
	}
	// Sanity: the rest of the line still composes the way the user
	// asked for in the original DoneCard spec.
	if !strings.Contains(content, "8s") {
		t.Errorf("footer missing elapsed, got %q", content)
	}
	if !strings.Contains(content, "$0.62") {
		t.Errorf("footer missing cost, got %q", content)
	}
}

// TestDoneFooterElement_RealisticContext covers the OTHER side of
// the same fix — a normal-sized turn (say 152k against 200k) must
// still render the way the user originally asked for in the spec:
// `152k/200k (76%)`. This guards against the formatTokensK change
// drifting away from the spec at the >10k scale.
func TestDoneFooterElement_RealisticContext(t *testing.T) {
	footer := doneFooterElement(6*time.Minute+49*time.Second, 13, &UsageStats{
		CostUSD:       6.32,
		ContextUsed:   152_000,
		ContextWindow: 200_000,
		Model:         "claude-opus-4-8",
	})
	content, _ := footer["content"].(string)
	if !strings.Contains(content, "152k/200k") {
		t.Errorf("footer should render `152k/200k`, got %q", content)
	}
	if !strings.Contains(content, "(76%)") {
		t.Errorf("footer should render `(76%%)`, got %q", content)
	}
}

// TestDoneFooterElement_ExactZeroIsZeroPct guards the negative case
// of the <1% rule: if ContextUsed is genuinely zero, we WANT `(0%)`
// because there's no usage to report and "<1%" would be a lie.
func TestDoneFooterElement_ExactZeroIsZeroPct(t *testing.T) {
	footer := doneFooterElement(time.Second, 0, &UsageStats{
		CostUSD:       0,
		ContextUsed:   0,
		ContextWindow: 200_000,
		Model:         "claude-sonnet-4-5",
	})
	content, _ := footer["content"].(string)
	if !strings.Contains(content, "(0%)") {
		t.Errorf("zero usage should render `(0%%)` (not `<1%%`), got %q", content)
	}
}

// TestBuildCredentialFormCard_FormShape pins the structural promise: one
// outer form container with one input element per field plus a submit
// button whose action+qkey round-trip to the callback handler verbatim.
func TestBuildCredentialFormCard_FormShape(t *testing.T) {
	card := BuildCredentialFormCard("", []CredentialFormField{
		{Kind: "github_pat", Label: "GitHub Token", CapabilityName: "GitHub"},
		{Kind: "slack_bot_token", Label: "Slack Bot Token", CapabilityName: "Slack"},
	}, "qkey-abc")
	assertString(t, mapField(t, card, "header"), "template", "orange")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	if len(elements) != 1 {
		t.Fatalf("body should hold exactly one form element, got %d", len(elements))
	}
	form, _ := elements[0].(map[string]any)
	if form["tag"] != "form" {
		t.Fatalf("outer element should be tag=form, got %v", form["tag"])
	}
	formElements := sliceField(t, form, "elements")
	// Count inputs + the trailing submit button.
	var inputs []map[string]any
	var submit map[string]any
	for _, raw := range formElements {
		el, _ := raw.(map[string]any)
		switch el["tag"] {
		case "input":
			inputs = append(inputs, el)
		case "button":
			submit = el
		}
	}
	if len(inputs) != 2 {
		t.Fatalf("expected 2 inputs, got %d", len(inputs))
	}
	wantNames := map[string]bool{"credential_github_pat": false, "credential_slack_bot_token": false}
	for _, in := range inputs {
		name, _ := in["name"].(string)
		if _, ok := wantNames[name]; !ok {
			t.Errorf("unexpected input name %q", name)
			continue
		}
		wantNames[name] = true
		if in["input_type"] != "password" {
			t.Errorf("input %q should be password-masked, got %v", name, in["input_type"])
		}
		if in["required"] != true {
			t.Errorf("input %q should be required, got %v", name, in["required"])
		}
	}
	for name, seen := range wantNames {
		if !seen {
			t.Errorf("missing input %q", name)
		}
	}
	if submit == nil {
		t.Fatal("submit button missing")
	}
	// Pin the button's own `name` field. Feishu rejects the entire
	// form-container card with 200530 ("表单容器中的交互组件的 name 属性
	// 为空") when any name-bearing component inside the form is
	// missing its name. A prior regression silently dropped this on a
	// merge (commit ef9b1343, 2026-06-17) and the inputs alone passed
	// the existing shape assertions while the submit click broke in
	// prod. Assert directly so future drops fail loudly here instead.
	if got, _ := submit["name"].(string); got != "credential_form_submit" {
		t.Errorf("submit button name = %q, want \"credential_form_submit\" (Feishu form-container 200530 if empty)", got)
	}
	value, _ := submit["value"].(map[string]any)
	if value["action"] != "credential_form_submit" {
		t.Errorf("submit action = %v, want credential_form_submit", value["action"])
	}
	if value["qkey"] != "qkey-abc" {
		t.Errorf("submit qkey = %v, want qkey-abc", value["qkey"])
	}
}

// TestBuildCredentialFormCard_EmptyFieldsRenderNoSubmit pins the degenerate
// case: if the channel layer hands the builder an empty fields slice, no
// submit button should appear (nothing to send). H2 expanded this from
// "always render submit" to "render submit only when at least one input
// is fillable" — submitting an empty form would consume the qkey with no
// credentials and leave the user stuck.
func TestBuildCredentialFormCard_EmptyFieldsRenderNoSubmit(t *testing.T) {
	card := BuildCredentialFormCard("", nil, "qkey-empty")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	form, _ := elements[0].(map[string]any)
	formElements := sliceField(t, form, "elements")
	for _, raw := range formElements {
		el, _ := raw.(map[string]any)
		if el["tag"] == "button" {
			t.Fatalf("submit button must NOT appear when no input is renderable, got %+v", el)
		}
	}
}

// TestBuildCredentialFormSubmittedCard_Green confirms the after-submit
// card uses the success color so the user gets immediate feedback that
// the cleartext landed without seeing it echoed back.
//
// Body shape: the post-submit card keeps the same `tag: form` shell as
// the original orange card, AND the form holds a name-bearing
// placeholder button alongside the markdown. Both constraints come
// from Feishu's PATCH endpoint:
//
//  1. PATCH silently reverts schema-shape changes that drop the form
//     container (prod 2026-06-17: green flashed for a moment, then the
//     client snapped back to the orange form).
//  2. PATCH sends 200530 ("表单容器内交互组件的 name 属性为空") when
//     the form container holds zero name-bearing components — markdown
//     alone is not enough.
//
// Assert the form shell + the placeholder action so a refactor that
// drops either trips the test instead of regressing in prod.
func TestBuildCredentialFormSubmittedCard_Green(t *testing.T) {
	card := BuildCredentialFormSubmittedCard("")
	assertString(t, mapField(t, card, "header"), "template", "green")
	body := mapField(t, card, "body")
	elements := sliceField(t, body, "elements")
	if len(elements) == 0 {
		t.Fatal("submitted card body must have content")
	}
	outer, _ := elements[0].(map[string]any)
	if got, _ := outer["tag"].(string); got != "form" {
		t.Errorf("body.elements[0].tag = %q, want \"form\" so Feishu PATCH stays a content-only swap from the orange card", got)
	}
	if got, _ := outer["name"].(string); got != "credential_form" {
		t.Errorf("form name = %q, want \"credential_form\" (must match the orange card's form name)", got)
	}
	innerRaw, _ := outer["elements"].([]any)
	if len(innerRaw) < 2 {
		t.Fatalf("submitted card form shell must wrap at least a markdown + a placeholder button (Feishu 200530); got %d elements", len(innerRaw))
	}
	md, _ := innerRaw[0].(map[string]any)
	if got, _ := md["tag"].(string); got != "markdown" {
		t.Errorf("first form element tag = %q, want \"markdown\"", got)
	}
	content, _ := md["content"].(string)
	// Critical: the confirmation must not re-display credential plaintext.
	for _, leaky := range []string{"github_pat", "ghp_", "Bearer ", "xoxb-"} {
		if strings.Contains(content, leaky) {
			t.Errorf("confirmation card must not echo credentials; saw %q in %q", leaky, content)
		}
	}
	btn, _ := innerRaw[1].(map[string]any)
	if got, _ := btn["tag"].(string); got != "button" {
		t.Errorf("placeholder element tag = %q, want \"button\" (Feishu requires a name-bearing component inside the form container)", got)
	}
	if got, _ := btn["name"].(string); got == "" {
		t.Errorf("placeholder button has empty name — Feishu would 200530 the PATCH")
	}
	btnValue, _ := btn["value"].(map[string]any)
	if got, _ := btnValue["action"].(string); got != "credential_form_acknowledged" {
		t.Errorf("placeholder button action = %q, want \"credential_form_acknowledged\" so handleCardAction's dedicated branch catches the click", got)
	}
	// Feishu rejects form-container cards that don't carry at least one
	// `action_type: form_submit` button with code 230099 / ErrCode
	// 300123 ("there is no submit button in the form container, at
	// least one"). Asserting both fields here so a refactor that drops
	// either trips the test instead of the green PATCH bouncing in prod.
	if got, _ := btn["action_type"].(string); got != "form_submit" {
		t.Errorf("placeholder button action_type = %q, want \"form_submit\" (Feishu form container requires at least one submit button)", got)
	}
	if got, _ := btn["form_action_type"].(string); got != "submit" {
		t.Errorf("placeholder button form_action_type = %q, want \"submit\"", got)
	}
}

// TestCredentialFormCards_DeclareUpdateMulti pins the
// `config.update_multi: true` requirement Feishu's PATCH contract
// imposes on cards that get patched in place — the orange card and
// the green/red cards it's patched into must all carry it on both
// SEND and PATCH. Prod 2026-06-17: the orange card went out without
// the flag, so post-submit PATCH returned 200+code=0 but recipients
// never saw the green confirmation — the masked password input
// stayed visible to the whole chat.
func TestCredentialFormCards_DeclareUpdateMulti(t *testing.T) {
	t.Parallel()
	cards := map[string]map[string]any{
		"orange":    BuildCredentialFormCard("", []CredentialFormField{{Kind: "x"}}, "qkey"),
		"green":     BuildCredentialFormSubmittedCard(""),
		"red":       BuildCredentialFormRejectedCard("", CredentialFormRejectOperatorMismatch),
	}
	for name, card := range cards {
		t.Run(name, func(t *testing.T) {
			config, _ := card["config"].(map[string]any)
			if got, _ := config["update_multi"].(bool); !got {
				t.Errorf("config.update_multi = %v, want true (Feishu requires this on patched cards or PATCH silently no-ops for recipients)", config["update_multi"])
			}
		})
	}
}

// TestBuildQueueCard_PositionLabel pins the queue card's headline
// text across all positions we render. Prod 2026-06-15: a user with
// 1 running + 1 queued saw "排队中（第 2 位）" because the SQL
// counted the running sibling. After QueuePositionForRun was
// fixed to count queued-only, position=1 means "next, the running
// sibling is currently being served" — and the card's display path
// (position > 1 ? numbered : bare) maps that to plain "排队中".
func TestBuildQueueCard_PositionLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		position  int
		wantLabel string
	}{
		{"position 0 (compute failed) → bare 排队中", 0, "排队中"},
		{"position 1 (next) → bare 排队中, no '第 1 位'", 1, "排队中"},
		{"position 2 → 排队中（第 2 位）", 2, "排队中（第 2 位）"},
		{"position 5 → 排队中（第 5 位）", 5, "排队中（第 5 位）"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			card := BuildQueueCard("", tc.position)
			body, _ := card["body"].(map[string]any)
			elements, _ := body["elements"].([]map[string]any)
			if len(elements) == 0 {
				t.Fatalf("queue card has no body elements")
			}
			headline, _ := elements[0]["content"].(string)
			if headline != tc.wantLabel {
				t.Errorf("BuildQueueCard(%d) headline = %q, want %q", tc.position, headline, tc.wantLabel)
			}
		})
	}
}

// TestTruncateMarkdown_BelowCapPassesThrough asserts the no-op path
// for the common case: agent replies typically come well under 20 KB
// and must be returned byte-for-byte.
func TestTruncateMarkdown_BelowCapPassesThrough(t *testing.T) {
	s := strings.Repeat("a", markdownBodyCap)
	if got := truncateMarkdown(s); got != s {
		t.Errorf("at-cap input mutated: len(in)=%d len(out)=%d", len(s), len(got))
	}
	short := "hello world"
	if got := truncateMarkdown(short); got != short {
		t.Errorf("short input mutated: got %q want %q", got, short)
	}
}

// TestTruncateMarkdown_PreservesHeadAndTail is the regression guard
// for the head-only-truncate bug this MR fixes. The old behavior
// dropped everything after byte 20000, which meant a long StreamingCard
// body would freeze its visible content at the early prefix while
// later PATCHes silently swapped in the same prefix again. The new
// shape must keep both endpoints intact so the user always sees the
// model's latest tokens at the bottom.
func TestTruncateMarkdown_PreservesHeadAndTail(t *testing.T) {
	head := "HEAD_MARKER_前缀"
	tail := "尾部_TAIL_MARKER"
	body := head + strings.Repeat("X", markdownBodyCap*2) + tail
	out := truncateMarkdown(body)
	if len(out) > markdownBodyCap {
		t.Errorf("output len %d > cap %d", len(out), markdownBodyCap)
	}
	if !strings.HasPrefix(out, head) {
		t.Errorf("output lost head: prefix=%q", out[:min(len(head)*2, len(out))])
	}
	if !strings.HasSuffix(out, tail) {
		t.Errorf("output lost tail: suffix=%q", out[max(0, len(out)-len(tail)*2):])
	}
	if !strings.Contains(out, truncateMarkdownMarker) {
		t.Errorf("output missing middle marker")
	}
}

// TestTruncateMarkdown_UTF8Safe builds an input whose byte length
// crosses the cap right in the middle of a multi-byte Chinese rune.
// The output's head must end on a rune boundary (no 0xEF/0xBF/0xBD
// replacement runes, no half-rune trailing bytes).
func TestTruncateMarkdown_UTF8Safe(t *testing.T) {
	// One '中' is 3 bytes. Spam enough to overflow the cap multiple
	// times so the head boundary lands inside a rune for at least one
	// alignment.
	body := strings.Repeat("中", markdownBodyCap)
	out := truncateMarkdown(body)
	// strings.ContainsRune(out, utf8.RuneError) would do, but invalid
	// UTF-8 here would surface as raw bytes 0x80-0xBF unaccompanied
	// by a leading byte. Walk the runes manually.
	for i, r := range out {
		if r == 0xFFFD {
			t.Fatalf("truncated output contains replacement rune at byte %d (UTF-8 split)", i)
		}
	}
}

// TestTruncateMarkdown_TailWeightedSlightlyHeavier pins the
// "tail-favored" budget split documented in the function comment.
// Streaming replies need the most-recent tokens visible.
func TestTruncateMarkdown_TailWeightedSlightlyHeavier(t *testing.T) {
	body := strings.Repeat("a", markdownBodyCap*3)
	out := truncateMarkdown(body)
	parts := strings.SplitN(out, truncateMarkdownMarker, 2)
	if len(parts) != 2 {
		t.Fatalf("output did not split on marker: %d parts", len(parts))
	}
	headLen, tailLen := len(parts[0]), len(parts[1])
	if tailLen <= headLen {
		t.Errorf("tail (%d) should be longer than head (%d) for streaming weight", tailLen, headLen)
	}
}
