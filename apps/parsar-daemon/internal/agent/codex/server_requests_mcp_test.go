package codex

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestCodexMCPConfirmationUsesPermissionLifecycle(t *testing.T) {
	for _, action := range []string{"approve", "deny", "expire"} {
		t.Run(action, func(t *testing.T) {
			tc, srv, cleanup := NewTestClient()
			defer cleanup()
			s, out := newInteractionTestSession(tc.JSONRPCClient)
			defer s.stopCodexInteractionTimers()
			params := json.RawMessage(`{"threadId":"thread-1","turnId":"turn-1","serverName":"qa-service-desk","mode":"form","message":"Allow get_test_ticket?","requestedSchema":{"type":"object","properties":{}},"_meta":{"codex_approval_kind":"mcp_tool_call","persist":["session","always"]}}`)
			if err := SendServerRequest(srv, "rpc-mcp", "mcpServer/elicitation/request", params); err != nil {
				t.Fatal(err)
			}
			var env proto.Envelope
			select {
			case env = <-out:
			case <-time.After(2 * time.Second):
				t.Fatal("MCP confirmation was not surfaced")
			}
			var request proto.PermissionRequestPayload
			if err := env.DecodePayload(&request); err != nil {
				t.Fatal(err)
			}
			if env.Type != proto.TypePermissionRequest || env.ID != "run-test" || request.RequestID == "" || request.Tool != "mcp:qa-service-desk" || request.Title != "Allow get_test_ticket?" {
				t.Fatalf("incorrect confirmation: %+v / %+v", env, request)
			}
			done := make(chan error, 1)
			go func() {
				if action == "expire" {
					s.expireCodexPermission(request.RequestID)
					done <- nil
					return
				}
				done <- s.SubmitPermission(t.Context(), request.RequestID, proto.PermissionDecisionPayload{Approved: action == "approve"})
			}()
			var reply struct {
				ID     string                     `json:"id"`
				Result map[string]json.RawMessage `json:"result"`
			}
			decodeCodexReply(t, srv, &reply)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			wantAction, wantContent := `"decline"`, `null`
			if action == "approve" {
				wantAction, wantContent = `"accept"`, `{}`
			}
			if reply.ID != "rpc-mcp" || len(reply.Result) != 2 || string(reply.Result["action"]) != wantAction || string(reply.Result["content"]) != wantContent {
				t.Fatalf("incorrect MCP response: %+v", reply)
			}
			if err := s.SubmitPermission(t.Context(), request.RequestID, proto.PermissionDecisionPayload{Approved: true}); !errors.Is(err, agent.ErrUnknownPermission) {
				t.Fatalf("resolved confirmation accepted again: %v", err)
			}
		})
	}
}

func TestCodexMCPElicitationRejectsUnsupportedInputWithoutApproval(t *testing.T) {
	for _, params := range []string{
		`{"serverName":"qa","mode":"form","message":"Enter name","requestedSchema":{"type":"object","properties":{"name":{"type":"string"}}}}`,
		`{"serverName":"qa","mode":"url","message":"Sign in","url":"https://example.test/login","elicitationId":"login"}`,
		`{"serverName":"qa","mode":"form","message":"Confirm","requestedSchema":{"type":"object","properties":{},"required":["name"]}}`,
		`{"serverName":"qa","mode":"form","message":"Confirm","requestedSchema":{"type":"object"}}`,
		`{"mode":"form","requestedSchema":{"type":"object","properties":{}}}`,
	} {
		t.Run(params, func(t *testing.T) {
			tc, srv, cleanup := NewTestClient()
			defer cleanup()
			s, out := newInteractionTestSession(tc.JSONRPCClient)
			defer s.stopCodexInteractionTimers()
			if err := SendServerRequest(srv, "rpc-unsupported", "mcpServer/elicitation/request", json.RawMessage(params)); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			decodeCodexReply(t, srv, &reply)
			if reply.Error == nil || reply.Error.Code != -32603 {
				t.Fatalf("unsupported input was not rejected: %+v", reply)
			}
			select {
			case env := <-out:
				t.Fatalf("unsupported input became an approval: %+v", env)
			default:
			}
		})
	}
}
