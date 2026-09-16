//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceCommandsRequirePackagedFeatureOnlyWhenRequested(t *testing.T) {
	for _, observed := range []bool{false, true} {
		config := preparationFixture(t, "old-command-runtime")
		req := preparationRequest()
		req.ObserveToolObservations = observed
		resource, err := NewPreparationFactory(config)(t.Context(), req)
		if observed {
			if err == nil || !strings.Contains(err.Error(), "workspace command observations") {
				t.Fatal("old bridge accepted requested command observations", err)
			}
			if _, err := os.Stat(filepath.Join(config.StateDir, "launched")); !os.IsNotExist(err) {
				t.Fatal("old bridge started execution before rejection")
			}
		} else {
			if err != nil {
				t.Fatal("old bridge changed opt-out behavior", err)
			}
			if err := resource.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestWorkspaceCommandFramesKeepStartIdentityAndObservedOutput(t *testing.T) {
	for _, observed := range []bool{false, true} {
		config := preparationFixture(t, "commands-success")
		req := preparationRequest()
		req.ObserveToolObservations = observed
		resource, err := NewPreparationFactory(config)(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		defer resource.Close()
		if _, err := os.Stat(filepath.Join(config.StateDir, "start.json")); !os.IsNotExist(err) {
			t.Fatal("preparation submitted a command")
		}
		out := make(chan proto.Envelope, 16)
		s, err := resource.Start(t.Context(), "actual-command-run", "hello", out)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Cancel(context.Background())
		var frames []proto.ToolCallPayload
		done := 0
		for event := range out {
			if event.ID != "actual-command-run" || event.Type == proto.TypeError || event.Type == proto.TypeCommandOutput {
				t.Fatal("execution identity or final-only command behavior changed", event.Type)
			}
			if event.Type == proto.TypeToolCall {
				var payload proto.ToolCallPayload
				if err := event.DecodePayload(&payload); err != nil {
					t.Fatal(err)
				}
				frames = append(frames, payload)
			}
			if event.Type == proto.TypeDone {
				done++
			}
		}
		if done != 1 || observed && len(frames) != 2 || !observed && len(frames) != 0 {
			t.Fatal("completion or observation opt-in changed", done, frames)
		}
		if observed && (frames[0].ID != "observed" || frames[1].ID != "observed" || frames[1].Observation.Status != "failed" ||
			string(frames[1].Observation.Output) != `"Exit code 7\nretained"`) {
			t.Fatal("native failure output was not retained", frames)
		}
	}
}

func TestWorkspaceCommandCancellationAndBridgeFailuresCloseOnlyPendingCalls(t *testing.T) {
	for _, mode := range []string{"commands-cancel", "commands-drained", "commands-killed", "commands-missing", "commands-unknown", "commands-before-ready", "commands-wrong-session"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			config := preparationFixture(t, mode)
			req := workspaceRequest()
			req.AgentSessionID, req.ObserveToolObservations = "native-session", true
			out := make(chan proto.Envelope, 32)
			s, err := NewFactory(config)(ctx, req, out)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			frames := map[string][]proto.ToolCallPayload{}
			failed, done := false, 0
			for event := range out {
				if event.ID != req.RunID || event.Type == proto.TypeCommandOutput {
					t.Fatal("command frame changed execution identity or fabricated deltas")
				}
				switch event.Type {
				case proto.TypeToolCall:
					var payload proto.ToolCallPayload
					if err := event.DecodePayload(&payload); err != nil {
						t.Fatal(err)
					}
					frames[payload.ID] = append(frames[payload.ID], payload)
					if payload.ID == "pending" && payload.Stage == "before" {
						if mode == "commands-killed" {
							if err := s.(*session).process.Cmd.Process.Signal(syscall.SIGKILL); err != nil {
								t.Fatal(err)
							}
						} else if mode == "commands-cancel" || mode == "commands-drained" {
							if err := s.Cancel(ctx); err != nil {
								t.Fatal(err)
							}
						}
					}
				case proto.TypeError:
					failed = true
				case proto.TypeDone:
					done++
				}
			}
			if !failed || done != 1 {
				t.Fatal("invalid completion after command failure", failed, done)
			}
			if mode == "commands-before-ready" || mode == "commands-wrong-session" {
				if len(frames) != 0 {
					t.Fatal("unverified execution identity emitted commands", frames)
				}
				return
			}
			observed, pending := frames["observed"], frames["pending"]
			if len(observed) != 2 || observed[1].Observation.Status != "failed" || string(observed[1].Observation.Output) != `"Exit code 7\nretained"` ||
				len(pending) != 2 || pending[1].Observation.Status != "incomplete" {
				t.Fatal("shutdown lost observed output or did not close only pending calls", frames)
			}
			if mode == "commands-drained" {
				if string(pending[1].Observation.Output) != `"observed while draining"` {
					t.Fatal("valid interruption output was lost during drain")
				}
			} else if len(pending[1].Observation.Output) != 0 {
				t.Fatal("missing native result acquired output")
			}
		})
	}
}

func runCommandsHelper(request startRequest, mode string, emit func(bridgeEvent)) {
	var stopping chan os.Signal
	if mode == "commands-cancel" || mode == "commands-drained" {
		stopping = make(chan os.Signal, 1)
		signal.Notify(stopping, syscall.SIGTERM)
		defer signal.Stop(stopping)
	}
	if mode != "commands-before-ready" {
		emit(bridgeEvent{Type: "input_ready", SessionID: request.Resume})
	}
	before := commandEvent("observed", "before", "in_progress", "printf 'retained\\n'; exit 7")
	if mode == "commands-wrong-session" {
		before.SessionID = "other"
	}
	emit(before)
	if mode == "commands-before-ready" || mode == "commands-wrong-session" {
		return
	}
	after := commandEvent("observed", "after", "failed", before.Observation.Command)
	after.Observation.Output = json.RawMessage(`"Exit code 7\nretained"`)
	emit(after)
	if mode != "commands-success" {
		emit(commandEvent("pending", "before", "in_progress", "sleep 30"))
	}
	switch mode {
	case "commands-cancel", "commands-drained":
		<-stopping
		if mode == "commands-drained" {
			interrupted := commandEvent("pending", "after", "incomplete", "sleep 30")
			interrupted.Observation.Output = json.RawMessage(`"observed while draining"`)
			emit(interrupted)
		}
		emit(bridgeEvent{Type: "error", Code: "cancelled"})
		return
	case "commands-killed":
		time.Sleep(time.Hour)
		return
	case "commands-unknown":
		emit(bridgeEvent{Type: "unexpected-command-event"})
		return
	}
	emit(bridgeEvent{Type: "input_closed", SessionID: request.Resume})
	emit(bridgeEvent{Type: "result", SessionID: request.Resume, Text: "completed"})
}
