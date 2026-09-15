package codex

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestRootNotificationIsolation(t *testing.T) {
	for _, childStarted := range []bool{false, true} {
		t.Run(map[bool]string{false: "child without thread started", true: "child thread started"}[childStarted], func(t *testing.T) {
			out := make(chan proto.Envelope, 64)
			s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: defaultSessionConfig(),
				rpc: NewJSONRPCClient(JSONRPCConfig{}), bufs: NewItemBuffers(), observeMessages: true,
				observeTools: true, observeToolObservations: true}
			s.registerHandlers()
			s.setThreadID("root")
			notify := func(method, params string) { t.Helper(); scopeNotification(t, s, method, params) }
			notify("thread/tokenUsage/updated", `{"threadId":"root","turnId":"previous","tokenUsage":{"total":{"inputTokens":100,"cachedInputTokens":20,"outputTokens":10,"reasoningOutputTokens":2,"totalTokens":110}}}`)
			notify("turn/started", `{"threadId":"root","turn":{"id":"root-turn"}}`)
			notify("item/agentMessage/delta", `{"threadId":"root","turnId":"root-turn","itemId":"shared","delta":"root "}`)
			notify("item/reasoning/textDelta", `{"threadId":"root","turnId":"root-turn","itemId":"thought","delta":"root thought"}`)
			notify("error", `{"threadId":"root","turnId":"root-turn","message":"root retry"}`)
			notify("thread/tokenUsage/updated", `{"threadId":"root","turnId":"root-turn","tokenUsage":{"total":{"inputTokens":120,"cachedInputTokens":25,"outputTokens":15,"reasoningOutputTokens":3,"totalTokens":135}}}`)
			rootEvents := len(out)
			if childStarted {
				notify("thread/started", `{"thread":{"id":"child"}}`)
			}
			for _, coord := range []struct{ threadID, turnID string }{
				{"child", "child-turn"}, {"unrelated", "root-turn"}, {"root", "stale-turn"}, {"", "root-turn"}, {"root", ""},
			} {
				params := func(tail string) string {
					return `{"threadId":"` + coord.threadID + `","turnId":"` + coord.turnID + `",` + tail + `}`
				}
				notify("turn/started", params(`"turn":{"id":"`+coord.turnID+`"}`))
				notify("item/agentMessage/delta", params(`"itemId":"shared","delta":"foreign"`))
				notify("item/reasoning/summaryTextDelta", params(`"itemId":"thought","delta":"foreign"`))
				notify("item/started", params(`"item":{"type":"agentMessage","id":"shared","text":"foreign"}`))
				notify("item/completed", params(`"item":{"type":"agentMessage","id":"shared","text":"foreign"}`))
				notify("item/completed", params(`"item":{"type":"reasoning","id":"thought","text":"foreign"}`))
				notify("item/started", params(`"item":{"type":"commandExecution","id":"tool","command":"foreign"}`))
				notify("item/completed", params(`"item":{"type":"commandExecution","id":"tool","status":"completed"}`))
				notify("thread/tokenUsage/updated", params(`"tokenUsage":{"total":{"inputTokens":999,"cachedInputTokens":999,"outputTokens":999,"reasoningOutputTokens":999,"totalTokens":1998}}`))
				if coord.turnID != "" {
					notify("thread/tokenUsage/updated", params(`"usage":{"inputTokens":999,"outputTokens":999}`))
				}
				notify("error", params(`"message":"foreign error"`))
				notify("error", params(`"error":{"message":"foreign native error"},"willRetry":false`))
				notify("turn/completed", params(`"turn":{"id":"`+coord.turnID+`","status":"completed","usage":{"inputTokens":999,"outputTokens":999}}`))
				notify("turn/failed", params(`"turn":{"id":"`+coord.turnID+`","status":"failed"}`))
			}
			for _, method := range []string{"turn/started", "turn/completed", "turn/failed", "error", "item/started", "item/completed", "item/agentMessage/delta", "thread/tokenUsage/updated"} {
				notify(method, `{"threadId":42,"turnId":"root-turn","turn":{"id":"root-turn"}}`)
				notify(method, `{}`)
			}
			if s.terminal.Load() || s.currentThreadID() != "root" || len(out) != rootEvents {
				t.Fatalf("foreign notification changed Run ownership/output: terminal=%v thread=%q events=%d want=%d", s.terminal.Load(), s.currentThreadID(), len(out), rootEvents)
			}
			if s.bufs.AgentText["shared"] != "root " || s.bufs.Reasoning["thought"] != "root thought" || s.peekLastErrText() != "root retry" || s.takeFinalText() != "" {
				t.Fatal("foreign notification changed root buffers")
			}
			if s.latestUsage == nil || s.latestUsage.InputTokens != 20 || s.latestUsage.OutputTokens != 5 || s.usageTurnID != "root-turn" {
				t.Fatalf("foreign notification changed root usage: %+v", s.latestUsage)
			}
			s.steering.mu.Lock()
			turnID, stopped := s.steering.id, s.steering.stopped
			s.steering.mu.Unlock()
			if stopped || turnID != "root-turn" {
				t.Fatalf("foreign notification changed steering: %q stopped=%v", turnID, stopped)
			}
			notify("turn/started", `{"threadId":"root","turn":{"id":"root-turn"}}`)
			notify("item/agentMessage/delta", `{"threadId":"root","turnId":"root-turn","itemId":"shared","delta":"result"}`)
			notify("item/completed", `{"threadId":"root","turnId":"root-turn","item":{"type":"agentMessage","id":"shared","text":"root result"}}`)
			notify("item/completed", `{"threadId":"root","turnId":"root-turn","item":{"type":"reasoning","id":"thought"}}`)
			notify("turn/completed", `{"threadId":"root","turn":{"id":"root-turn","status":"completed"}}`)
			notify("turn/completed", `{"threadId":"root","turn":{"id":"root-turn","status":"completed","usage":{"inputTokens":999}}}`)
			var doneCount int
			for env := range out {
				if env.Type == proto.TypeDone {
					doneCount++
					var done proto.DonePayload
					if err := env.DecodePayload(&done); err != nil {
						t.Fatal(err)
					}
					if done.Content != "root result" || done.Metadata[proto.DoneMetaAgentSessionID] != "root" || done.Usage.Tokens == nil || done.Usage.Tokens.TotalTokens != 25 {
						t.Fatalf("incorrect root completion: %+v", done)
					}
				}
			}
			if doneCount != 1 || s.deltaSeq.Load() != 2 || s.thinkingSeq.Load() != 1 {
				t.Fatalf("root output duplicated or polluted: done=%d delta=%d thinking=%d", doneCount, s.deltaSeq.Load(), s.thinkingSeq.Load())
			}
		})
	}
}

func TestNotificationsCannotEstablishRootIdentity(t *testing.T) {
	s := &Session{cfg: defaultSessionConfig(), rpc: NewJSONRPCClient(JSONRPCConfig{}), bufs: NewItemBuffers()}
	s.registerHandlers()
	scopeNotification(t, s, "thread/started", `{"thread":{"id":"unrelated"}}`)
	scopeNotification(t, s, "turn/started", `{"threadId":"unrelated","turn":{"id":"unrelated-turn"}}`)
	scopeNotification(t, s, "thread/tokenUsage/updated", `{"threadId":"unrelated","turnId":"unrelated-turn","tokenUsage":{"total":{"inputTokens":999}}}`)
	scopeNotification(t, s, "turn/completed", `{"threadId":"unrelated","turn":{"id":"unrelated-turn","status":"completed"}}`)
	if s.currentThreadID() != "" || s.usageTurnID != "" || s.usageTotal.InputTokens != 0 || s.terminal.Load() {
		t.Fatal("notification acquired root identity or state before its RPC result")
	}
}

func TestRootNativeErrorNotification(t *testing.T) {
	for name, errorJSON := range map[string]string{
		"object variant": `{"message":"native provider failure","codexErrorInfo":{"httpConnectionFailed":{"httpStatusCode":502}},"additionalDetails":null}`,
		"string details": `{"message":"native provider failure","codexErrorInfo":"other","additionalDetails":"request failed"}`,
		"null metadata":  `{"message":"native provider failure","codexErrorInfo":null,"additionalDetails":null}`,
	} {
		for _, location := range []string{"error notification", "completion"} {
			t.Run(name+"/"+location, func(t *testing.T) {
				out := make(chan proto.Envelope, 4)
				s := &Session{runID: "run", out: out, cancelCtx: context.Background(), cfg: defaultSessionConfig(), rpc: NewJSONRPCClient(JSONRPCConfig{})}
				s.registerHandlers()
				s.setThreadID("root")
				scopeNotification(t, s, "turn/started", `{"threadId":"root","turn":{"id":"turn"}}`)
				for _, threadID := range []string{"child", "root"} {
					turnID := "turn"
					if threadID == "root" {
						turnID = "stale-turn"
					}
					scopeNotification(t, s, "error", `{"threadId":"`+threadID+`","turnId":"`+turnID+`","error":`+errorJSON+`,"willRetry":false}`)
					scopeNotification(t, s, "turn/completed", `{"threadId":"`+threadID+`","turn":{"id":"`+turnID+`","status":"failed","error":`+errorJSON+`}}`)
				}
				if s.terminal.Load() || s.peekLastErrText() != "" || len(out) != 0 {
					t.Fatal("foreign native failure changed the root Run")
				}
				completionError := "null"
				if location == "error notification" {
					scopeNotification(t, s, "error", `{"threadId":"root","turnId":"turn","error":`+errorJSON+`,"willRetry":false}`)
					if s.peekLastErrText() != "native provider failure" || s.terminal.Load() {
						t.Fatal("root error must be buffered until completion")
					}
				} else {
					completionError = errorJSON
				}
				scopeNotification(t, s, "turn/completed", `{"threadId":"root","turn":{"id":"turn","status":"failed","error":`+completionError+`}}`)
				if !s.terminal.Load() {
					t.Fatal("valid native failure did not settle the root Run")
				}
				var errorCount, doneCount int
				for env := range out {
					switch env.Type {
					case proto.TypeError:
						errorCount++
						var payload proto.ErrorPayload
						if err := env.DecodePayload(&payload); err != nil || payload.Error != "native provider failure" {
							t.Fatalf("native failure lost: %+v, %v", payload, err)
						}
					case proto.TypeDone:
						doneCount++
						var done proto.DonePayload
						if err := env.DecodePayload(&done); err != nil {
							t.Fatal(err)
						}
						if done.Content != "native provider failure" || done.Metadata[proto.DoneMetaAgentSessionID] != "root" {
							t.Fatalf("native failure lost: %+v", done)
						}
					}
				}
				if errorCount != 1 || doneCount != 1 {
					t.Fatalf("terminal counts: errors=%d done=%d", errorCount, doneCount)
				}
			})
		}
	}
}

func scopeNotification(t *testing.T, s *Session, method, params string) {
	t.Helper()
	frame, err := json.Marshal(map[string]any{"method": method, "params": json.RawMessage(params)})
	if err != nil {
		t.Fatal(err)
	}
	s.rpc.dispatchFrame(frame)
}
