package codex

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func newInteractionTestSession(rpc *JSONRPCClient) (*Session, <-chan proto.Envelope) {
	out := make(chan proto.Envelope, 1)
	s := &Session{
		runID:        "run-test",
		out:          out,
		rpc:          rpc,
		cancelCtx:    context.Background(),
		interactions: newPendingCodexInteractions(),
	}
	s.registerHandlers()
	return s, out
}

func TestCodexPermissionRequestWaitsForHumanDecision(t *testing.T) {
	tc, srv, cleanup := NewTestClient()
	defer cleanup()
	s, out := newInteractionTestSession(tc.JSONRPCClient)

	command := "go test ./..."
	reason := "requires process execution"
	if err := SendServerRequest(srv, "rpc-perm-1", "item/commandExecution/requestApproval", CommandExecutionRequestApprovalParams{
		ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1", Command: &command, Reason: &reason,
	}); err != nil {
		t.Fatalf("send server request: %v", err)
	}

	var env proto.Envelope
	select {
	case env = <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("permission request was not surfaced to Parsar")
	}
	if env.Type != proto.TypePermissionRequest {
		t.Fatalf("envelope type = %q, want %q", env.Type, proto.TypePermissionRequest)
	}
	var request proto.PermissionRequestPayload
	if err := env.DecodePayload(&request); err != nil {
		t.Fatalf("decode permission payload: %v", err)
	}
	if env.ID != "run-test" || request.RequestID == "" {
		t.Fatalf("permission correlation = env.ID %q request.ID %q", env.ID, request.RequestID)
	}
	if request.Tool != "command_execution" || request.Title != command || request.Detail != reason {
		t.Fatalf("permission payload = %+v", request)
	}

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- s.SubmitPermission(context.Background(), request.RequestID, proto.PermissionDecisionPayload{Approved: true})
	}()
	reply := readCodexServerReply(t, srv)
	if err := <-submitDone; err != nil {
		t.Fatalf("submit permission: %v", err)
	}
	if reply.ID != "rpc-perm-1" || reply.Result.Decision != "accept" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestCodexPermissionsApprovalUsesDedicatedResponseSchema(t *testing.T) {
	for _, tt := range []struct {
		name       string
		approved   bool
		wantGrants bool
	}{
		{name: "approve echoes requested grants", approved: true, wantGrants: true},
		{name: "deny grants nothing", approved: false, wantGrants: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc, srv, cleanup := NewTestClient()
			defer cleanup()
			s, out := newInteractionTestSession(tc.JSONRPCClient)
			requested := map[string]any{
				"network":    map[string]any{"enabled": true},
				"fileSystem": map[string]any{"write": []any{"/workspace"}},
			}
			if err := SendServerRequest(srv, "rpc-permissions", "item/permissions/requestApproval", PermissionsRequestApprovalParams{
				Cwd: "/workspace", Permissions: requested,
			}); err != nil {
				t.Fatalf("send permissions request: %v", err)
			}
			env := <-out
			var request proto.PermissionRequestPayload
			if err := env.DecodePayload(&request); err != nil {
				t.Fatalf("decode permission payload: %v", err)
			}
			done := make(chan error, 1)
			go func() {
				done <- s.SubmitPermission(context.Background(), request.RequestID, proto.PermissionDecisionPayload{Approved: tt.approved})
			}()
			var reply struct {
				ID     string `json:"id"`
				Result struct {
					Permissions map[string]any `json:"permissions"`
					Scope       string         `json:"scope"`
					Decision    string         `json:"decision"`
				} `json:"result"`
			}
			decodeCodexReply(t, srv, &reply)
			if err := <-done; err != nil {
				t.Fatalf("submit permissions: %v", err)
			}
			if reply.Result.Scope != "turn" || reply.Result.Decision != "" {
				t.Fatalf("permissions reply = %+v", reply.Result)
			}
			_, granted := reply.Result.Permissions["network"]
			if granted != tt.wantGrants {
				t.Fatalf("permissions = %+v, want grants=%v", reply.Result.Permissions, tt.wantGrants)
			}
		})
	}
}

func TestCodexUserInputMapsAnswersByQuestionID(t *testing.T) {
	tc, srv, cleanup := NewTestClient()
	defer cleanup()
	s, out := newInteractionTestSession(tc.JSONRPCClient)

	autoResolutionMs := uint64(120_000)
	if err := SendServerRequest(srv, "rpc-ask-1", "item/tool/requestUserInput", ToolRequestUserInputParams{
		ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-ask",
		AutoResolutionMs: &autoResolutionMs,
		Questions: []ToolRequestUserInputQuestion{
			{ID: "deployment", Header: "Deploy", Question: "Where?", IsOther: true, Options: []ToolRequestUserInputOption{{Label: "Staging"}, {Label: "Production"}}},
			{ID: "checks", Header: "Checks", Question: "Which checks?", IsSecret: true, Options: []ToolRequestUserInputOption{{Label: "Unit"}, {Label: "E2E"}}},
		},
	}); err != nil {
		t.Fatalf("send server request: %v", err)
	}

	var env proto.Envelope
	select {
	case env = <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("requestUserInput was not surfaced to Parsar")
	}
	if env.Type != proto.TypePromptForUserChoice {
		t.Fatalf("envelope type = %q, want %q", env.Type, proto.TypePromptForUserChoice)
	}
	var request proto.PromptForUserChoicePayload
	if err := env.DecodePayload(&request); err != nil {
		t.Fatalf("decode user input payload: %v", err)
	}
	if len(request.Questions) != 2 || request.Questions[0].ID != "deployment" || request.Questions[1].ID != "checks" || request.Questions[0].Header != "Deploy" {
		t.Fatalf("user input payload = %+v", request)
	}
	if !request.Questions[0].IsOther || !request.Questions[1].IsSecret || request.AutoResolutionMs == nil || *request.AutoResolutionMs != autoResolutionMs {
		t.Fatalf("user input metadata = %+v", request)
	}
	s.interactions.mu.Lock()
	pending := s.interactions.asks[request.AskID]
	s.interactions.mu.Unlock()
	if pending.timeout != 2*time.Minute {
		t.Fatalf("ask timeout = %v, want 2m", pending.timeout)
	}

	submitDone := make(chan error, 1)
	go func() {
		submitDone <- s.SubmitPromptForUserChoice(context.Background(), request.AskID, proto.PromptForUserChoiceDecisionPayload{
			QuestionAnswers: []proto.PromptForUserChoiceQuestionAnswer{
				{QuestionID: "checks", Answers: []string{"Unit", "E2E"}},
				{QuestionID: "deployment", Answers: []string{"Staging"}},
			},
		})
	}()

	var reply struct {
		ID     string                       `json:"id"`
		Result ToolRequestUserInputResponse `json:"result"`
	}
	decodeCodexReply(t, srv, &reply)
	if err := <-submitDone; err != nil {
		t.Fatalf("submit user input: %v", err)
	}
	if reply.ID != "rpc-ask-1" {
		t.Fatalf("reply id = %q", reply.ID)
	}
	if got := reply.Result.Answers["deployment"].Answers; len(got) != 1 || got[0] != "Staging" {
		t.Fatalf("deployment answers = %v", got)
	}
	if got := reply.Result.Answers["checks"].Answers; len(got) != 2 || got[0] != "Unit" || got[1] != "E2E" {
		t.Fatalf("checks answers = %v", got)
	}
}

func TestCodexUserInputCancellationReturnsErrorInsteadOfEmptyAnswers(t *testing.T) {
	tc, srv, cleanup := NewTestClient()
	defer cleanup()
	s, out := newInteractionTestSession(tc.JSONRPCClient)

	if err := SendServerRequest(srv, "rpc-ask-cancel", "item/tool/requestUserInput", ToolRequestUserInputParams{
		Questions: []ToolRequestUserInputQuestion{{ID: "confirm", Header: "Confirm", Question: "Continue?"}},
	}); err != nil {
		t.Fatalf("send server request: %v", err)
	}
	var env proto.Envelope
	select {
	case env = <-out:
	case <-time.After(2 * time.Second):
		t.Fatal("requestUserInput was not surfaced to Parsar")
	}
	var request proto.PromptForUserChoicePayload
	if err := env.DecodePayload(&request); err != nil {
		t.Fatalf("decode user input payload: %v", err)
	}
	submitDone := make(chan error, 1)
	go func() {
		submitDone <- s.SubmitPromptForUserChoice(context.Background(), request.AskID, proto.PromptForUserChoiceDecisionPayload{
			Cancelled: true, Reason: "cancelled by user",
		})
	}()

	var reply struct {
		ID    string `json:"id"`
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	decodeCodexReply(t, srv, &reply)
	if err := <-submitDone; err != nil {
		t.Fatalf("cancel user input: %v", err)
	}
	if reply.ID != "rpc-ask-cancel" || reply.Error == nil || reply.Error.Code != -32001 {
		t.Fatalf("cancel reply = %+v", reply)
	}
}

func TestCodexInteractionExpiryUnblocksRuntime(t *testing.T) {
	t.Run("permission declines", func(t *testing.T) {
		tc, srv, cleanup := NewTestClient()
		defer cleanup()
		s, out := newInteractionTestSession(tc.JSONRPCClient)
		command := "deploy"
		if err := SendServerRequest(srv, "rpc-perm-timeout", "item/commandExecution/requestApproval", CommandExecutionRequestApprovalParams{Command: &command}); err != nil {
			t.Fatalf("send permission request: %v", err)
		}
		env := <-out
		var request proto.PermissionRequestPayload
		if err := env.DecodePayload(&request); err != nil {
			t.Fatalf("decode permission payload: %v", err)
		}
		expireDone := make(chan struct{})
		go func() { s.expireCodexPermission(request.RequestID); close(expireDone) }()
		reply := readCodexServerReply(t, srv)
		<-expireDone
		if reply.Result.Decision != "decline" {
			t.Fatalf("expiry decision = %q", reply.Result.Decision)
		}
		if err := s.SubmitPermission(context.Background(), request.RequestID, proto.PermissionDecisionPayload{Approved: true}); !errors.Is(err, agent.ErrUnknownPermission) {
			t.Fatalf("late permission error = %v, want ErrUnknownPermission", err)
		}
	})

	t.Run("permission profile grants nothing", func(t *testing.T) {
		tc, srv, cleanup := NewTestClient()
		defer cleanup()
		s, out := newInteractionTestSession(tc.JSONRPCClient)
		if err := SendServerRequest(srv, "rpc-profile-timeout", "item/permissions/requestApproval", PermissionsRequestApprovalParams{
			Permissions: map[string]any{"network": map[string]any{"enabled": true}},
		}); err != nil {
			t.Fatalf("send permission profile request: %v", err)
		}
		env := <-out
		var request proto.PermissionRequestPayload
		if err := env.DecodePayload(&request); err != nil {
			t.Fatalf("decode permission payload: %v", err)
		}
		done := make(chan struct{})
		go func() { s.expireCodexPermission(request.RequestID); close(done) }()
		var reply struct {
			Result PermissionsRequestApprovalResponse `json:"result"`
		}
		decodeCodexReply(t, srv, &reply)
		<-done
		if len(reply.Result.Permissions) != 0 || reply.Result.Scope != "turn" {
			t.Fatalf("expiry permissions response = %+v", reply.Result)
		}
	})

	t.Run("user input returns timeout error", func(t *testing.T) {
		tc, srv, cleanup := NewTestClient()
		defer cleanup()
		s, out := newInteractionTestSession(tc.JSONRPCClient)
		if err := SendServerRequest(srv, "rpc-ask-timeout", "item/tool/requestUserInput", ToolRequestUserInputParams{
			Questions: []ToolRequestUserInputQuestion{{ID: "q1", Header: "Confirm", Question: "Continue?"}},
		}); err != nil {
			t.Fatalf("send input request: %v", err)
		}
		var request proto.PromptForUserChoicePayload
		if err := (<-out).DecodePayload(&request); err != nil {
			t.Fatalf("decode input request: %v", err)
		}
		expireDone := make(chan struct{})
		go func() { s.expireCodexAsk(request.AskID); close(expireDone) }()
		var reply struct {
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		decodeCodexReply(t, srv, &reply)
		<-expireDone
		if reply.Error == nil || reply.Error.Code != -32001 {
			t.Fatalf("expiry reply = %+v", reply)
		}
		if err := s.SubmitPromptForUserChoice(context.Background(), request.AskID, proto.PromptForUserChoiceDecisionPayload{Answers: []string{"yes"}}); !errors.Is(err, agent.ErrUnknownAsk) {
			t.Fatalf("late input error = %v, want ErrUnknownAsk", err)
		}
	})
}

type approvalReply struct {
	ID     string                 `json:"id"`
	Result ApprovalDecisionResult `json:"result"`
}

func readCodexServerReply(t *testing.T, srv ServerSide) approvalReply {
	t.Helper()
	var reply approvalReply
	decodeCodexReply(t, srv, &reply)
	return reply
}

func decodeCodexReply(t *testing.T, srv ServerSide, target any) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- json.NewDecoder(srv.FromClient).Decode(target) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("decode Codex reply: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Codex reply timed out")
	}
}
