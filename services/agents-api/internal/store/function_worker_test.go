package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestWorkerWaitsForToolCapabilities(t *testing.T) {
	for _, missing := range []string{"durable_input_receipts", "execution_controls", "function_tools", "tool_observations", "mcp_http_tools", "mcp_http_bearer_auth"} {
		for _, prebound := range []bool{false, true} {
			t.Run(missing+"/"+map[bool]string{false: "select", true: "bound"}[prebound], func(t *testing.T) {
				h := newFunctionHarness(t)
				configuration := functionConfiguration
				isMCP := strings.HasPrefix(missing, "mcp_http_")
				token := ""
				if isMCP {
					configuration = mcpWorkerConfiguration
				}
				if missing == "mcp_http_bearer_auth" {
					configuration, token = mcpBearerWorkerConfiguration(t, h)
				}
				if !prebound || isMCP {
					var err error
					h.session, err = h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "unbound", Configuration: []byte(configuration)})
					if err != nil {
						t.Fatal(err)
					}
				}
				if prebound && isMCP {
					if err := h.s.BindSessionDevice(t.Context(), h.tenant, h.session.ID, h.device.ID); err != nil {
						t.Fatal(err)
					}
				}
				caps := proto.AgentKindCapabilities{Streaming: true, Steering: true, DurableTurns: true, DurableInputReceipts: missing != "durable_input_receipts", EnvironmentNone: true, WebSearchControl: true, TextVerbosity: true, ExecutionControls: missing != "execution_controls", SubagentControl: true, ToolObservations: missing != "tool_observations", MCPHTTPTools: missing != "mcp_http_tools", MCPHTTPBearerAuth: missing != "mcp_http_bearer_auth", FunctionTools: missing != "function_tools" && !isMCP}
				heartbeat := func() {
					h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
				}
				heartbeat()
				deadline := time.Now().Add(3 * time.Second)
				for {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, _, _ := peer.AgentKindStatus("codex")
					if info.Capabilities.ExecutionControls == caps.ExecutionControls && info.Capabilities.FunctionTools == caps.FunctionTools && info.Capabilities.ToolObservations == caps.ToolObservations && info.Capabilities.MCPHTTPTools == caps.MCPHTTPTools && info.Capabilities.MCPHTTPBearerAuth == caps.MCPHTTPBearerAuth {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("heartbeat not applied")
					}
					time.Sleep(10 * time.Millisecond)
				}
				input := h.message("queued", "Look up ticket")
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				worker, err := execution.StartWorker(ctx, h.d)
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- worker.Run(ctx) }()
				defer func() {
					cancel()
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("worker did not stop")
					}
				}()
				time.Sleep(650 * time.Millisecond)
				current, err := h.s.GetTurn(ctx, h.tenant, h.session.ID, input.TurnID)
				if err != nil || current.Status != store.TurnQueued {
					t.Fatal(current, err)
				}
				if !prebound {
					if _, err := h.s.GetSessionDevice(ctx, h.tenant, h.session.ID); !errors.Is(err, store.ErrNotFound) {
						t.Fatal("bound an incapable device", err)
					}
				}
				caps.DurableInputReceipts, caps.ExecutionControls, caps.ToolObservations, caps.MCPHTTPTools = true, true, true, true
				caps.MCPHTTPBearerAuth, caps.FunctionTools = true, !isMCP
				heartbeat()
				request := h.read(proto.TypePromptRequest)
				var prompt proto.PromptRequestPayload
				if request.DecodePayload(&prompt) != nil {
					t.Fatal("invalid prompt")
				}
				if isMCP {
					if prompt.MCPHTTPServers == nil || len(*prompt.MCPHTTPServers) != 1 || (*prompt.MCPHTTPServers)[0].ServerLabel != "tickets" || (*prompt.MCPHTTPServers)[0].AllowedTools == nil || len(*(*prompt.MCPHTTPServers)[0].AllowedTools) != 0 || len(prompt.FunctionTools) != 0 {
						t.Fatal("MCP declaration lost during dispatch")
					}
					bearer := (*prompt.MCPHTTPServers)[0].BearerToken
					if token != "" && (bearer == nil || *bearer != token) || token == "" && bearer != nil {
						t.Fatal("dispatch lost selected authentication or authenticated an anonymous server")
					}
				} else if len(prompt.FunctionTools) != 1 || prompt.FunctionTools[0].Name != "lookup_ticket" {
					t.Fatal(prompt)
				}
				h.write(input.TurnID, proto.TypeDone, proto.DonePayload{Content: "done"})
				waitTurn(t, h, input.TurnID, store.TurnCompleted)
			})
		}
	}
}

const mcpWorkerConfiguration = `{"agent":{"model":"gpt-5.5","tools":[{"type":"mcp","server_label":"tickets","transport":{"type":"http","server_url":"http://127.0.0.1:9191/mcp"},"connection_origin":"service","allowed_tools":[],"credential_id":null,"request_metadata":{},"required":false}]},"environment":{"type":"none"}}`

func mcpBearerWorkerConfiguration(t *testing.T, h *dispatchHarness) (string, string) {
	t.Helper()
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	h.s = store.NewWithCredentialCipher(pool, cipher)
	h.d.Store = h.s
	vault, err := h.s.CreateVault(t.Context(), h.tenant, store.CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	token, endpoint := uuid.NewString(), "https://mcp.example/tools"
	_, err = h.s.CreateStaticCredential(t.Context(), h.tenant, vault.ID, store.CreateStaticCredentialInput{Name: "worker", MCPServerURL: endpoint, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	var snapshot execution.Snapshot
	if json.Unmarshal([]byte(strings.ReplaceAll(mcpWorkerConfiguration, "http://127.0.0.1:9191/mcp", endpoint)), &snapshot) != nil {
		t.Fatal("invalid worker fixture")
	}
	snapshot.VaultIDs = []string{vault.ID}
	snapshot.MCPCredentials, err = h.s.ResolveMCPCredentials(t.Context(), h.tenant, snapshot.VaultIDs, []store.MCPCredentialRequest{{ServerLabel: "tickets", ServerURL: endpoint}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(raw), token) {
		t.Fatal("unsafe worker configuration")
	}
	return string(raw), token
}
