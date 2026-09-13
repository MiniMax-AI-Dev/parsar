//go:build unix

package claudesdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/clirunner"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSteeringReceiptsAndLifecycle(t *testing.T) {
	for _, mode := range []string{"success", "phased", "timeout", "wrong-receipt", "duplicate-usage", "cancel", "blocked-write"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=steering-" + mode, "GORACE=atexit_sleep_ms=0"}}
			if mode == "phased" {
				config.Env[1] = "SDK_HELPER_MODE=steering-timeout"
			}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", AgentSessionID: "native", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			running, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			s := running.(*session)
			defer s.Cancel(context.Background())
			if frame := <-out; frame.Type != proto.TypeDelta {
				t.Fatal("missing ready barrier", frame.Type)
			}
			input := proto.PromptSteerPayload{InputID: "extra", Text: "additional"}
			if mode == "blocked-write" {
				input.Text = strings.Repeat("x", 512*1024)
			}
			receiptCtx, receiptCancel := context.WithTimeout(ctx, time.Second)
			if mode == "timeout" {
				receiptCancel()
				receiptCtx, receiptCancel = context.WithTimeout(ctx, 30*time.Millisecond)
			}
			if mode == "blocked-write" {
				receiptCancel()
				receiptCtx, receiptCancel = context.WithCancel(ctx)
			}
			defer receiptCancel()
			reply := make(chan error, 1)
			go func() {
				if mode == "phased" {
					callCtx, cancel := context.WithCancel(ctx)
					defer cancel()
					timer := time.AfterFunc(30*time.Millisecond, cancel)
					defer timer.Stop()
					reply <- s.SteerWithReceipt(callCtx, input, func() {
						if !timer.Stop() {
							t.Error("write phase exceeded deadline")
						}
					})
				} else {
					reply <- s.Steer(receiptCtx, input)
				}
			}()
			if mode == "blocked-write" {
				// Serialization can exceed a short deadline under race instrumentation.
				// Cancel only after admission so this exercises transport cancellation.
				for {
					s.steering.mu.Lock()
					admitted := s.steering.pending != nil
					s.steering.mu.Unlock()
					if admitted {
						receiptCancel()
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("input was not admitted before test deadline")
					case <-time.After(time.Millisecond):
					}
				}
			}
			if mode == "cancel" {
				time.Sleep(30 * time.Millisecond)
				_ = s.Cancel(context.Background())
			}
			receipt := <-reply
			if mode == "success" || mode == "phased" || mode == "duplicate-usage" {
				if receipt != nil {
					t.Fatal(receipt)
				}
			} else if receipt == nil {
				t.Fatal("missing failure/unknown receipt")
			}
			if mode == "timeout" {
				if !errors.Is(receipt, context.DeadlineExceeded) {
					t.Fatal(receipt)
				}
				select {
				case <-s.process.Done():
					t.Fatal("receipt timeout killed native process")
				default:
				}
			}
			var done proto.DonePayload
			failed := false
			measurements := 0
			for frame := range out {
				switch frame.Type {
				case proto.TypeError:
					failed = true
				case proto.TypeUsage:
					measurements++
				case proto.TypeDone:
					if err := frame.DecodePayload(&done); err != nil {
						t.Fatal(err)
					}
				}
			}
			wantSuccess := mode == "success" || mode == "phased" || mode == "timeout"
			if failed == wantSuccess {
				t.Fatalf("unexpected terminal failure=%v", failed)
			}
			if wantSuccess {
				if measurements != 2 || done.Content != "final" || done.Metadata[proto.DoneMetaAgentSessionID] != "native" {
					t.Fatalf("bad completion: %+v, measurements=%d", done, measurements)
				}
				snapshots, ok := done.Usage.Raw["claude_sdk_results"].([]any)
				if !ok || len(snapshots) != 2 {
					t.Fatal("lost native-turn usage snapshots", done.Usage.Raw)
				}
				if snapshots[0].(map[string]any)["total_cost_usd"] != float64(0.1) || snapshots[1].(map[string]any)["total_cost_usd"] != float64(0.3) {
					t.Fatal("native cumulative counters changed", snapshots)
				}
			}
			if mode == "duplicate-usage" && measurements != 1 {
				t.Fatal("duplicate measurement was published")
			}
			if err := s.Steer(ctx, input); !errors.Is(err, agent.ErrSteeringInactive) {
				t.Fatal("completed execution accepted input", err)
			}
			select {
			case <-s.process.Done():
			default:
				t.Fatal("completion preceded process release")
			}
		})
	}
}

func TestSteeringDoesNotSendBeforeReadiness(t *testing.T) {
	s := &session{process: &clirunner.Process{}}
	if err := s.Steer(context.Background(), proto.PromptSteerPayload{InputID: "one", Text: "hello"}); !errors.Is(err, agent.ErrSteeringNotReady) {
		t.Fatal(err)
	}
	s.stopSteering()
	if err := s.Steer(context.Background(), proto.PromptSteerPayload{InputID: "one", Text: "hello"}); !errors.Is(err, agent.ErrSteeringInactive) {
		t.Fatal(err)
	}
}

func runSteeringHelper(request startRequest, mode string, scanner *bufio.Scanner, emit func(bridgeEvent)) {
	emit(bridgeEvent{Type: "input_ready", SessionID: request.Resume})
	emit(bridgeEvent{Type: "delta", Delta: "ready"})
	if mode == "steering-blocked-write" {
		time.Sleep(time.Hour)
		return
	}
	if !scanner.Scan() {
		os.Exit(2)
	}
	var input struct {
		Type, Text string
		InputID    string `json:"input_id"`
	}
	if json.Unmarshal(scanner.Bytes(), &input) != nil || input.Type != "steer" || input.Text != "additional" || input.InputID != "extra" {
		os.Exit(3)
	}
	if mode == "steering-cancel" {
		time.Sleep(time.Hour)
		return
	}
	if mode == "steering-timeout" {
		time.Sleep(100 * time.Millisecond)
	}
	if mode == "steering-wrong-receipt" {
		emit(bridgeEvent{Type: "input_applied", InputID: "unknown"})
		time.Sleep(time.Hour)
		return
	}
	emit(bridgeEvent{Type: "usage", SessionID: request.Resume, ResultID: "first", Usage: json.RawMessage(`{"usage":{"input_tokens":4},"modelUsage":{"model":{"inputTokens":4}},"total_cost_usd":0.1}`)})
	emit(bridgeEvent{Type: "input_applied", InputID: input.InputID})
	time.Sleep(30 * time.Millisecond)
	id := "second"
	if mode == "steering-duplicate-usage" {
		id = "first"
	}
	emit(bridgeEvent{Type: "usage", SessionID: request.Resume, ResultID: id, Usage: json.RawMessage(`{"usage":{"input_tokens":7},"modelUsage":{"model":{"inputTokens":11}},"total_cost_usd":0.3}`)})
	emit(bridgeEvent{Type: "input_closed", SessionID: request.Resume})
	emit(bridgeEvent{Type: "result", SessionID: request.Resume, Text: "final"})
}
