package codex

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestRootRPCNotificationOrdering(t *testing.T) {
	for _, resume := range []bool{false, true} {
		for _, notificationFirst := range []bool{false, true} {
			name := map[bool]string{false: "start", true: "resume"}[resume] + "/" + map[bool]string{false: "reply first", true: "notification first"}[notificationFirst]
			t.Run(name, func(t *testing.T) {
				client, server, cleanup := NewTestClient()
				defer cleanup()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				out := make(chan proto.Envelope, 16)
				s := &Session{runID: "run", out: out, rpc: client.JSONRPCClient, cancelCtx: ctx, bufs: NewItemBuffers(), cfg: defaultSessionConfig()}
				s.registerHandlers()
				barrier := make(chan struct{}, 1)
				client.OnNotification("test/barrier", func(json.RawMessage) { barrier <- struct{}{} })
				notify := func(method, params string) {
					t.Helper()
					if err := SendNotification(server, method, json.RawMessage(params)); err != nil {
						t.Fatal(err)
					}
				}
				reply := func(id string, result string) {
					t.Helper()
					if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": id, "result": json.RawMessage(result)}); err != nil {
						t.Fatal(err)
					}
				}
				waitResult := func(result <-chan error) {
					t.Helper()
					select {
					case err := <-result:
						if err != nil {
							t.Fatal(err)
						}
					case <-ctx.Done():
						t.Fatal("RPC did not finish")
					}
				}
				result := make(chan error, 1)
				go func() {
					if resume {
						result <- s.resumeThread("root", SessionPlan{})
					} else {
						result <- s.startThread(SessionPlan{})
					}
				}()
				decoder := json.NewDecoder(server.FromClient)
				var request JsonRpcRequest
				if err := decoder.Decode(&request); err != nil {
					t.Fatal(err)
				}
				notify("thread/started", `{"thread":{"id":"unrelated","sessionId":"root"}}`)
				notify("thread/tokenUsage/updated", `{"threadId":"unrelated","turnId":"previous","tokenUsage":{"total":{"inputTokens":999}}}`)
				baseline := `{"threadId":"root","turnId":"previous","tokenUsage":{"total":{"inputTokens":100,"cachedInputTokens":20,"outputTokens":10,"reasoningOutputTokens":2,"totalTokens":110}}}`
				if resume && notificationFirst {
					notify("thread/tokenUsage/updated", baseline)
				}
				reply(request.ID, `{"thread":{"id":"root"},"model":"test-model"}`)
				if resume && !notificationFirst {
					notify("thread/tokenUsage/updated", baseline)
				}
				notify("thread/started", `{"thread":{"id":"child"}}`)
				notify("test/barrier", `{}`)
				<-barrier
				waitResult(result)
				if s.currentThreadID() != "root" || (resume && s.usageTotal.InputTokens != 100) {
					t.Fatalf("root/resume baseline raced reply delivery: thread=%q usage=%+v", s.currentThreadID(), s.usageTotal)
				}
				go func() {
					_, err := client.requestWithResult(ctx, "turn/start", TurnStartParams{ThreadID: "root"}, s.bindTurnResult)
					result <- err
				}()
				if err := decoder.Decode(&request); err != nil {
					t.Fatal(err)
				}
				if !notificationFirst {
					reply(request.ID, `{"turn":{"id":"current"}}`)
				}
				notify("turn/started", `{"threadId":"root","turn":{"id":"current"}}`)
				notify("item/agentMessage/delta", `{"threadId":"root","turnId":"current","itemId":"message","delta":"root answer"}`)
				notify("thread/tokenUsage/updated", `{"threadId":"root","turnId":"current","tokenUsage":{"total":{"inputTokens":130,"cachedInputTokens":25,"outputTokens":20,"reasoningOutputTokens":3,"totalTokens":150}}}`)
				notify("turn/started", `{"threadId":"child","turn":{"id":"child-turn"}}`)
				notify("turn/completed", `{"threadId":"child","turn":{"id":"child-turn","status":"completed"}}`)
				notify("item/completed", `{"threadId":"root","turnId":"current","item":{"type":"agentMessage","id":"message"}}`)
				notify("turn/completed", `{"threadId":"root","turn":{"id":"current","status":"completed"}}`)
				if notificationFirst {
					reply(request.ID, `{"turn":{"id":"current"}}`)
				}
				notify("test/barrier", `{}`)
				<-barrier
				waitResult(result)
				var doneCount int
				for env := range out {
					if env.Type != proto.TypeDone {
						continue
					}
					doneCount++
					var done proto.DonePayload
					if err := env.DecodePayload(&done); err != nil {
						t.Fatal(err)
					}
					wantInput := int32(130)
					if resume {
						wantInput = 30
					}
					if done.Content != "root answer" || done.Metadata[proto.DoneMetaAgentSessionID] != "root" || done.Usage.InputTokens != wantInput {
						t.Fatalf("reply ordering lost root output/usage: %+v", done)
					}
				}
				if doneCount != 1 {
					t.Fatalf("root completion count = %d", doneCount)
				}
			})
		}
	}
}

func TestThreadRPCResponseRequiresRootIdentity(t *testing.T) {
	for _, raw := range []string{`{}`, `{"thread":{"id":""}}`, `{"thread":{"sessionId":"root"}}`, `{"thread":{"id":42}}`, `{"thread":{"id":"other"}}`} {
		t.Run(raw, func(t *testing.T) {
			client, server, cleanup := NewTestClient()
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s := &Session{rpc: client.JSONRPCClient, cancelCtx: ctx, cfg: defaultSessionConfig()}
			s.registerHandlers()
			result := make(chan error, 1)
			go func() { result <- s.resumeThread("root", SessionPlan{}) }()
			var request JsonRpcRequest
			if err := json.NewDecoder(server.FromClient).Decode(&request); err != nil {
				t.Fatal(err)
			}
			if err := SendNotification(server, "thread/tokenUsage/updated", json.RawMessage(`{"threadId":"root","turnId":"old","tokenUsage":{"total":{"inputTokens":999}}}`)); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": json.RawMessage(raw)}); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if err == nil || s.currentThreadID() != "" || s.usageTotal.InputTokens != 0 || s.resumeUsageTotal != nil {
					t.Fatalf("invalid reply acquired root identity or usage: err=%v thread=%q usage=%+v", err, s.currentThreadID(), s.usageTotal)
				}
			case <-ctx.Done():
				t.Fatal("invalid reply did not settle the RPC")
			}
		})
	}
	s := &Session{}
	if err := s.bindThreadResult(json.RawMessage(`{"thread":{"id":"root"}}`), "root"); err != nil {
		t.Fatal(err)
	}
	if err := s.bindThreadResult(json.RawMessage(`{"thread":{"id":"other"}}`), ""); err == nil || s.currentThreadID() != "root" {
		t.Fatal("later reply replaced root")
	}
}
