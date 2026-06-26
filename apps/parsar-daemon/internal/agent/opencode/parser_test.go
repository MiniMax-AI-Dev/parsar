package opencode_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/opencode"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestTranslatePartDeltaEmitsDeltaAndDone(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-1")
	tx, err := tr.Translate([]byte(`{"type":"message.part.delta","properties":{"field":"text","delta":"hello"}}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if len(tx.Envelopes) != 1 || tx.Envelopes[0].Type != proto.TypeDelta {
		t.Fatalf("delta envelopes = %#v", tx.Envelopes)
	}
	delta := decodePayload[proto.DeltaPayload](t, tx.Envelopes[0])
	if delta.Delta != "hello" || delta.Sequence == 0 {
		t.Fatalf("delta payload = %#v", delta)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	last := envs[len(envs)-1]
	if last.Type != proto.TypeDone {
		t.Fatalf("last env type = %q, want done", last.Type)
	}
	done := decodePayload[proto.DonePayload](t, last)
	if done.Content != "hello" || done.Metadata["connector_path"] != "opencode_run" {
		t.Fatalf("done payload = %#v", done)
	}
}

func TestTranslateCapturesUsage(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-u")
	_, err := tr.Translate([]byte(`{"type":"message.updated","properties":{"info":{"cost":0.25,"tokens":{"input":10,"output":7,"reasoning":3,"cacheRead":2,"cacheWrite":1,"total":23}}}}`))
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	envs := tr.TerminalEnvelopes(nil, "", false)
	var got *proto.UsagePayload
	for _, env := range envs {
		if env.Type == proto.TypeUsage {
			payload := decodePayload[proto.UsagePayload](t, env)
			got = &payload
		}
	}
	if got == nil {
		t.Fatalf("usage env missing: %#v", envs)
	}
	if got.Provider != "opencode" || got.InputTokens != 10 || got.OutputTokens != 7 || got.CostUSD != 0.25 {
		t.Fatalf("usage = %#v", got)
	}
	if got.Raw["total_tokens"] != float64(23) {
		t.Fatalf("usage raw = %#v", got.Raw)
	}
}

func TestPlainOutputFallsBackToDelta(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-p")
	_, _ = tr.Translate([]byte("plain output"))
	envs := tr.TerminalEnvelopes(nil, "", false)
	if len(envs) < 2 || envs[0].Type != proto.TypeDelta || envs[len(envs)-1].Type != proto.TypeDone {
		t.Fatalf("plain terminal envs = %#v", envs)
	}
	done := decodePayload[proto.DonePayload](t, envs[len(envs)-1])
	if done.Content != "plain output" {
		t.Fatalf("done content = %q", done.Content)
	}
}

func TestTerminalErrorIncludesStderr(t *testing.T) {
	tr := opencode.NewTranslatorForTest("run-e")
	envs := tr.TerminalEnvelopes(errors.New("exit status 2"), "bad auth", false)
	if len(envs) < 2 || envs[0].Type != proto.TypeError || envs[len(envs)-1].Type != proto.TypeDone {
		t.Fatalf("error terminal envs = %#v", envs)
	}
	errPayload := decodePayload[proto.ErrorPayload](t, envs[0])
	if errPayload.Error == "" || !contains(errPayload.Error, "bad auth") {
		t.Fatalf("error payload = %#v", errPayload)
	}
}

func decodePayload[T any](t *testing.T, env proto.Envelope) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		t.Fatalf("decode %s payload: %v", env.Type, err)
	}
	return out
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return sub == ""
}
