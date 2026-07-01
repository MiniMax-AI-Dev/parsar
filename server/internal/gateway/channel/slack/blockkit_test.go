package slack

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

func decodeCard(t *testing.T, card channel.Card) bkMessage {
	t.Helper()
	if card.MIME != slackCardMIME {
		t.Fatalf("MIME = %q, want %q", card.MIME, slackCardMIME)
	}
	var msg bkMessage
	if err := json.Unmarshal(card.Payload, &msg); err != nil {
		t.Fatalf("payload is not valid Block Kit JSON: %v\n%s", err, card.Payload)
	}
	return msg
}

// TestRenderProgress_GoldenMinimal locks the exact Block Kit JSON for a
// minimal in-flight card (title only, no steps/streaming, zero elapsed). This
// is the byte-level golden; the richer cases below assert structure.
func TestRenderProgress_GoldenMinimal(t *testing.T) {
	now := time.Date(2026, 6, 25, 10, 0, 30, 0, time.UTC)
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title: "Demo",
		Now:   now,
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	const want = `{"text":"Demo is running","blocks":[` +
		`{"type":"header","text":{"type":"plain_text","text":"Demo","emoji":true}},` +
		`{"type":"context","elements":[{"type":"mrkdwn","text":":hourglass_flowing_sand: Running"}]}` +
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

	if msg.Blocks[0].Type != "header" || msg.Blocks[0].Text.Text != "Demo Agent" {
		t.Fatalf("first block must be the title header, got %+v", msg.Blocks[0])
	}
	// Step line: Bash icon + label + 2s duration.
	if !blocksContain(msg, ":computer: ls -la · 2s") {
		t.Errorf("missing Bash step line; blocks=%s", card.Payload)
	}
	// Streaming text mrkdwn-formatted: **out** -> *out*.
	if !blocksContain(msg, "thinking *out* loud") {
		t.Errorf("streaming text not mrkdwn-formatted; blocks=%s", card.Payload)
	}
	// Footer status carries the elapsed.
	if !blocksContain(msg, ":hourglass_flowing_sand: Running · 30s") {
		t.Errorf("missing running footer with elapsed; blocks=%s", card.Payload)
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

	if msg.Blocks[0].Type != "header" {
		t.Fatalf("first block must be header, got %q", msg.Blocks[0].Type)
	}
	if !blocksContain(msg, "*Thinking*") {
		t.Errorf("missing thinking section; blocks=%s", card.Payload)
	}
	if !blocksContain(msg, "final answer") { // streaming trimmed
		t.Errorf("missing streaming answer; blocks=%s", card.Payload)
	}
	if !blocksContain(msg, "$0.42") {
		t.Errorf("missing usage footer; blocks=%s", card.Payload)
	}
	if msg.Text != "final answer" {
		t.Errorf("fallback text = %q, want first line of answer", msg.Text)
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

	if !blocksContain(msg, ":x: *tool exploded*") {
		t.Errorf("missing error headline; blocks=%s", card.Payload)
	}
	// Raw error escaped angle brackets inside a code block.
	if !blocksContain(msg, "stack <trace>") && !blocksContain(msg, "stack &lt;trace&gt;") {
		t.Errorf("missing raw error; blocks=%s", card.Payload)
	}
	if !blocksContain(msg, "<https://example.com/runs/abc|View run details>") {
		t.Errorf("missing run-detail deep link; blocks=%s", card.Payload)
	}
	if !blocksContain(msg, "register at example.com") {
		t.Errorf("missing guest hint; blocks=%s", card.Payload)
	}
	if msg.Text != "Demo Agent — tool exploded" {
		t.Errorf("fallback text = %q", msg.Text)
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
	if !blocksContain(msg, errCardFallbackMessage) {
		t.Errorf("empty ErrorMessage must fall back to default copy; blocks=%s", card.Payload)
	}
}

// TestRenderProgress_DefaultsNow asserts a zero Now does not panic and yields a
// non-empty card (exact bytes depend on wall-clock).
func TestRenderProgress_DefaultsNow(t *testing.T) {
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{Title: "Demo"})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	if len(card.Payload) == 0 {
		t.Fatal("zero Now produced empty payload")
	}
}

// TestRenderClampsToFiftyBlocks verifies the 50-block cap with a truncation note.
func TestRenderClampsToFiftyBlocks(t *testing.T) {
	// 51 streaming chunks (>3000 chars each split) + header + status > 50.
	huge := strings.Repeat("a", 51*slackSectionLimit+10)
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{
		Title:         "Demo",
		StreamingText: huge,
		Now:           time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	msg := decodeCard(t, card)
	if len(msg.Blocks) != slackMaxBlocks {
		t.Fatalf("blocks = %d, want capped at %d", len(msg.Blocks), slackMaxBlocks)
	}
	last := msg.Blocks[slackMaxBlocks-1]
	if last.Type != "context" || len(last.Elements) == 0 || last.Elements[0].Text != truncatedNote {
		t.Errorf("last block must be the truncation note, got %+v", last)
	}
}

func TestEmptyTitleFallsBack(t *testing.T) {
	card, err := newTestChannel().RenderProgress(context.Background(), channel.ReplyTarget{}, channel.ProgressState{Now: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatalf("RenderProgress: %v", err)
	}
	msg := decodeCard(t, card)
	if msg.Blocks[0].Text.Text != defaultCardTitle {
		t.Errorf("empty title header = %q, want %q", msg.Blocks[0].Text.Text, defaultCardTitle)
	}
}

// blocksContain reports whether any block's section text or context element
// text contains sub.
func blocksContain(msg bkMessage, sub string) bool {
	for _, b := range msg.Blocks {
		if b.Text != nil && strings.Contains(b.Text.Text, sub) {
			return true
		}
		for _, e := range b.Elements {
			if strings.Contains(e.Text, sub) {
				return true
			}
		}
	}
	return false
}
