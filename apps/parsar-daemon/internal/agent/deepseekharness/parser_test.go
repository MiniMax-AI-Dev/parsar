package deepseekharness_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/deepseekharness"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func decodePayload[T any](t *testing.T, env proto.Envelope) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(env.Payload, &out); err != nil {
		t.Fatalf("decode %s payload: %v", env.Type, err)
	}
	return out
}

func TestTerminalEnvelopesSuccessEmitsDeltaThenDone(t *testing.T) {
	tr := deepseekharness.NewTranslatorForTest("run-1")
	tr.AppendLine("first line")
	tr.AppendLine("second line")

	envs := tr.TerminalEnvelopes(nil, "", false)
	if len(envs) != 2 {
		t.Fatalf("expected delta+done, got %d: %v", len(envs), envTypes(envs))
	}
	if envs[0].Type != proto.TypeDelta || envs[1].Type != proto.TypeDone {
		t.Fatalf("types = %v", envTypes(envs))
	}
	delta := decodePayload[proto.DeltaPayload](t, envs[0])
	if delta.Delta != "first line\nsecond line" {
		t.Fatalf("delta = %q", delta.Delta)
	}
	done := decodePayload[proto.DonePayload](t, envs[1])
	if done.Content != "first line\nsecond line" {
		t.Fatalf("done content = %q", done.Content)
	}
	// dsh headless creates a fresh session per run and prints no session
	// id, so the server must not be handed a resume handle.
	if _, ok := done.Metadata[proto.DoneMetaAgentSessionID]; ok {
		t.Fatalf("done metadata must carry no session id: %#v", done.Metadata)
	}
	if done.Usage.Provider != "deepseek-harness" {
		t.Fatalf("done usage = %#v", done.Usage)
	}
	for _, env := range envs {
		if env.ID != "run-1" {
			t.Fatalf("env %s ID = %q, want run-1", env.Type, env.ID)
		}
	}
}

func TestTerminalEnvelopesFailureFoldsStderr(t *testing.T) {
	tr := deepseekharness.NewTranslatorForTest("run-2")
	envs := tr.TerminalEnvelopes(errors.New("exit status 1"), "MODEL_ERROR: upstream refused", false)
	types := envTypes(envs)
	if len(envs) != 2 || envs[0].Type != proto.TypeError || envs[1].Type != proto.TypeDone {
		t.Fatalf("types = %v, want error+done", types)
	}
	payload := decodePayload[proto.ErrorPayload](t, envs[0])
	if !strings.Contains(payload.Error, "exit status 1") || !strings.Contains(payload.Error, "upstream refused") {
		t.Fatalf("error payload = %q", payload.Error)
	}
}

func TestTerminalEnvelopesCancelledReportsCancellation(t *testing.T) {
	tr := deepseekharness.NewTranslatorForTest("run-3")
	tr.AppendLine("partial")
	envs := tr.TerminalEnvelopes(errors.New("signal: terminated"), "", true)
	if envs[len(envs)-1].Type != proto.TypeDone {
		t.Fatalf("last env = %v, want done", envTypes(envs))
	}
	var errPayload proto.ErrorPayload
	for _, env := range envs {
		if env.Type == proto.TypeError {
			errPayload = decodePayload[proto.ErrorPayload](t, env)
		}
	}
	if !strings.Contains(errPayload.Error, "cancelled") {
		t.Fatalf("error payload = %q, want cancelled", errPayload.Error)
	}
}

func envTypes(envs []proto.Envelope) []string {
	out := make([]string, len(envs))
	for i, env := range envs {
		out[i] = env.Type
	}
	return out
}
