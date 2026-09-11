package agentdaemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestAuthoringSharesOnlyActiveRunSubscription(t *testing.T) {
	c, _, session, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer session.Close("test done")
	feedAgentKinds(t, conn, session, []proto.SupportedAgentKind{{Kind: "claude_code", Available: true, Capabilities: proto.AgentKindCapabilities{WorkspaceAuthoring: true}}})
	seen := make(chan string, 2)
	c.authoring = func(_ context.Context, id string, request proto.AuthoringRequestPayload) (any, error) {
		seen <- id
		return map[string]string{"operation": request.Operation}, nil
	}
	ch, err := c.StreamPrompt(t.Context(), basicInput())
	if err != nil {
		t.Fatal(err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt not sent")
	}
	var request proto.PromptRequestPayload
	for _, env := range conn.Writes() {
		if env.Type == proto.TypePromptRequest {
			_ = env.DecodePayload(&request)
		}
	}
	if !request.WorkspaceAuthoring || !strings.Contains(request.AgentOptions["system_prompt"].(string), "parsar workspace context") {
		t.Fatal("authoring not advertised to supported runtime")
	}
	foreign, _ := proto.NewEnvelope(proto.TypeAuthoringRequest, "another-run", proto.AuthoringRequestPayload{RequestID: "foreign", Operation: proto.AuthoringContext})
	conn.Feed(foreign)
	valid, _ := proto.NewEnvelope(proto.TypeAuthoringRequest, "run-1", proto.AuthoringRequestPayload{RequestID: "valid", Operation: proto.AuthoringContext})
	conn.Feed(valid)
	if !waitForWrite(t, conn, proto.TypeAuthoringResponse, 2*time.Second) {
		t.Fatal("authoring response missing")
	}
	for _, env := range conn.Writes() {
		if env.Type != proto.TypeAuthoringResponse {
			continue
		}
		var response proto.AuthoringResponsePayload
		_ = env.DecodePayload(&response)
		if env.ID != "run-1" || response.RequestID != "valid" || response.Error != "" {
			t.Fatalf("response escaped request scope: %+v", env)
		}
	}
	done, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{Content: "done"})
	conn.Feed(done)
	_, final := drainEvents(ch, t)
	if final == nil {
		t.Fatal("run completion lost")
	}
	if len(seen) != 1 || <-seen != "run-1" {
		t.Fatal("authoring accepted a foreign run")
	}
}

func TestAuthoringKeepsLegacyDaemonPromptCompatible(t *testing.T) {
	c, _, session, conn, _ := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
	defer session.Close("test done")
	c.authoring = func(context.Context, string, proto.AuthoringRequestPayload) (any, error) {
		t.Fatal("legacy runtime called authoring")
		return nil, nil
	}
	c.skillUploadToken = func(string) (string, error) { return "existing-upload-token", nil }
	ch, err := c.StreamPrompt(t.Context(), basicInput())
	if err != nil {
		t.Fatal(err)
	}
	if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
		t.Fatal("prompt not sent")
	}
	for _, env := range conn.Writes() {
		if env.Type != proto.TypePromptRequest {
			continue
		}
		var request proto.PromptRequestPayload
		_ = env.DecodePayload(&request)
		if request.WorkspaceAuthoring || strings.Contains(request.AgentOptions["system_prompt"].(string), "parsar workspace context") {
			t.Fatal("new commands injected into legacy daemon")
		}
		env := request.AgentOptions["env"].(map[string]any)
		if env["PARSAR_CAPABILITY_UPLOAD_TOKEN"] != "existing-upload-token" {
			t.Fatal("legacy upload credential changed")
		}
	}
	done, _ := proto.NewEnvelope(proto.TypeDone, "run-1", proto.DonePayload{})
	conn.Feed(done)
	drainEvents(ch, t)
}
