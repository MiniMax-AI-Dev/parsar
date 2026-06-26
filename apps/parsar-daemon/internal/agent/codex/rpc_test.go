package codex_test

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
)

// TestJSONRPCClient_NotificationDispatch verifies the dispatch loop
// routes inbound notifications to a registered handler.
func TestJSONRPCClient_NotificationDispatch(t *testing.T) {
	tc, srv, cleanup := codex.NewTestClient()
	defer cleanup()

	var observed atomic.Int64
	tc.OnNotification("turn/started", func(p json.RawMessage) {
		observed.Add(1)
	})

	if err := codex.SendNotification(srv, "turn/started", map[string]any{"turn": map[string]any{"id": "t1"}}); err != nil {
		t.Fatalf("send: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if observed.Load() >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("notification handler never fired")
}

// TestJSONRPCClient_ServerRequestRoundTrip exercises the inbound server-
// request branch: codex sends a request, the daemon's handler returns a
// result, the reply gets written back on stdin (the test reads it from
// the FromClient side).
func TestJSONRPCClient_ServerRequestRoundTrip(t *testing.T) {
	tc, srv, cleanup := codex.NewTestClient()
	defer cleanup()

	tc.OnServerRequest("item/commandExecution/requestApproval",
		func(_ json.RawMessage, _ any) (any, error) {
			return map[string]any{"decision": "accept"}, nil
		})

	if err := codex.SendServerRequest(srv, "req-1", "item/commandExecution/requestApproval",
		map[string]any{"command": "ls"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	decoder := json.NewDecoder(srv.FromClient)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var reply map[string]any
		if err := decoder.Decode(&reply); err == nil {
			if reply["id"] != "req-1" {
				t.Fatalf("reply id = %v", reply["id"])
			}
			result, _ := reply["result"].(map[string]any)
			if result["decision"] != "accept" {
				t.Fatalf("reply result = %v", reply)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no reply observed within deadline")
}

// TestJSONRPCClient_UnhandledServerRequestReplies_MethodNotFound
// confirms the daemon doesn't leave codex hanging when a server-request
// arrives for an unregistered method — it must reply with -32601.
func TestJSONRPCClient_UnhandledServerRequestReplies_MethodNotFound(t *testing.T) {
	_, srv, cleanup := codex.NewTestClient()
	defer cleanup()

	if err := codex.SendServerRequest(srv, "req-2", "no/such/method", nil); err != nil {
		t.Fatalf("send: %v", err)
	}

	decoder := json.NewDecoder(srv.FromClient)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var reply map[string]any
		if err := decoder.Decode(&reply); err == nil {
			errBody, _ := reply["error"].(map[string]any)
			if errBody == nil {
				t.Fatalf("no error body: %v", reply)
			}
			if code, _ := errBody["code"].(float64); int(code) != -32601 {
				t.Fatalf("error code = %v (want -32601)", errBody["code"])
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no error reply observed within deadline")
}
