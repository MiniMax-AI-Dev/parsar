package feishu

import (
	"context"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/gateway/channel"
)

// newTestChannel lives in channel_test.go (same package).

func sampleSteps() []gateway.StepInfo {
	base := time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)
	return []gateway.StepInfo{
		{Tool: "Bash", Label: "ls -la", ID: "call_1", StartedAt: base, EndedAt: base.Add(2 * time.Second)},
		{Tool: "Read", Label: "open file", ID: "call_2", StartedAt: base.Add(3 * time.Second)},
	}
}

// TestRenderProgress_MatchesLegacyBuilder locks the adapter's RenderProgress
// output to the exact bytes the in-place buildMidRunCardContent path
// produces: MarshalCard(BuildRunningCard(...)). Pinning Now makes the
// comparison deterministic.
func TestRenderProgress_MatchesLegacyBuilder(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 30, 0, time.UTC)
	steps := sampleSteps()
	const (
		title     = "Demo Agent"
		streaming = "thinking out loud…"
	)
	elapsed := 30 * time.Second

	want, err := gateway.MarshalCard(gateway.BuildRunningCard(title, steps, streaming, elapsed, now))
	if err != nil {
		t.Fatalf("legacy BuildRunningCard: %v", err)
	}

	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title:         title,
		Steps:         steps,
		StreamingText: streaming,
		Elapsed:       elapsed,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	if card.MIME != feishuCardMIME {
		t.Errorf("MIME = %q, want %q", card.MIME, feishuCardMIME)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("RenderProgress payload diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderProgress_DefaultsNow asserts a zero Now does not panic and yields
// a non-empty card (the exact bytes depend on wall-clock, so we only check
// it renders).
func TestRenderProgress_DefaultsNow(t *testing.T) {
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title: "Demo Agent",
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	if len(card.Payload) == 0 {
		t.Fatal("RenderProgress with zero Now produced empty payload")
	}
}

// TestRenderTerminal_DoneMatchesLegacyBuilder locks the success path to
// MarshalCard(BuildDoneCard(...)). StreamingText is trimmed by both paths.
func TestRenderTerminal_DoneMatchesLegacyBuilder(t *testing.T) {
	steps := sampleSteps()
	usage := &gateway.UsageStats{CostUSD: 0.42, ContextUsed: 1200, ContextWindow: 200000, Model: "claude"}
	const (
		title     = "Demo Agent"
		streaming = "  final answer  "
		thinking  = "internal reasoning"
	)
	elapsed := 90 * time.Second

	want, err := gateway.MarshalCard(gateway.BuildDoneCard(title, "final answer", steps, thinking, elapsed, usage))
	if err != nil {
		t.Fatalf("legacy BuildDoneCard: %v", err)
	}

	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:         title,
		StreamingText: streaming,
		Steps:         steps,
		Thinking:      thinking,
		Elapsed:       elapsed,
		Usage:         usage,
		Success:       true,
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("Done payload diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderTerminal_ErrorMatchesLegacyBuilder locks the failure path to
// BuildFeishuErrorCardContent with the explicit message supplied.
func TestRenderTerminal_ErrorMatchesLegacyBuilder(t *testing.T) {
	const (
		title  = "Demo Agent"
		errMsg = "tool exploded"
		rawErr = "stack trace …"
		detail = "https://example.com/runs/abc"
		guest  = "register at …"
	)

	want, err := gateway.BuildFeishuErrorCardContent(title, errMsg, rawErr, detail, guest)
	if err != nil {
		t.Fatalf("legacy BuildFeishuErrorCardContent: %v", err)
	}

	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:        title,
		Success:      false,
		ErrorMessage: errMsg,
		RawError:     rawErr,
		RunDetailURL: detail,
		GuestHint:    guest,
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("Error payload diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderTerminal_ErrorFallbackMessage asserts an empty ErrorMessage falls
// back to the same copy buildFinalCardForRun applies.
func TestRenderTerminal_ErrorFallbackMessage(t *testing.T) {
	const title = "Demo Agent"

	want, err := gateway.BuildFeishuErrorCardContent(title, errCardFallbackMessage, "", "", "")
	if err != nil {
		t.Fatalf("legacy BuildFeishuErrorCardContent: %v", err)
	}

	card, err := newTestChannel().RenderTerminal(context.Background(), channel.ReplyTarget{}, channel.TerminalResult{
		Title:   title,
		Success: false,
	})
	if err != nil {
		t.Fatalf("RenderTerminal: %v", err)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("fallback Error payload diverged\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderPermission_MatchesLegacyBuilder locks the adapter's RenderPermission
// to the exact bytes BuildFeishuPermissionCardContent produces, so the Feishu
// hot path (which still builds inline) and this interface method never drift.
func TestRenderPermission_MatchesLegacyBuilder(t *testing.T) {
	const (
		title     = "Demo Agent"
		toolName  = "Bash"
		toolInput = "rm -rf /tmp/x"
		reqID     = "perm-req-1"
	)
	want, err := gateway.BuildFeishuPermissionCardContent(title, toolName, toolInput, reqID)
	if err != nil {
		t.Fatalf("legacy BuildFeishuPermissionCardContent: %v", err)
	}
	card, err := newTestChannel().RenderPermission(context.Background(), channel.ReplyTarget{}, channel.PermissionRequest{
		Title:     title,
		ToolName:  toolName,
		ToolInput: toolInput,
		RequestID: reqID,
	})
	if err != nil {
		t.Fatalf("RenderPermission: %v", err)
	}
	if card.MIME != feishuCardMIME {
		t.Errorf("MIME = %q, want %q", card.MIME, feishuCardMIME)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("RenderPermission diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderChoiceForm_MatchesLegacyBuilder locks the adapter's RenderChoiceForm
// to BuildFeishuPromptForUserChoiceCardContent after mapping the neutral
// channel.ChoiceQuestion list onto the gateway card-question shape.
func TestRenderChoiceForm_MatchesLegacyBuilder(t *testing.T) {
	const (
		title = "Pick options"
		reqID = "pfuc-1"
	)
	neutral := []channel.ChoiceQuestion{
		{Header: "Q1", Question: "Single?", MultiSelect: false, Options: []string{"a", "b"}},
		{Header: "Q2", Question: "Many?", MultiSelect: true, Options: []string{"x", "y", "z"}},
	}
	legacy := []gateway.PromptForUserChoiceCardQuestion{
		{Header: "Q1", Question: "Single?", MultiSelect: false, Options: []gateway.PromptForUserChoiceCardOption{{Label: "a"}, {Label: "b"}}},
		{Header: "Q2", Question: "Many?", MultiSelect: true, Options: []gateway.PromptForUserChoiceCardOption{{Label: "x"}, {Label: "y"}, {Label: "z"}}},
	}
	want, err := gateway.BuildFeishuPromptForUserChoiceCardContent(title, legacy, reqID)
	if err != nil {
		t.Fatalf("legacy BuildFeishuPromptForUserChoiceCardContent: %v", err)
	}
	card, err := newTestChannel().RenderChoiceForm(context.Background(), channel.ReplyTarget{}, channel.ChoiceForm{
		Title:     title,
		RequestID: reqID,
		Questions: neutral,
	})
	if err != nil {
		t.Fatalf("RenderChoiceForm: %v", err)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("RenderChoiceForm diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}

// TestRenderCredentialForm_MatchesLegacyBuilder locks the adapter's
// RenderCredentialForm to MarshalCard(BuildCredentialFormCard(...)).
func TestRenderCredentialForm_MatchesLegacyBuilder(t *testing.T) {
	const (
		title = "Add credentials"
		qkey  = "qkey-abc"
	)
	fields := []gateway.CredentialFormField{
		{Kind: "token", Label: "API Token", CapabilityName: "github", Placeholder: "ghp_…"},
		{Kind: "token", Label: "Slack Token", CapabilityName: "slack"},
	}
	want, err := gateway.MarshalCard(gateway.BuildCredentialFormCard(title, fields, qkey))
	if err != nil {
		t.Fatalf("legacy BuildCredentialFormCard: %v", err)
	}
	card, err := newTestChannel().RenderCredentialForm(context.Background(), channel.ReplyTarget{}, channel.CredentialForm{
		Title:  title,
		Qkey:   qkey,
		Fields: fields,
	})
	if err != nil {
		t.Fatalf("RenderCredentialForm: %v", err)
	}
	if got := string(card.Payload); got != want {
		t.Errorf("RenderCredentialForm diverged from legacy builder\n got: %s\nwant: %s", got, want)
	}
}
