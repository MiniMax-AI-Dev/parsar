package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const persistedChild = `{"id":"child","parentThreadId":"root","createdAt":100,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root"}}}}`
const completedSpawn = `{"threadId":"root","turnId":"turn","item":{"id":"spawn","type":"collabAgentToolCall","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child"]}}`

func identitySession(t *testing.T) (*Session, ServerSide, <-chan JsonRpcRequest, <-chan proto.Envelope) {
	t.Helper()
	client, server, cleanup := NewTestClient()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	out := make(chan proto.Envelope, 64)
	s := &Session{runID: "run", rpc: client.JSONRPCClient, out: out,
		cancelCtx: ctx, cancelFn: cancel, cfg: defaultSessionConfig(), bufs: NewItemBuffers()}
	if err := s.bindThreadResult(json.RawMessage(`{"thread":{"id":"root"}}`), ""); err != nil {
		t.Fatal(err)
	}
	s.beginRootTurn("root", "turn")
	s.startSubagentObservations()
	s.registerHandlers()
	requests := make(chan JsonRpcRequest, 64)
	go func() {
		defer close(requests)
		decoder := json.NewDecoder(server.FromClient)
		for {
			var req JsonRpcRequest
			if decoder.Decode(&req) != nil {
				return
			}
			requests <- req
		}
	}()
	t.Cleanup(func() {
		cancel()
		cleanup()
		select {
		case <-s.subagents.done:
		case <-time.After(time.Second):
			t.Error("metadata worker outlived owner")
		}
	})
	return s, server, requests, out
}

func identityRequest(t *testing.T, requests <-chan JsonRpcRequest) JsonRpcRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("metadata request absent")
	}
	return JsonRpcRequest{}
}

func identityReply(t *testing.T, server ServerSide, request JsonRpcRequest, result string) {
	t.Helper()
	if err := json.NewEncoder(server.ToClient).Encode(map[string]any{"id": request.ID, "result": json.RawMessage(result)}); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentIdentitySettlesBeforeFrozenRootTerminal(t *testing.T) {
	s, server, requests, out := identitySession(t)
	s.observeSubagentIdentity(json.RawMessage(completedSpawn))
	s.observeSubagentIdentity(json.RawMessage(completedSpawn))
	request := identityRequest(t, requests)
	if request.Method != "thread/list" {
		t.Fatal(request.Method)
	}
	params, _ := json.Marshal(request.Params)
	var query map[string]any
	_ = json.Unmarshal(params, &query)
	if query["parentThreadId"] != "root" || query["useStateDbOnly"] != true || query["limit"] != float64(100) || len(query["modelProviders"].([]any)) != 0 {
		t.Fatal("lookup is not explicitly persisted and parent-scoped", string(params))
	}
	notify := func(method, raw string) {
		t.Helper()
		if err := SendNotification(server, method, json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	notify("item/completed", `{"threadId":"root","turnId":"turn","item":{"type":"agentMessage","id":"root-message","text":"root result"}}`)
	notify("turn/completed", `{"threadId":"child","turn":{"id":"child-turn","status":"completed"}}`)
	notify("turn/completed", `{"threadId":"root","turn":{"id":"turn","status":"completed"}}`)
	barrier := make(chan struct{}, 1)
	s.rpc.OnNotification("test/barrier", func(json.RawMessage) { barrier <- struct{}{} })
	notify("test/barrier", `{}`)
	select {
	case <-barrier:
	case <-time.After(time.Second):
		t.Fatal("root terminal blocked the native reader")
	}
	notify("item/completed", `{"threadId":"root","turnId":"turn","item":{"type":"agentMessage","id":"late","text":"late mutation"}}`)
	identityReply(t, server, request, `{"data":[`+persistedChild+`],"nextCursor":null}`)
	var kinds []string
	for {
		select {
		case env, open := <-out:
			if !open {
				if len(kinds) != 2 || kinds[0] != proto.TypeSubagentIdentity || kinds[1] != proto.TypeDone {
					t.Fatal("identity missing, duplicated or delivered after root terminal", kinds)
				}
				return
			}
			kinds = append(kinds, env.Type)
			if env.Type == proto.TypeSubagentIdentity {
				var value proto.SubagentIdentityPayload
				if env.DecodePayload(&value) != nil || value.NativeID != "child" || value.ParentNativeID != "root" || value.NativeCreatedAt != 100 || value.ParentTurnID != "turn" || value.SourceItemID != "spawn" {
					t.Fatal(value)
				}
			}
			if env.Type == proto.TypeDone {
				var done proto.DonePayload
				if env.DecodePayload(&done) != nil || done.Content != "root result" || done.Metadata[proto.DoneMetaAgentSessionID] != "root" {
					t.Fatal("pending metadata changed frozen root outcome", done)
				}
			}
		case <-time.After(time.Second):
			t.Fatal("settled metadata did not release root terminal")
		}
	}
}

func TestSubagentIdentityRejectsUnverifiedMetadata(t *testing.T) {
	for _, row := range []string{
		`{"id":"child","parentThreadId":"foreign","createdAt":100,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root"}}}}`,
		`{"id":"child","parentThreadId":"root","createdAt":100,"source":{"subAgent":{"thread_spawn":{"parent_thread_id":"foreign"}}}}`,
		`{"id":"child","parentThreadId":"root","createdAt":100,"source":"cli"}`,
		`{"id":"child","parentThreadId":"root","source":{"subAgent":{"thread_spawn":{"parent_thread_id":"root"}}}}`,
	} {
		t.Run(row, func(t *testing.T) {
			s, server, requests, out := identitySession(t)
			s.observeSubagentIdentity(json.RawMessage(completedSpawn))
			request := identityRequest(t, requests)
			s.emitDone("root result", nil)
			identityReply(t, server, request, `{"data":[`+row+`],"nextCursor":null}`)
			select {
			case env := <-out:
				if env.Type != proto.TypeDone {
					t.Fatal("unverified identity escaped", env.Type)
				}
			case <-time.After(time.Second):
				t.Fatal("failed verification blocked terminal")
			}
		})
	}
}

func TestSubagentIdentityRetriesPersistenceAndUsesOpaqueCursor(t *testing.T) {
	s, server, requests, out := identitySession(t)
	s.observeSubagentIdentity(json.RawMessage(completedSpawn))
	identityReply(t, server, identityRequest(t, requests), `{"data":[],"nextCursor":null}`)
	identityReply(t, server, identityRequest(t, requests), `{"data":[],"nextCursor":"opaque-native-cursor"}`)
	request := identityRequest(t, requests)
	params, _ := json.Marshal(request.Params)
	var query map[string]any
	_ = json.Unmarshal(params, &query)
	if query["cursor"] != "opaque-native-cursor" {
		t.Fatal(string(params))
	}
	identityReply(t, server, request, `{"data":[`+persistedChild+`],"nextCursor":null}`)
	select {
	case env := <-out:
		if env.Type != proto.TypeSubagentIdentity {
			t.Fatal(env.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("persisted child not observed")
	}
}

func TestSubagentIdentityTerminalDeadlineBoundsQueuedDiscovery(t *testing.T) {
	s, _, requests, out := identitySession(t)
	for i := range 64 {
		s.observeSubagentIdentity(json.RawMessage(fmt.Sprintf(`{"threadId":"root","turnId":"turn","item":{"id":"spawn-%d","type":"collabAgentToolCall","tool":"spawnAgent","status":"completed","senderThreadId":"root","receiverThreadIds":["child-%d"]}}`, i, i)))
	}
	identityRequest(t, requests) // The native reader accepts the request but gives no reply.
	started := time.Now()
	s.emitDone("root result", nil)
	select {
	case env := <-out:
		if env.Type != proto.TypeDone || time.Since(started) > 4*time.Second {
			t.Fatal("queued lookups extended root lifetime", env.Type)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("metadata worker did not respect shared terminal deadline")
	}
}
