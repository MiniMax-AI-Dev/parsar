package discord

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

func sampleSteps() []gateway.StepInfo {
	base := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	return []gateway.StepInfo{
		{Tool: "Bash", Label: "ls -la", ID: "call_1", StartedAt: base, EndedAt: base.Add(2 * time.Second)},
		{Tool: "Read", Label: "open file", ID: "call_2", StartedAt: base.Add(3 * time.Second), EndedAt: base.Add(4 * time.Second)},
	}
}

func decodeCard(t *testing.T, card channel.Card) deMessage {
	t.Helper()
	if card.MIME != discordCardMIME {
		t.Fatalf("MIME = %q, want %q", card.MIME, discordCardMIME)
	}
	var msg deMessage
	if err := json.Unmarshal(card.Payload, &msg); err != nil {
		t.Fatalf("payload is not valid Discord message JSON: %v\n%s", err, card.Payload)
	}
	return msg
}

// TestRenderProgress_GoldenMinimal locks the exact embed JSON for a minimal
// in-flight card (title only, no steps/streaming, zero elapsed).
func TestRenderProgress_GoldenMinimal(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 30, 0, time.UTC)
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title: "Demo",
		Now:   now,
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	const want = `{"content":"Demo is running","embeds":[` +
		`{"title":"Demo","color":5793266,"footer":{"text":"⏳ Running"}}` +
		`]}`
	if got := string(card.Payload); got != want {
		t.Errorf("golden mismatch\n got: %s\nwant: %s", got, want)
	}
}

func TestRenderProgress_RichStructure(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 30, 0, time.UTC)
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title:         "Demo Agent",
		Steps:         sampleSteps(),
		StreamingText: "thinking **out** loud",
		Elapsed:       30 * time.Second,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	msg := decodeCard(t, card)
	if len(msg.Embeds) != 1 {
		t.Fatalf("want 1 embed, got %d", len(msg.Embeds))
	}
	desc := msg.Embeds[0].Description
	// Step line: Bash emoji + label + 2s duration.
	if !strings.Contains(desc, "💻 ls -la · 2s") {
		t.Errorf("missing Bash step line; desc=%q", desc)
	}
	// Markdown passes through untouched (no mrkdwn rewrite).
	if !strings.Contains(desc, "thinking **out** loud") {
		t.Errorf("streaming text should pass through; desc=%q", desc)
	}
	// Footer status carries the elapsed.
	if msg.Embeds[0].Footer == nil || !strings.Contains(msg.Embeds[0].Footer.Text, "⏳ Running · 30s") {
		t.Errorf("missing running footer with elapsed; embed=%+v", msg.Embeds[0])
	}
}

func TestRenderProgress_DoneColorAndFooter(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 30, 0, time.UTC)
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title: "Demo",
		Done:  true,
		Now:   now,
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	msg := decodeCard(t, card)
	if msg.Embeds[0].Color != colorDone {
		t.Errorf("done color = %d, want %d", msg.Embeds[0].Color, colorDone)
	}
	if !strings.Contains(msg.Embeds[0].Footer.Text, "✅ Done") {
		t.Errorf("done footer = %q", msg.Embeds[0].Footer.Text)
	}
}

func TestRenderTerminal_DoneStructure(t *testing.T) {
	usage := &gateway.UsageStats{CostUSD: 0.42, ContextUsed: 1200, ContextWindow: 200000, Model: "claude"}
	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:         "Demo Agent",
		StreamingText: "  final answer  ",
		Steps:         sampleSteps(),
		Thinking:      "internal reasoning",
		Usage:         usage,
		Success:       true,
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	msg := decodeCard(t, card)
	desc := msg.Embeds[0].Description
	if !strings.Contains(desc, "**Thinking**") {
		t.Errorf("missing thinking section; desc=%q", desc)
	}
	if !strings.Contains(desc, "final answer") { // streaming trimmed
		t.Errorf("missing streaming answer; desc=%q", desc)
	}
	if msg.Embeds[0].Color != colorDone {
		t.Errorf("done color = %d, want %d", msg.Embeds[0].Color, colorDone)
	}
	if msg.Embeds[0].Footer == nil || !strings.Contains(msg.Embeds[0].Footer.Text, "$0.42") {
		t.Errorf("missing usage footer; embed=%+v", msg.Embeds[0])
	}
	if msg.Content != "final answer" {
		t.Errorf("fallback content = %q, want first line of answer", msg.Content)
	}
}

func TestRenderTerminal_ErrorStructure(t *testing.T) {
	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:        "Demo Agent",
		Success:      false,
		ErrorMessage: "tool exploded",
		RawError:     "stack <trace>",
		RunDetailURL: "https://example.com/runs/abc",
		GuestHint:    "register at example.com",
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	msg := decodeCard(t, card)
	desc := msg.Embeds[0].Description
	if !strings.Contains(desc, "❌ **tool exploded**") {
		t.Errorf("missing error headline; desc=%q", desc)
	}
	if !strings.Contains(desc, "stack <trace>") {
		t.Errorf("missing raw error; desc=%q", desc)
	}
	if !strings.Contains(desc, "[View run details](https://example.com/runs/abc)") {
		t.Errorf("missing run-detail deep link; desc=%q", desc)
	}
	if !strings.Contains(desc, "register at example.com") {
		t.Errorf("missing guest hint; desc=%q", desc)
	}
	if msg.Embeds[0].Color != colorError {
		t.Errorf("error color = %d, want %d", msg.Embeds[0].Color, colorError)
	}
	if msg.Content != "Demo Agent — tool exploded" {
		t.Errorf("fallback content = %q", msg.Content)
	}
}

func TestRenderTerminal_ErrorFallbackMessage(t *testing.T) {
	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:   "Demo Agent",
		Success: false, // empty ErrorMessage
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	msg := decodeCard(t, card)
	if !strings.Contains(msg.Embeds[0].Description, errCardFallbackMessage) {
		t.Errorf("empty ErrorMessage must fall back to default copy; desc=%q", msg.Embeds[0].Description)
	}
}

// TestRenderPermission_Buttons asserts the Allow/Deny buttons carry the neutral
// action ids packed into their custom_id and the right styles.
func TestRenderPermission_Buttons(t *testing.T) {
	card, err := newTestChannel().RenderPermission(context.Background(), channel.ReplyTarget{}, channel.PermissionRequest{
		Title:     "Demo Agent",
		ToolName:  "Bash",
		ToolInput: "rm -rf /tmp/x",
		RequestID: "req-123",
	})
	if err != nil {
		t.Fatalf("RenderPermission: %v", err)
	}
	msg := decodeCard(t, card)
	if len(msg.Components) != 1 || len(msg.Components[0].Components) != 2 {
		t.Fatalf("want 1 row with 2 buttons, got %+v", msg.Components)
	}
	allow := buttonAt(t, msg, 0, 0)
	deny := buttonAt(t, msg, 0, 1)
	if allow.CustomID != "permission_allow:req-123" || allow.Style != buttonSuccess {
		t.Errorf("allow button = %+v", allow)
	}
	if deny.CustomID != "permission_deny:req-123" || deny.Style != buttonDanger {
		t.Errorf("deny button = %+v", deny)
	}
}

// TestRenderChoiceForm_SelectsAndSubmit asserts one select per question plus a
// reserved submit row, with the neutral action ids in the custom_ids.
func TestRenderChoiceForm_SelectsAndSubmit(t *testing.T) {
	card, err := newTestChannel().RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{
		Title:     "Pick",
		RequestID: "form-9",
		Questions: []channel.ChoiceQuestion{
			{Header: "Env", Question: "Which env?", Options: []string{"dev", "prod"}},
			{Header: "Mode", Question: "Which mode?", MultiSelect: true, Options: []string{"a", "b", "c"}},
		},
	})
	if err != nil {
		t.Fatalf("RenderChoiceForm: %v", err)
	}
	msg := decodeCard(t, card)
	// 2 selects + 1 submit row.
	if len(msg.Components) != 3 {
		t.Fatalf("want 3 rows (2 selects + submit), got %d", len(msg.Components))
	}
	sel0 := selectAt(t, msg, 0)
	if sel0.CustomID != "ask_user_choice_pick:0" || sel0.MaxValues != 1 {
		t.Errorf("select 0 = %+v", sel0)
	}
	sel1 := selectAt(t, msg, 1)
	if sel1.CustomID != "ask_user_choice_pick:1" || sel1.MaxValues != 3 { // multiselect → len(options)
		t.Errorf("select 1 = %+v", sel1)
	}
	submit := buttonAt(t, msg, 2, 0)
	if submit.CustomID != "ask_user_choice_submit:form-9" {
		t.Errorf("submit button = %+v", submit)
	}
}

// TestRenderChoiceForm_CapsSelects asserts > (maxRows-1) questions are dropped
// with an omission note and the submit row still renders.
func TestRenderChoiceForm_CapsSelects(t *testing.T) {
	var qs []channel.ChoiceQuestion
	for i := 0; i < 6; i++ {
		qs = append(qs, channel.ChoiceQuestion{Header: "Q", Question: "q", Options: []string{"x"}})
	}
	card, err := newTestChannel().RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{
		Title: "Many", RequestID: "f", Questions: qs,
	})
	if err != nil {
		t.Fatalf("RenderChoiceForm: %v", err)
	}
	msg := decodeCard(t, card)
	if len(msg.Components) != discordMaxActionRows {
		t.Fatalf("rows = %d, want capped at %d", len(msg.Components), discordMaxActionRows)
	}
	if !strings.Contains(msg.Embeds[0].Description, "omitted") {
		t.Errorf("missing omission note; desc=%q", msg.Embeds[0].Description)
	}
}

// TestRenderCredentialForm_FieldsAndButton asserts fields render as embed fields
// and the submit button carries the qkey under the neutral action id.
func TestRenderCredentialForm_FieldsAndButton(t *testing.T) {
	card, err := newTestChannel().RenderCredentialForm(context.Background(), channel.ReplyTarget{}, channel.CredentialForm{
		Title: "Creds",
		Qkey:  "qk-1",
		Fields: []gateway.CredentialFormField{
			{Label: "API Key", Placeholder: "sk-..."},
		},
	})
	if err != nil {
		t.Fatalf("RenderCredentialForm: %v", err)
	}
	msg := decodeCard(t, card)
	if len(msg.Embeds[0].Fields) != 1 || msg.Embeds[0].Fields[0].Name != "API Key" {
		t.Fatalf("fields = %+v", msg.Embeds[0].Fields)
	}
	btn := buttonAt(t, msg, 0, 0)
	if btn.CustomID != "credential_form_submit:qk-1" {
		t.Errorf("submit button = %+v", btn)
	}
}

func TestCustomID_CapsAt100(t *testing.T) {
	long := strings.Repeat("a", 200)
	id := customID("permission_allow", long)
	idLen := len([]rune(id))
	if idLen > discordCustomIDLimit {
		t.Errorf("custom_id len = %d, want ≤ %d", idLen, discordCustomIDLimit)
	}
	// No-value form returns the bare action.
	if got := customID("permission_allow", ""); got != "permission_allow" {
		t.Errorf("bare action = %q", got)
	}
}

func TestEmptyTitleFallsBack(t *testing.T) {
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{Now: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	msg := decodeCard(t, card)
	if msg.Embeds[0].Title != defaultCardTitle {
		t.Errorf("empty title = %q, want %q", msg.Embeds[0].Title, defaultCardTitle)
	}
}

// --- decode helpers ----------------------------------------------------------

func buttonAt(t *testing.T, msg deMessage, row, idx int) deButton {
	t.Helper()
	raw, err := json.Marshal(msg.Components[row].Components[idx])
	if err != nil {
		t.Fatalf("marshal component: %v", err)
	}
	var b deButton
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatalf("component [%d][%d] is not a button: %v", row, idx, err)
	}
	if b.Type != componentButton {
		t.Fatalf("component [%d][%d] type = %d, want button", row, idx, b.Type)
	}
	return b
}

func selectAt(t *testing.T, msg deMessage, row int) deSelect {
	t.Helper()
	raw, err := json.Marshal(msg.Components[row].Components[0])
	if err != nil {
		t.Fatalf("marshal component: %v", err)
	}
	var s deSelect
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("row %d component is not a select: %v", row, err)
	}
	if s.Type != componentStringSelect {
		t.Fatalf("row %d type = %d, want select", row, s.Type)
	}
	return s
}
