//go:build unix

package claudesdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestMessageObservations(t *testing.T) {
	for _, mode := range []string{"messages-success", "messages-partial", "messages-unrequested", "messages-missing-id", "messages-invalid-snapshot"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PARSAR_HOME", root)
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "worker"), StateDir: filepath.Join(root, "state"), Env: []string{"GO_CLAUDE_SDK_HELPER=1", "SDK_HELPER_MODE=" + mode, "GORACE=atexit_sleep_ms=0"}}
			request := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", ObserveMessages: mode != "messages-unrequested", AgentSessionID: "native-session", AgentOptions: map[string]any{"model": "fake-model", "system_prompt": "instructions"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			out := make(chan proto.Envelope, 16)
			running, err := NewFactory(config)(ctx, request, out)
			if err != nil {
				t.Fatal(err)
			}
			defer running.Cancel(context.Background())
			var events []proto.Envelope
			var failure bool
			var done *proto.DonePayload
			for event := range out {
				events = append(events, event)
				switch event.Type {
				case proto.TypeError:
					failure = true
				case proto.TypeDone:
					done = &proto.DonePayload{}
					if err := json.Unmarshal(event.Payload, done); err != nil {
						t.Fatal(err)
					}
				}
			}
			if done == nil || failure != (mode != "messages-success") {
				t.Fatalf("unexpected outcome: %+v", events)
			}
			switch mode {
			case "messages-success":
				verifyMessageEvents(t, events, "partialfinal")
				if done.Content != "partialfinal" || done.Metadata[proto.DoneMetaAgentSessionID] != "native-session" {
					t.Fatalf("bad Done: %+v", done)
				}
			case "messages-partial":
				if done.Content != "partial" || done.Metadata[proto.DoneMetaAgentSessionID] != nil {
					t.Fatalf("bad partial Done: %+v", done)
				}
				if len(events) != 4 || events[0].Type != proto.TypeOutputMessage || events[1].Type != proto.TypeDelta {
					t.Fatalf("lost partial output: %+v", events)
				}
			case "messages-unrequested", "messages-missing-id":
				if len(events) != 2 {
					t.Fatalf("invalid bridge observation escaped: %+v", events)
				}
			}
		})
	}
}

func runMessageHelper(request startRequest, mode string, emit func(bridgeEvent)) {
	message := func(id, status string, text *string) {
		emit(bridgeEvent{Type: "output_message", Message: &proto.OutputMessagePayload{ID: id, Status: status, Text: text}})
	}
	if mode == "messages-missing-id" {
		emit(bridgeEvent{Type: "delta", Delta: "unidentified"})
		return
	}
	if mode != "messages-unrequested" && !request.ObserveMessages {
		os.Exit(4)
	}
	message("message-1", "in_progress", nil)
	if mode == "messages-unrequested" {
		return
	}
	emit(bridgeEvent{Type: "delta", ItemID: "message-1", Delta: "partial"})
	if mode == "messages-partial" {
		emit(bridgeEvent{Type: "error", Code: "execution_failed"})
		return
	}
	if mode == "messages-invalid-snapshot" {
		message("message-1", "completed", nil)
		return
	}
	text := "partial"
	message("message-1", "completed", &text)
	message("message-2", "in_progress", nil)
	emit(bridgeEvent{Type: "delta", ItemID: "message-2", Delta: "fi"})
	emit(bridgeEvent{Type: "delta", ItemID: "message-2", Delta: "nal"})
	text = "final"
	message("message-2", "completed", &text)
	emit(bridgeEvent{Type: "result", Text: "partialfinal", SessionID: request.Resume})
}

func verifyMessageEvents(t *testing.T, events []proto.Envelope, want string) {
	t.Helper()
	type observation struct {
		text      string
		chunks    int
		completed bool
	}
	messages := map[string]*observation{}
	var completed []string
	var sequence uint64
	for _, event := range events {
		switch event.Type {
		case proto.TypeOutputMessage:
			var value proto.OutputMessagePayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				t.Fatal(err)
			}
			if value.ID == "" {
				t.Fatal("missing message identity")
			}
			if value.Status == "in_progress" {
				if messages[value.ID] != nil || value.Text != nil {
					t.Fatal("duplicate or invalid message start")
				}
				messages[value.ID] = &observation{}
			} else {
				state := messages[value.ID]
				if value.Status != "completed" || state == nil || state.completed || value.Text == nil || state.text != *value.Text {
					t.Fatalf("incorrect completion snapshot: %+v, state %+v", value, state)
				}
				state.completed = true
				completed = append(completed, *value.Text)
			}
		case proto.TypeDelta:
			var value proto.DeltaPayload
			if err := json.Unmarshal(event.Payload, &value); err != nil {
				t.Fatal(err)
			}
			state := messages[value.ItemID]
			if state == nil || state.completed || value.Sequence != sequence+1 {
				t.Fatalf("unmatched/out-of-order delta: %+v", value)
			}
			sequence = value.Sequence
			state.text += value.Delta
			state.chunks++
		}
	}
	if len(messages) == 0 || strings.Join(completed, "") != want || sequence < 2 {
		t.Fatalf("incomplete message stream: %+v", messages)
	}
	for id, state := range messages {
		if !state.completed {
			t.Fatalf("message %s did not complete", id)
		}
	}
}
