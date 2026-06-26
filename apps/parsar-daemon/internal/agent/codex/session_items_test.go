package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestDispatchStartedItem_Bash(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{
		Type: "commandExecution", ID: "c1", Command: "ls /tmp", Cwd: "/tmp",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("len envs = %d, want 1", len(envs))
	}
	if envs[0].Type != proto.TypeToolCall {
		t.Fatalf("type = %s, want tool_call", envs[0].Type)
	}
	var p proto.ToolCallPayload
	if err := envs[0].DecodePayload(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "Bash" || p.Stage != "before" {
		t.Fatalf("payload = %+v", p)
	}
	if cmd, _ := p.Args["command"].(string); cmd != "ls /tmp" {
		t.Fatalf("args.command = %v", p.Args["command"])
	}
}

func TestDispatchStartedItem_McpToolCall(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{
		Type: "mcpToolCall", ID: "m1", Server: "docs", Tool: "search",
		Arguments: map[string]any{"q": "hello"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("len envs = %d", len(envs))
	}
	var p proto.ToolCallPayload
	if err := envs[0].DecodePayload(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.Name != "mcp__docs__search" {
		t.Fatalf("tool name = %q", p.Name)
	}
	if got, _ := p.Args["q"].(string); got != "hello" {
		t.Fatalf("args.q = %v", p.Args["q"])
	}
}

func TestDispatchStartedItem_UnknownSilent(t *testing.T) {
	envs, err := DispatchStartedItem("run-1", ThreadItem{Type: "userMessage", ID: "u1"})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if envs != nil {
		t.Fatalf("unknown / silent type must produce no envelopes, got %d", len(envs))
	}
}

func TestDispatchCompletedItem_Reasoning_UsesBuffer(t *testing.T) {
	bufs := NewItemBuffers()
	FoldDeltaIntoBuffer(bufs, "reasoning", "r1", "Hello ")
	FoldDeltaIntoBuffer(bufs, "reasoning", "r1", "world")

	envs, text, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "reasoning", ID: "r1",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if text != "" {
		t.Fatalf("reasoning must not produce agent text, got %q", text)
	}
	if len(envs) != 1 || envs[0].Type != proto.TypeThinking {
		t.Fatalf("envs = %+v", envs)
	}
	var p proto.ThinkingPayload
	_ = envs[0].DecodePayload(&p)
	if p.Text != "Hello world" {
		t.Fatalf("thinking text = %q", p.Text)
	}
	// Buffer must be drained so a re-completion doesn't double-emit.
	if got := bufs.Reasoning["r1"]; got != "" {
		t.Fatalf("buffer not drained: %q", got)
	}
}

func TestDispatchCompletedItem_Reasoning_FallbackToItemBody(t *testing.T) {
	// No deltas observed — must fall back to item.text / summary.
	bufs := NewItemBuffers()
	envs, _, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "reasoning", ID: "r1", Text: "fallback body",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var p proto.ThinkingPayload
	_ = envs[0].DecodePayload(&p)
	if p.Text != "fallback body" {
		t.Fatalf("fallback path = %q", p.Text)
	}
}

func TestDispatchCompletedItem_AgentMessage_BufferIsFinalText(t *testing.T) {
	bufs := NewItemBuffers()
	FoldDeltaIntoBuffer(bufs, "agent", "a1", "Hello ")
	FoldDeltaIntoBuffer(bufs, "agent", "a1", "world")

	envs, text, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "agentMessage", ID: "a1", Text: "fallback",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 0 {
		t.Fatalf("agentMessage must not produce envelopes (final text emits via Done), got %+v", envs)
	}
	if text != "Hello world" {
		// The buffered concatenation must win over item.Text fallback
		// when deltas were observed.
		t.Fatalf("agent text = %q, want buffered concatenation", text)
	}
}

func TestDispatchCompletedItem_AgentMessage_FallbackToItemText(t *testing.T) {
	// No deltas — agent text comes from item.text directly.
	bufs := NewItemBuffers()
	_, text, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "agentMessage", ID: "a1", Text: "no-delta body",
	}, bufs)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if text != "no-delta body" {
		t.Fatalf("fallback path = %q", text)
	}
}

func TestDispatchCompletedItem_ToolCallEmitsAfterStage(t *testing.T) {
	exit := 0
	envs, _, err := DispatchCompletedItem("run-1", ThreadItem{
		Type: "commandExecution", ID: "c1", Status: "completed", ExitCode: &exit,
	}, NewItemBuffers())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(envs) != 1 || envs[0].Type != proto.TypeToolCall {
		t.Fatalf("envs = %+v", envs)
	}
	var p proto.ToolCallPayload
	_ = envs[0].DecodePayload(&p)
	if p.Stage != "after" {
		t.Fatalf("stage = %q, want after", p.Stage)
	}
	if status, _ := p.Result["status"].(string); status != "completed" {
		t.Fatalf("result.status = %v", p.Result["status"])
	}
}

func TestFoldDeltaIntoBuffer_AccumulatesPerItem(t *testing.T) {
	bufs := NewItemBuffers()
	if got := FoldDeltaIntoBuffer(bufs, "agent", "a1", "hi"); got != "hi" {
		t.Fatalf("fold returned %q", got)
	}
	if got := FoldDeltaIntoBuffer(bufs, "agent", "a1", " there"); got != "hi there" {
		t.Fatalf("fold accumulation = %q", got)
	}
	// Different itemId is a different accumulator.
	if got := FoldDeltaIntoBuffer(bufs, "agent", "a2", "other"); got != "other" {
		t.Fatalf("cross-item bleed: got %q", got)
	}
}

func TestArgMap_NonObjectValueWrapped(t *testing.T) {
	// dynamicToolCall.arguments arrives as any; arrays must not be
	// silently dropped.
	m := argMap([]any{"a", "b"})
	if _, ok := m["value"]; !ok {
		t.Fatalf("non-object args must surface under value: %+v", m)
	}
	raw, _ := json.Marshal(m["value"])
	if !strings.Contains(string(raw), `"a"`) || !strings.Contains(string(raw), `"b"`) {
		t.Fatalf("wrapped value lost array data: %s", raw)
	}
}
