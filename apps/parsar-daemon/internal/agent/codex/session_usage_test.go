package codex

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestNativeTokenUsageAcrossTurns(t *testing.T) {
	out := make(chan proto.Envelope, 4)
	s := &Session{runID: "run", out: out, cancelCtx: context.Background(),
		cfg: sessionConfig{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	s.setThreadID("thread")
	// A resumed thread replays its previous usage before starting this run.
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"previous","tokenUsage":{"total":{"inputTokens":1000,"outputTokens":100},"last":{"inputTokens":500,"outputTokens":50}}}`))
	if s.latestUsage != nil {
		t.Fatal("restored history was treated as current usage")
	}
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"current"}}`))
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"current","tokenUsage":{"total":{"inputTokens":1200,"outputTokens":120},"last":{"inputTokens":200,"outputTokens":20}}}`))
	// The second model request follows a tool call; its last counter is not
	// the whole turn. Repeating the cumulative snapshot must not add usage.
	second := json.RawMessage(`{"threadId":"thread","turnId":"current","tokenUsage":{"total":{"inputTokens":1500,"outputTokens":150},"last":{"inputTokens":300,"outputTokens":30}}}`)
	s.onUsageUpdated(second)
	s.onUsageUpdated(second)
	if s.latestUsage == nil || s.latestUsage.InputTokens != 500 || s.latestUsage.OutputTokens != 50 {
		t.Fatalf("current turn usage = %+v, want 500 input / 50 output", s.latestUsage)
	}
	// A subsequent turn in this process uses the last total as its baseline.
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"next"}}`))
	if s.latestUsage != nil {
		t.Fatal("new turn retained previous turn usage")
	}
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"next","tokenUsage":{"total":{"inputTokens":1700,"outputTokens":175},"last":{"inputTokens":200,"outputTokens":25}}}`))
	s.finalText = "done"
	s.onTurnCompleted(json.RawMessage(`{"threadId":"thread","turn":{"id":"next","status":"completed"}}`))
	var usage proto.UsagePayload
	var done proto.DonePayload
	for e := range out {
		switch e.Type {
		case proto.TypeUsage:
			if err := json.Unmarshal(e.Payload, &usage); err != nil {
				t.Fatal(err)
			}
		case proto.TypeDone:
			if err := json.Unmarshal(e.Payload, &done); err != nil {
				t.Fatal(err)
			}
		}
	}
	if usage.InputTokens != 200 || usage.OutputTokens != 25 || done.Usage.InputTokens != 200 || done.Content != "done" {
		t.Fatalf("terminal usage=%+v done=%+v", usage, done)
	}
}

func TestNativeTokenUsageFreshThreadAndIgnoredPayloads(t *testing.T) {
	s := &Session{}
	s.setThreadID("thread")
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"current"}}`))
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","turnId":"current","tokenUsage":{"total":{"inputTokens":321,"outputTokens":45}}}`))
	for _, raw := range []string{
		`{`,
		`{"threadId":"thread","turnId":"current"}`,
		`{"threadId":"thread","turnId":"current","tokenUsage":{}}`,
		`{"threadId":"thread","turnId":"previous","tokenUsage":{"total":{"inputTokens":999,"outputTokens":99}}}`,
		`{"threadId":"other","turnId":"current","tokenUsage":{"total":{"inputTokens":999,"outputTokens":99}}}`,
	} {
		s.onUsageUpdated(json.RawMessage(raw))
	}
	if s.latestUsage == nil || s.latestUsage.InputTokens != 321 || s.latestUsage.OutputTokens != 45 {
		t.Fatalf("valid usage was lost or overwritten: %+v", s.latestUsage)
	}
}

func TestLegacyTurnUsagePayload(t *testing.T) {
	s := &Session{}
	s.setThreadID("thread")
	s.onTurnStarted(json.RawMessage(`{"threadId":"thread","turn":{"id":"current"}}`))
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","usage":{"inputTokens":123,"outputTokens":12}}`))
	if s.latestUsage == nil || s.latestUsage.InputTokens != 123 || s.latestUsage.OutputTokens != 12 {
		t.Fatalf("legacy usage = %+v", s.latestUsage)
	}
	s.onUsageUpdated(json.RawMessage(`{"threadId":"thread","usage":{"inputTokens":0,"outputTokens":0}}`))
	if s.latestUsage == nil || s.latestUsage.InputTokens != 0 || s.latestUsage.OutputTokens != 0 {
		t.Fatalf("explicit zero usage = %+v", s.latestUsage)
	}
}
