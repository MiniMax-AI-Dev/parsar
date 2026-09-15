//go:build linux

package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func TestLiveOutputSchemaGatewayColdContinuation(t *testing.T) {
	if os.Getenv("PARSAR_OUTPUT_SCHEMA_LIVE") != "1" {
		t.Skip("real output-schema acceptance requires PARSAR_OUTPUT_SCHEMA_LIVE=1")
	}
	daemon, native, provider, root := mcpBearerSettings(t)
	t.Logf("private output-schema evidence: %s", root)
	token, runner := mcpBearerNonce(t), mcpBearerNonce(t)
	capture := &mcpBearerLog{}
	var turns []*mcpBearerTurn
	proof := map[string]any{"scope": "Private gateway -> built daemon -> pinned Codex -> real MiniMax; no public json_schema compatibility claim", "model": "MiniMax-M3", "provider_url": "https://api.minimax.cn/v1", "codex_version": "0.153.4", "daemon_sha256": mcpBearerBinaryHash(t, daemon), "codex_sha256": mcpBearerBinaryHash(t, native)}
	t.Cleanup(func() {
		if err := os.Remove(filepath.Join(root, "runtime/parsar-daemon/execution/auth.json")); err != nil && !os.IsNotExist(err) {
			t.Error("cannot remove owned daemon credential artifact")
		}
		mcpBearerSafeWrite(t, filepath.Join(root, "captured.log"), capture.snapshot(), token, runner, provider)
		histories := mcpBearerScanArtifacts(t, root, token, provider)
		mcpBearerScanArtifacts(t, root, runner, provider)
		launches, active := mcpBearerProcesses(root, false)
		if active != 0 || (!t.Failed() && histories == 0) {
			t.Error("native history missing or owned native process remains after cleanup")
		}
		proof["native_history_files"], proof["native_launches"], proof["native_active_after_cleanup"] = histories, launches, active
		proof["turns"], proof["passed"] = turns, !t.Failed()
		data, err := json.MarshalIndent(proof, "", "  ")
		if err != nil {
			t.Error("cannot encode output-schema evidence")
			return
		}
		mcpBearerSafeWrite(t, filepath.Join(root, "proof.json"), data, token, runner, provider)
	})
	fixture := newMCPBearerFixture(t, root, token)
	id, registry := uuid.NewString(), NewRegistry()
	auth := NewAuthenticator(&stubRuntimeStore{ok: true, row: device.Credential{ID: id, WorkspaceID: uuid.NewString(), Type: RuntimeTypeAgentDaemon, CredentialHash: device.HashCredential(runner)}})
	router := chi.NewRouter()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	RegisterRoutes(router, NewHandler(HandlerConfig{Authenticator: auth, Registry: registry, PublicWSURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/agent-daemon/ws", Log: func(format string, args ...any) { _, _ = fmt.Fprintf(capture, format+"\n", args...) }}))
	mcpBearerStartDaemon(t, root, daemon, native, provider, fixture.caFile, server.URL, id, runner, capture)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	peer, err := registry.WaitForDevice(ctx, id, 30*time.Second)
	if err != nil {
		t.Fatal("built daemon did not connect through the real gateway")
	}
	t.Cleanup(func() { peer.Close("owned output-schema acceptance finished") })
	deadline := time.Now().Add(30 * time.Second)
	for {
		info, found, known := peer.AgentKindStatus("codex")
		if known && found && info.Available {
			if !info.Capabilities.OutputSchema || !info.Capabilities.MessageItems || !info.Capabilities.DurableTurns || !info.Capabilities.EnvironmentNone || !info.Capabilities.MCPHTTPTools || !info.Capabilities.MCPHTTPBearerAuth || !info.Capabilities.ToolObservations || !info.Capabilities.ExecutionControls || !info.Capabilities.SubagentControl {
				t.Fatal("built daemon lacks required private output-schema capabilities")
			}
			proof["codex_descriptor"] = info
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pinned Codex capability discovery did not complete")
		}
		time.Sleep(50 * time.Millisecond)
	}
	allowed := []string{"remember"}
	servers := []proto.MCPHTTPServer{{ServerLabel: "private_mcp", ServerURL: fixture.private.URL, AllowedTools: &allowed, BearerToken: &token}}
	schema := func(phase string) json.RawMessage {
		return json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"memory":{"type":"string","description":"The exact value returned by remember in the first turn"},"phase":{"type":"string","enum":[%q]}},"required":["memory","phase"],"additionalProperties":false}`, phase))
	}
	firstSchema, resumedSchema := schema("first"), schema("resumed")
	proof["schemas"], proof["expected_memory"] = []json.RawMessage{firstSchema, resumedSchema}, fixture.memory
	run := func(prompt, resume, phase string, outputSchema json.RawMessage, expectedTools map[string]string) *mcpBearerTurn {
		t.Helper()
		turn := &mcpBearerTurn{}
		turns = append(turns, turn)
		runID := uuid.NewString()
		request := proto.PromptRequestPayload{AgentKind: "codex", ConversationID: "output-schema-acceptance", RunID: runID, Prompt: prompt, AgentStateKey: "output-schema-acceptance", AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, ObserveMessages: true, ObserveTools: true, ObserveToolObservations: true, DisableExecutionEnvironment: true, DisableSubagents: true, MCPHTTPServers: &servers, OutputSchema: outputSchema, AgentOptions: map[string]any{"model": "MiniMax-M3"}, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}}
		sub, err := peer.SubscribeDurable(runID)
		if err != nil {
			t.Fatal("cannot subscribe before real daemon dispatch")
		}
		defer peer.Unsubscribe(runID)
		envelope, err := proto.NewEnvelope(proto.TypePromptRequest, runID, request)
		if err != nil || peer.Send(ctx, envelope) != nil {
			t.Fatal("cannot dispatch the private output-schema request")
		}
		mcpBearerCollectTurn(t, ctx, sub, runID, turn, expectedTools, token, runner, provider)
		turn.NativeLaunches = mcpBearerReleased(t, root)
		turn.BearerEnvironmentReference = mcpBearerConfigReference(t, root, token)
		outputSchemaMessages(t, turn)
		if phase != "" {
			var value map[string]string
			if json.Unmarshal([]byte(turn.Done.Content), &value) != nil || len(value) != 2 || value["memory"] != fixture.memory || value["phase"] != phase {
				t.Fatal("structured Done is not the expected JSON object")
			}
		}
		return turn
	}
	first := run("Call private_mcp remember exactly once with tag first. Remember its exact value and return it in the required response format. Do not call any other tool. Emit no introductory or progress messages; emit only the final response.", "", "first", firstSchema, map[string]string{"remember": fixture.memory})
	nativeID, _ := first.Done.Metadata[proto.DoneMetaAgentSessionID].(string)
	if nativeID == "" {
		t.Fatal("first Turn did not return its native identity")
	}
	run("Recall the exact value returned by remember in the preceding turn and return it in the required response format. Do not call any tool. Emit only the final response.", nativeID, "resumed", resumedSchema, nil)
	plain := "PLAIN_" + mcpBearerNonce(t)
	proof["expected_plain_text"] = plain
	third := run("Reply with exactly this plain text, without quotes, JSON, markup or explanation: "+plain+". Do not call any tool.", nativeID, "", nil, nil)
	if strings.TrimSpace(third.Done.Content) != plain {
		t.Fatal("schema omission did not restore ordinary text output")
	}
	for i, turn := range turns[1:] {
		if turn.Done.Metadata[proto.DoneMetaAgentSessionID] != nativeID || turn.NativeLaunches <= turns[i].NativeLaunches {
			t.Fatal("cold continuation lost native identity or reused a native process")
		}
	}
	fixture.mu.Lock()
	valid := fixture.calls["remember"] == 1 && fixture.crossed == 0
	fixture.mu.Unlock()
	proof["mcp"] = fixture.observations()
	if !valid {
		t.Fatal("unpredictable first-turn memory was fetched again or authorization crossed servers")
	}
}

func outputSchemaMessages(t *testing.T, turn *mcpBearerTurn) {
	t.Helper()
	started, completed := make(map[string]bool), make(map[string]bool)
	var texts []string
	for _, event := range turn.Events {
		if event.Type != proto.TypeOutputMessage {
			continue
		}
		var message proto.OutputMessagePayload
		if event.DecodePayload(&message) != nil || message.ID == "" {
			t.Fatal("invalid normalized output message")
		}
		switch message.Status {
		case "in_progress":
			if started[message.ID] || completed[message.ID] {
				t.Fatal("duplicate normalized message start")
			}
			started[message.ID] = true
		case "completed":
			if completed[message.ID] || message.Text == nil {
				t.Fatal("duplicate or missing normalized message completion")
			}
			completed[message.ID] = true
			if *message.Text != "" {
				texts = append(texts, *message.Text)
			}
		default:
			t.Fatal("unexpected normalized message status")
		}
	}
	for id := range started {
		if !completed[id] {
			t.Fatal("normalized message start has no completion")
		}
	}
	if len(texts) == 0 || strings.Join(texts, "\n\n") != turn.Done.Content {
		t.Fatal("normalized completed message text differs from Done")
	}
}
