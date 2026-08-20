package agentdaemon

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

type fakeHistoryReader struct {
	messages []store.ConversationHistoryMessage
	err      error
	calls    int
	gotLimit int32
	gotConv  string
}

func (f *fakeHistoryReader) ListRecentConversationHistory(_ context.Context, conversationID string, limit int32) ([]store.ConversationHistoryMessage, error) {
	f.calls++
	f.gotConv = conversationID
	f.gotLimit = limit
	return f.messages, f.err
}

func historyMessages() []store.ConversationHistoryMessage {
	base := time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)
	return []store.ConversationHistoryMessage{
		{ID: "m1", SenderType: "user", Content: "add a health endpoint", CreatedAt: base},
		{ID: "m2", SenderType: "agent", Content: "added /healthz in api.go", CreatedAt: base.Add(time.Minute)},
		{ID: "m3", SenderType: "user", Content: "now add a readiness probe", CreatedAt: base.Add(2 * time.Minute)},
	}
}

func historyInput() connector.PromptInput {
	return connector.PromptInput{
		RunID:                 "run-1",
		ConversationID:        "conv-1",
		AgentID:               "agt-1",
		TriggerMessageID:      "m3",
		TriggerMessageContent: "now add a readiness probe",
	}
}

func noResumeKind() store.AgentDaemonSupportedAgentKind {
	return store.AgentDaemonSupportedAgentKind{
		Kind:      "deepseek_harness",
		Available: true,
	}
}

func resumeKind() store.AgentDaemonSupportedAgentKind {
	info := store.AgentDaemonSupportedAgentKind{Kind: "claude_code", Available: true}
	info.Capabilities.Resume = true
	return info
}

// The transcript exists only for engines that start every turn from zero.
// An engine that resumes its own session would receive the same history
// twice — once from its session, once from us.
func TestApplyConversationHistoryInjection_ResumeCapableEngineSkipped(t *testing.T) {
	reader := &fakeHistoryReader{messages: historyMessages()}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	opts := map[string]any{}

	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), resumeKind())

	if reader.calls != 0 {
		t.Fatalf("resume-capable engine must not even read history; calls=%d", reader.calls)
	}
	if _, ok := opts["system_prompt"]; ok {
		t.Fatalf("system_prompt must stay absent: %#v", opts)
	}
}

func TestResumeFallbackPromptCarriesHistoryOnlyForAnExistingSession(t *testing.T) {
	reader := &fakeHistoryReader{messages: historyMessages()}
	c := &Connector{conversationHistory: reader, log: discardLogger()}

	got := c.resumeFallbackPrompt(context.Background(), historyInput(), resumeKind(), "thread-1", map[string]any{})
	if !strings.Contains(got, "Assistant: added /healthz in api.go") {
		t.Fatalf("fallback history = %q", got)
	}
	if strings.Contains(got, "now add a readiness probe") {
		t.Fatalf("fallback repeated the trigger: %q", got)
	}
	if reader.calls != 1 {
		t.Fatalf("history reads = %d, want 1", reader.calls)
	}

	if got := c.resumeFallbackPrompt(context.Background(), historyInput(), resumeKind(), "", map[string]any{}); got != "" {
		t.Fatalf("fresh session fallback = %q", got)
	}
	if got := c.resumeFallbackPrompt(context.Background(), historyInput(), noResumeKind(), "thread-1", map[string]any{}); got != "" {
		t.Fatalf("non-resume fallback = %q", got)
	}
}

func TestResumeFallbackPromptRespectsOverrideSystemPrompt(t *testing.T) {
	reader := &fakeHistoryReader{messages: historyMessages()}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	got := c.resumeFallbackPrompt(context.Background(), historyInput(), resumeKind(), "thread-1", map[string]any{
		"override_system_prompt": "only this",
	})
	if got != "" || reader.calls != 0 {
		t.Fatalf("override fallback=%q reads=%d", got, reader.calls)
	}
}

func TestApplyConversationHistoryInjection_NoResumeEngineGetsTranscript(t *testing.T) {
	reader := &fakeHistoryReader{messages: historyMessages()}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	opts := map[string]any{"system_prompt": "be terse"}

	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), noResumeKind())

	if reader.calls != 1 || reader.gotConv != "conv-1" {
		t.Fatalf("reader calls=%d conv=%q", reader.calls, reader.gotConv)
	}
	if reader.gotLimit != historyTurnLimit {
		t.Fatalf("limit = %d, want %d", reader.gotLimit, historyTurnLimit)
	}
	prompt, _ := opts["system_prompt"].(string)
	if !strings.HasPrefix(prompt, "be terse\n\n") {
		t.Fatalf("existing system prompt must be preserved first: %q", prompt)
	}
	if !strings.Contains(prompt, "User: add a health endpoint") {
		t.Fatalf("missing user turn: %q", prompt)
	}
	if !strings.Contains(prompt, "Assistant: added /healthz in api.go") {
		t.Fatalf("missing assistant turn: %q", prompt)
	}
	// The current task is already the prompt; echoing it as history would
	// invite the engine to answer it twice.
	if strings.Contains(prompt, "now add a readiness probe") {
		t.Fatalf("trigger message must be excluded: %q", prompt)
	}
}

func TestApplyConversationHistoryInjection_OverrideSystemPromptWins(t *testing.T) {
	reader := &fakeHistoryReader{messages: historyMessages()}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	opts := map[string]any{"override_system_prompt": "you are root"}

	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), noResumeKind())

	if reader.calls != 0 {
		t.Fatalf("override must short-circuit before the read; calls=%d", reader.calls)
	}
	if _, ok := opts["system_prompt"]; ok {
		t.Fatalf("override must not gain a system_prompt: %#v", opts)
	}
}

// A history read failure costs context; failing the prompt costs the turn.
func TestApplyConversationHistoryInjection_ReadErrorIsSwallowed(t *testing.T) {
	reader := &fakeHistoryReader{err: errors.New("db down")}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	opts := map[string]any{"system_prompt": "base"}

	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), noResumeKind())

	if got := opts["system_prompt"]; got != "base" {
		t.Fatalf("system_prompt = %v, want untouched", got)
	}
}

func TestApplyConversationHistoryInjection_FirstTurnAddsNothing(t *testing.T) {
	// Only the trigger message exists yet.
	reader := &fakeHistoryReader{messages: []store.ConversationHistoryMessage{
		{ID: "m3", SenderType: "user", Content: "now add a readiness probe"},
	}}
	c := &Connector{conversationHistory: reader, log: discardLogger()}
	opts := map[string]any{}

	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), noResumeKind())

	if _, ok := opts["system_prompt"]; ok {
		t.Fatalf("a first turn has no history to inject: %#v", opts)
	}
}

func TestApplyConversationHistoryInjection_NilReaderIsNoOp(t *testing.T) {
	c := &Connector{log: discardLogger()}
	opts := map[string]any{}
	c.applyConversationHistoryInjection(context.Background(), opts, historyInput(), noResumeKind())
	if len(opts) != 0 {
		t.Fatalf("unwired reader must change nothing: %#v", opts)
	}
}

func TestRenderConversationHistory_OldestFirstAndBounded(t *testing.T) {
	long := strings.Repeat("x", historyMessageBudgetBytes*2)
	messages := []store.ConversationHistoryMessage{
		{ID: "m1", SenderType: "user", Content: "first"},
		{ID: "m2", SenderType: "external", Content: "im guest asks"},
		{ID: "m3", SenderType: "agent", Content: long},
		{ID: "m4", SenderType: "user", Content: "  "},
	}
	block := renderConversationHistory(messages, "", "unrelated trigger")

	firstIdx := strings.Index(block, "first")
	guestIdx := strings.Index(block, "im guest asks")
	if firstIdx == -1 || guestIdx == -1 || firstIdx > guestIdx {
		t.Fatalf("turns must render oldest-first: %q", block)
	}
	// An unregistered IM sender is still a human on the other side.
	if !strings.Contains(block, "User: im guest asks") {
		t.Fatalf("external sender must render as User: %q", block)
	}
	if !strings.Contains(block, "… [truncated]") {
		t.Fatalf("oversized turn must be truncated: %q", block[:200])
	}
	if strings.Contains(block, "User:   ") {
		t.Fatalf("blank turns must be dropped: %q", block)
	}
	if len(block) > historyTotalBudgetBytes {
		t.Fatalf("block is %d bytes, over the %d budget", len(block), historyTotalBudgetBytes)
	}
}

func TestRenderConversationHistory_DropsOldestUntilItFits(t *testing.T) {
	chunk := strings.Repeat("y", historyMessageBudgetBytes)
	messages := make([]store.ConversationHistoryMessage, 0, historyTurnLimit)
	for i := 0; i < historyTurnLimit; i++ {
		messages = append(messages, store.ConversationHistoryMessage{
			ID:         string(rune('a' + i)),
			SenderType: "user",
			Content:    chunk,
		})
	}
	// Mark the newest turn so we can prove it survived the trim.
	messages[len(messages)-1].Content = "NEWEST " + chunk

	block := renderConversationHistory(messages, "", "")
	if len(block) > historyTotalBudgetBytes {
		t.Fatalf("block is %d bytes, over the %d budget", len(block), historyTotalBudgetBytes)
	}
	if !strings.Contains(block, "NEWEST") {
		t.Fatalf("the newest turn must survive the trim: %q", block[:200])
	}
}

// Without a stored message id (synthesized prompts), the content fallback
// still has to recognise the task — including the gateway's quoted-chain
// prefix, which rides on the dispatched content but not on the stored row.
func TestRenderConversationHistory_TriggerFallbackHandlesQuotedPrefix(t *testing.T) {
	messages := []store.ConversationHistoryMessage{
		{ID: "m1", SenderType: "agent", Content: "earlier answer"},
		{ID: "m2", SenderType: "user", Content: "please retry"},
	}
	block := renderConversationHistory(messages, "", "[Quoted message] ...\n\nplease retry")
	if strings.Contains(block, "please retry") {
		t.Fatalf("quoted-prefixed trigger must still be excluded: %q", block)
	}
	if !strings.Contains(block, "earlier answer") {
		t.Fatalf("older turns must remain: %q", block)
	}
}

func TestRenderConversationHistory_EmptyInputRendersNothing(t *testing.T) {
	if got := renderConversationHistory(nil, "", ""); got != "" {
		t.Fatalf("expected empty render, got %q", got)
	}
}

func TestTruncateHistoryTextKeepsValidUTF8(t *testing.T) {
	text := strings.Repeat("汉", 40)
	got := truncateHistoryText(text, 30)
	if len(got) > 30 {
		t.Fatalf("truncated text is %d bytes, over budget", len(got))
	}
	if !strings.HasSuffix(got, "… [truncated]") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
	if strings.ContainsRune(got, '\uFFFD') {
		t.Fatalf("truncation broke a rune: %q", got)
	}
}
