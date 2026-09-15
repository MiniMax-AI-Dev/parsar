//go:build linux

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type mcpBearerTurn struct {
	Events                     []proto.Envelope  `json:"events"`
	Done                       proto.DonePayload `json:"done"`
	NativeLaunches             int               `json:"native_launches"`
	BearerEnvironmentReference string            `json:"bearer_environment_reference"`
}

func TestLiveMCPBearerGatewayColdContinuation(t *testing.T) {
	daemon, native, provider, root := mcpBearerSettings(t)
	t.Logf("private MCP bearer evidence: %s", root)
	token, runner := mcpBearerNonce(t), mcpBearerNonce(t)
	fixture := newMCPBearerFixture(t, root, token)
	capture := &mcpBearerLog{}
	proof := map[string]any{"scope": "Private gateway -> built daemon -> pinned Codex -> real MiniMax with owned HTTPS MCP; no public API or Vault execution claim", "model": "MiniMax-M3", "provider_url": "https://api.minimax.cn/v1", "daemon_sha256": mcpBearerBinaryHash(t, daemon), "codex_sha256": mcpBearerBinaryHash(t, native)}
	var turns []*mcpBearerTurn
	t.Cleanup(func() {
		mcpBearerSafeWrite(t, filepath.Join(root, "captured.log"), capture.snapshot(), token, provider)
		histories := mcpBearerScanArtifacts(t, root, token, provider)
		if !t.Failed() && histories == 0 {
			t.Error("native history artifact missing")
		}
		proof["native_history_files"], proof["mcp"], proof["turns"] = histories, fixture.observations(), turns
		proof["passed"] = !t.Failed()
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Error("cannot encode safe acceptance evidence")
			return
		}
		mcpBearerSafeWrite(t, filepath.Join(root, "proof.json"), data, token, provider)
	})
	id := uuid.NewString()
	registry := NewRegistry()
	auth := NewAuthenticator(&stubRuntimeStore{ok: true, row: device.Credential{ID: id, WorkspaceID: uuid.NewString(), Type: RuntimeTypeAgentDaemon, CredentialHash: device.HashCredential(runner)}})
	router := chi.NewRouter()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	handler := NewHandler(HandlerConfig{Authenticator: auth, Registry: registry, PublicWSURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/agent-daemon/ws", Log: func(format string, args ...any) { _, _ = fmt.Fprintf(capture, format+"\n", args...) }})
	RegisterRoutes(router, handler)
	mcpBearerStartDaemon(t, root, daemon, native, provider, fixture.caFile, server.URL, id, runner, capture)
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
	defer cancel()
	peer, err := registry.WaitForDevice(ctx, id, 30*time.Second)
	if err != nil {
		t.Fatal("built daemon did not connect through the real gateway")
	}
	t.Cleanup(func() { peer.Close("owned MCP acceptance finished") })
	ready := time.Now().Add(30 * time.Second)
	for {
		info, found, known := peer.AgentKindStatus("codex")
		if known && found && info.Available {
			if !info.Capabilities.MCPHTTPTools || !info.Capabilities.MCPHTTPBearerAuth || !info.Capabilities.ToolObservations || !info.Capabilities.DurableTurns || !info.Capabilities.EnvironmentNone {
				t.Fatal("built daemon did not advertise the required private execution capabilities")
			}
			proof["codex_descriptor"] = info
			break
		}
		if time.Now().After(ready) {
			t.Fatal("pinned Codex capability discovery did not complete")
		}
		time.Sleep(50 * time.Millisecond)
	}
	allowed, anonymousTools := []string{"remember", "fail"}, []string{"ping"}
	servers := []proto.MCPHTTPServer{{ServerLabel: "private_mcp", ServerURL: fixture.private.URL, AllowedTools: &allowed, BearerToken: &token}, {ServerLabel: "anonymous_mcp", ServerURL: fixture.anonymous.URL, AllowedTools: &anonymousTools}}
	run := func(prompt, resume string, expected map[string]string) *mcpBearerTurn {
		t.Helper()
		turn := &mcpBearerTurn{}
		turns = append(turns, turn)
		runID := uuid.NewString()
		request := proto.PromptRequestPayload{AgentKind: "codex", ConversationID: "mcp-bearer-acceptance", RunID: runID, Prompt: prompt, AgentStateKey: "mcp-bearer-acceptance", AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, ObserveMessages: true, ObserveTools: true, ObserveToolObservations: true, DisableExecutionEnvironment: true, DisableSubagents: true, MCPHTTPServers: &servers, AgentOptions: map[string]any{"model": "MiniMax-M3"}, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}}
		sub, err := peer.SubscribeDurable(runID)
		if err != nil {
			t.Fatal("cannot subscribe before real daemon dispatch")
		}
		defer peer.Unsubscribe(runID)
		envelope, err := proto.NewEnvelope(proto.TypePromptRequest, runID, request)
		if err != nil || peer.Send(ctx, envelope) != nil {
			t.Fatal("cannot dispatch the private MCP request")
		}
		mcpBearerCollectTurn(t, ctx, sub, runID, turn, expected, token, provider)
		turn.NativeLaunches = mcpBearerReleased(t, root)
		turn.BearerEnvironmentReference = mcpBearerConfigReference(t, root, token)
		return turn
	}
	first := run("Call private_mcp remember exactly once with tag first. Also call anonymous_mcp ping exactly once with tag first. Reply with the exact remembered value and the ping result. Do not use any other tool.", "", map[string]string{"remember": fixture.memory, "ping": "ANONYMOUS_OK"})
	nativeID, _ := first.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	if nativeID == "" || !strings.Contains(first.Done.Content, fixture.memory) {
		t.Fatal("first real model Turn did not return its native identity and unpredictable tool result")
	}
	second := run("Recall the exact remembered value from the preceding tool result. Call private_mcp fail exactly once with tag cold-followup. It intentionally reports an ordinary tool error; do not retry. Reply with the earlier remembered value and the exact error text. Do not call remember, ping or any other tool.", nativeID, map[string]string{"fail": "INTENTIONAL_MCP_TOOL_ERROR:cold-followup"})
	if second.Done.Metadata[proto.DoneMetaAgentSessionID] != nativeID || !strings.Contains(second.Done.Content, fixture.memory) || !strings.Contains(second.Done.Content, "INTENTIONAL_MCP_TOOL_ERROR:cold-followup") {
		t.Fatal("cold native continuation lost history, identity or ordinary error output")
	}
	if second.NativeLaunches <= first.NativeLaunches || second.BearerEnvironmentReference == first.BearerEnvironmentReference {
		t.Fatal("cold continuation did not create a fresh native process and bearer environment reference")
	}
	fixture.mu.Lock()
	valid := fixture.accepted > 0 && fixture.rejected == 2 && fixture.anonymousRequests > 0 && fixture.crossed == 0 && fixture.calls["remember"] == 1 && fixture.calls["ping"] == 1 && fixture.calls["fail"] == 1
	fixture.mu.Unlock()
	if !valid {
		t.Fatal("HTTPS authorization, per-server separation or expected tool-call counts failed")
	}
}

func mcpBearerCollectTurn(t *testing.T, ctx context.Context, sub *Subscription, runID string, turn *mcpBearerTurn, expected map[string]string, secrets ...string) {
	t.Helper()
	before, after := make(map[string]string), make(map[string]int)
	for {
		var event proto.Envelope
		select {
		case value, ok := <-sub.Events:
			if !ok {
				t.Fatal("real daemon subscription closed before Done")
			}
			event = value
		case <-ctx.Done():
			t.Fatal("real MiniMax MCP Turn timed out")
		}
		data, _ := json.Marshal(event)
		for _, secret := range secrets {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("secret appeared in a daemon event")
			}
		}
		if event.ID != runID {
			t.Fatal("real daemon event changed Run identity")
		}
		turn.Events = append(turn.Events, event)
		switch event.Type {
		case proto.TypeError, proto.TypePermissionRequest, proto.TypePromptForUserChoice:
			t.Fatal("unexpected execution failure or interaction during private MCP acceptance")
		case proto.TypeToolCall:
			var call proto.ToolCallPayload
			if event.DecodePayload(&call) != nil || call.Observation == nil {
				t.Fatal("missing normalized native tool observation")
			}
			obs := call.Observation
			output, wanted := expected[obs.Name]
			server := "private_mcp"
			if obs.Name == "ping" {
				server = "anonymous_mcp"
			}
			if !wanted || obs.Kind != "mcp" || obs.Server != server || call.ID == "" {
				t.Fatal("unexpected native tool identity or server")
			}
			if call.Stage == "before" {
				if obs.Status != "in_progress" || before[call.ID] != "" {
					t.Fatal("invalid native tool start observation")
				}
				before[call.ID] = obs.Name
				continue
			}
			status := "completed"
			if obs.Name == "fail" {
				status = "failed"
			}
			if call.Stage != "after" || before[call.ID] != obs.Name || obs.Status != status || !bytes.Contains(obs.Output, []byte(output)) || (len(obs.Error) != 0 && string(obs.Error) != "null") {
				t.Fatal("native tool result, lifecycle or ordinary error semantics changed")
			}
			after[obs.Name]++
		case proto.TypeDone:
			if event.DecodePayload(&turn.Done) != nil || sub.Err() != nil || turn.Done.Content == "" || turn.Done.Metadata[proto.DoneMetaAgentSessionType] != "codex_thread" {
				t.Fatal("invalid native Done or incomplete gateway delivery")
			}
			for name := range expected {
				if after[name] != 1 {
					t.Fatal("expected exactly one complete native observation per requested tool")
				}
			}
			return
		}
	}
}
