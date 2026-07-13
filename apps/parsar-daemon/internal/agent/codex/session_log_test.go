package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// TestEmitTerminal_LogsErrorMessage pins the diagnostic invariant:
// every time the codex session decides "this prompt is over with an
// error", the error message MUST land in the daemon's structured log.
//
// Before this fix, emitTerminal silently sent a TypeError envelope to
// the upstream channel. If the upstream connector logged only the
// envelope type (not the body), debugging required guessing what the
// session had decided to surface. With the fix the daemon log carries
// the exact message string, the run_id, and the thread_id at the same
// timestamp the TypeError frame was emitted.
func TestEmitTerminal_LogsErrorMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	out := make(chan proto.Envelope, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Session{
		runID:     "run-test-123",
		cfg:       sessionConfig{logger: logger},
		out:       out,
		cancelCtx: ctx,
	}
	s.setThreadID("thread-abc")

	const message = "codex: thread/start: bad provider config"
	s.emitTerminal(message, true)

	logs := buf.String()
	for _, want := range []string{
		`"msg":"codex: emitting terminal error"`,
		`"run_id":"run-test-123"`,
		`"thread_id":"thread-abc"`,
		`"message":"` + message + `"`,
		`"level":"WARN"`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, logs)
		}
	}

	// Side-effects on out channel: TypeError + TypeDone.
	got := drainEnvelopes(out)
	if len(got) != 2 {
		t.Fatalf("envelope count = %d, want 2 (error + done); got=%+v", len(got), got)
	}
	if got[0].Type != proto.TypeError {
		t.Fatalf("first envelope type = %q, want error", got[0].Type)
	}
	var errPayload proto.ErrorPayload
	_ = json.Unmarshal(got[0].Payload, &errPayload)
	if errPayload.Error != message {
		t.Fatalf("error payload = %q, want %q", errPayload.Error, message)
	}
	if got[1].Type != proto.TypeDone {
		t.Fatalf("second envelope type = %q, want done", got[1].Type)
	}
}

// TestEmitTerminal_LogsDoneMessage covers the success-path log so a
// future change that flips asError=false on a real prompt completion
// still leaves a trace in the daemon log.
func TestEmitTerminal_LogsDoneMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	out := make(chan proto.Envelope, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Session{
		runID:     "run-test-456",
		cfg:       sessionConfig{logger: logger},
		out:       out,
		cancelCtx: ctx,
	}

	s.emitTerminal("hello world", false)

	logs := buf.String()
	for _, want := range []string{
		`"msg":"codex: emitting terminal done"`,
		`"run_id":"run-test-456"`,
		`"message_len":11`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, logs)
		}
	}
	// asError=false: only TypeDone, no TypeError.
	got := drainEnvelopes(out)
	if len(got) != 1 || got[0].Type != proto.TypeDone {
		t.Fatalf("envelopes = %+v, want exactly 1 done", got)
	}
}

// TestOnErrorNotif_LogsAndBuffers verifies the codex `error`
// notification path stays inspectable. Codex's gateway / provider
// failures (a 401 from a misconfigured custom header, a 400 from
// Azure missing api-version) arrive on this single channel; without
// the log, the daemon shows ~13s of silence and then a TypeError
// envelope the upstream connector can't decode.
func TestOnErrorNotif_LogsAndBuffers(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	s := &Session{runID: "run-x", cfg: sessionConfig{logger: logger}}
	s.setThreadID("thread-y")

	raw, _ := json.Marshal(ErrorNotification{Message: "401 from gateway: invalid X-Sub-Module header"})
	s.onErrorNotif(raw)

	if got := s.peekLastErrText(); !strings.Contains(got, "401") {
		t.Fatalf("lastErrText not buffered: %q", got)
	}
	logs := buf.String()
	for _, want := range []string{
		`"msg":"codex: error notification received"`,
		`"thread_id":"thread-y"`,
		`401 from gateway`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, logs)
		}
	}
}

// TestOnTurnFailed_LogsBufferedError pins that turn/failed includes
// "we already have buffered error text" in its log line. If onErrorNotif
// fired first and stashed the real cause, the post-mortem can correlate
// the two events by run_id alone.
func TestOnTurnFailed_LogsBufferedError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	out := make(chan proto.Envelope, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Session{
		runID:     "run-z",
		cfg:       sessionConfig{logger: logger},
		out:       out,
		cancelCtx: ctx,
	}
	// Simulate codex sending the real error first…
	rawErr, _ := json.Marshal(ErrorNotification{Message: "platform-api 500"})
	s.onErrorNotif(rawErr)
	// …then turn/failed.
	rawFail, _ := json.Marshal(TurnCompletedNotification{Turn: Turn{ID: "t-1", Status: "failed"}})
	s.onTurnFailed(rawFail)

	logs := buf.String()
	if !strings.Contains(logs, `"msg":"codex: turn/failed received"`) {
		t.Fatalf("turn/failed log missing\n%s", logs)
	}
	if !strings.Contains(logs, `"last_err_text_present":true`) {
		t.Fatalf("turn/failed log must note that an upstream error was buffered\n%s", logs)
	}
}

func TestTerminalTurnKeepsRPCAliveUntilSessionCancel(t *testing.T) {
	tests := []struct {
		name string
		run  func(*Session)
	}{
		{
			name: "completed",
			run: func(s *Session) {
				raw, _ := json.Marshal(TurnCompletedNotification{
					Turn: Turn{ID: "turn-1", Status: "completed"},
				})
				s.onTurnCompleted(raw)
			},
		},
		{
			name: "failed",
			run: func(s *Session) {
				raw, _ := json.Marshal(TurnCompletedNotification{
					Turn: Turn{ID: "turn-2", Status: "failed"},
				})
				s.onTurnFailed(raw)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc, _, cleanup := NewTestClient()
			defer cleanup()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			out := make(chan proto.Envelope, 4)
			s := &Session{
				runID:     "run-terminal-" + tt.name,
				cfg:       sessionConfig{logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
				out:       out,
				rpc:       rpc.JSONRPCClient,
				cancelCtx: ctx,
				cancelFn:  cancel,
			}
			s.setThreadID("thread-" + tt.name)

			tt.run(s)

			if !rpc.Alive() {
				t.Fatal("terminal turn must keep the codex RPC client alive during the idle window")
			}
			select {
			case <-ctx.Done():
				t.Fatal("terminal turn must not cancel the session context")
			default:
			}

		})
	}
}

func drainEnvelopes(out <-chan proto.Envelope) []proto.Envelope {
	var got []proto.Envelope
	for {
		select {
		case env := <-out:
			got = append(got, env)
		default:
			return got
		}
	}
}
