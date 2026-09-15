package store_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/device"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/api"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/credentialcrypto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
	"github.com/google/uuid"
)

func TestRemoteMCPCredentialAdmissionAndRejectedWrites(t *testing.T) {
	_, pool := store.NewTestStore(t)
	cipher, err := credentialcrypto.New([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	s := store.NewWithCredentialCipher(pool, cipher)
	tenant := uuid.NewString()
	vault, err := s.CreateVault(t.Context(), tenant, store.CreateVaultInput{})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.CreateStaticCredential(t.Context(), tenant, vault.ID, store.CreateStaticCredentialInput{Name: "test", MCPServerURL: "https://tools.example/mcp", Token: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewAuthenticator([]api.APIKey{{OrganizationID: "test-org", ProjectID: tenant, SubjectKind: "service_account", SubjectID: "test", TenantID: tenant, TokenSHA256: device.HashCredential("test-token")}})
	if err != nil {
		t.Fatal(err)
	}
	worker, _ := publicInitialWorker(t, s)
	handler, err := api.NewHandler(s, auth, "codex", api.WithExecution(worker), api.WithEnvironmentRemoteURL("https://executor.example"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"unattached", "missing", "wrong URL", "foreign Vault", "implicit", "explicit"} {
		for _, initial := range []bool{false, true} {
			tool := map[string]any{"type": "mcp", "server_label": "tools", "connection_origin": "service", "transport": map[string]string{"type": "http", "server_url": "https://tools.example/mcp"}}
			if mode != "implicit" {
				tool["credential_id"] = credential.ID
			}
			body := map[string]any{"agent": map[string]any{"model": "model", "tools": []any{tool}}, "environment": map[string]string{"type": "self_hosted", "workspace_directory": "/remote"}, "vault_ids": []string{vault.ID}}
			switch mode {
			case "unattached":
				body["vault_ids"] = []string{}
			case "missing":
				tool["credential_id"] = uuid.NewString()
			case "wrong URL":
				tool["transport"] = map[string]string{"type": "http", "server_url": "https://other.example/mcp"}
			case "foreign Vault":
				body["vault_ids"] = []string{uuid.NewString()}
			}
			if initial {
				body["input"] = "Do not run"
			}
			raw, _ := json.Marshal(body)
			request := httptest.NewRequest(http.MethodPost, "/v1/agents/sessions", strings.NewReader(string(raw)))
			request.Header.Set("Authorization", "Bearer test-token")
			request.Header.Set("OpenAI-Beta", "agents=v1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)

			if mode == "implicit" || mode == "explicit" {
				if response.Code != http.StatusOK {
					t.Fatal("valid remote credential rejected", response.Code, response.Body)
				}
				var public struct{ ID string }
				if json.Unmarshal(response.Body.Bytes(), &public) != nil || public.ID == "" {
					t.Fatal("missing Session")
				}
				session, err := s.GetSession(t.Context(), tenant, public.ID)
				var snapshot execution.Snapshot
				if err != nil || json.Unmarshal(session.Configuration, &snapshot) != nil || len(snapshot.MCPCredentials) != 1 || snapshot.MCPCredentials[0].CredentialID != credential.ID || strings.Contains(string(session.Configuration), "synthetic-token") {
					t.Fatal("frozen credential missing or secret persisted", err)
				}
				if strings.Contains(response.Body.String(), "mcp_credentials") || strings.Contains(response.Body.String(), "synthetic-token") {
					t.Fatal("private authentication exposed")
				}
				var count int
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM environment_input_reservations WHERE session_id=$1 AND is_initial", public.ID).Scan(&count); err != nil || (count == 1) != initial {
					t.Fatal("initial reservation changed", err)
				}
				continue
			}
			if response.Code != http.StatusNotFound {
				t.Fatal("invalid reference accepted or wrong error", mode, response.Code, response.Body)
			}
			for _, table := range []string{"sessions", "environments", "turns", "turn_inputs", "session_items", "session_events", "environment_input_reservations"} {
				var count int
				where := "session_id IN (SELECT id FROM sessions WHERE tenant_id=$1)"
				if table == "sessions" {
					where = "tenant_id=$1"
				}
				if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table+" WHERE "+where, tenant).Scan(&count); err != nil || count != 0 {
					t.Fatal("rejected request wrote execution state", table, count, err)
				}
			}
		}
	}
}

func TestRemoteMCPWorkerRequiresCombinationCapability(t *testing.T) {
	for _, authenticated := range []bool{false, true} {
		for _, bound := range []bool{false, true} {
			t.Run(map[bool]string{false: "selection", true: "bound"}[bound]+map[bool]string{false: "-anonymous", true: "-bearer"}[authenticated], func(t *testing.T) {
				h := newDispatchHarness(t)
				configuration, token := mcpWorkerConfiguration, ""
				if authenticated {
					configuration, token = mcpBearerWorkerConfiguration(t, h)
				}
				enableWorkerEnvironment(t, h)
				configuration = strings.Replace(configuration, `"type":"none"`, `"type":"self_hosted","workspace_directory":"/remote"`, 1)
				session, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "remote-mcp", Configuration: json.RawMessage(configuration)})
				if err != nil {
					t.Fatal(err)
				}
				pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, session.ID, "work", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"first"}`)}})
				if err != nil {
					t.Fatal(err)
				}
				if bound {
					if err := h.s.BindSessionDevice(t.Context(), h.tenant, session.ID, h.device.ID); err != nil {
						t.Fatal(err)
					}
				}
				caps := workerEnvironmentCapabilities()
				caps.MCPHTTPTools = true
				caps.MCPHTTPBearerAuth = true
				caps.MCPHTTPRemoteEnvironment = authenticated
				heartbeat := func() {
					h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
				}
				heartbeat()
				awaitDaemonRemoteCondition(t, t.Context(), time.Second, "MCP capability", func() bool {
					peer, _ := h.registry.LookupDevice(h.device.ID)
					info, _, _ := peer.AgentKindStatus("codex")
					return info.Capabilities.MCPHTTPTools
				})
				frames := workerFrames(t, h)
				_, stop := startEnvironmentExpiryWorker(t, h.d)
				select {
				case frame := <-frames:
					t.Fatal("old peer received preparation", frame.Type)
				case <-time.After(650 * time.Millisecond):
				}
				current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, session.ID, pending.ID)
				if err != nil || current.State != store.EnvironmentInputPending || len(current.Receipts) != 0 {
					t.Fatal("old peer promoted work", err)
				}
				if !bound {
					if _, err := h.s.GetSessionDevice(t.Context(), h.tenant, session.ID); !errors.Is(err, store.ErrNotFound) {
						t.Fatal("old peer bound", err)
					}
				}
				caps.MCPHTTPRemoteEnvironment = true
				caps.MCPHTTPRemoteBearerAuth = authenticated
				heartbeat()
				frame := nextWorkerFrame(t, frames, proto.TypeExecutionPrepare)
				var prepare proto.ExecutionPreparePayload
				if frame.DecodePayload(&prepare) != nil || prepare.Configuration.RemoteEnvironment == nil || prepare.Configuration.MCPHTTPServers == nil {
					t.Fatal("combination missing from preparation")
				}
				servers := *prepare.Configuration.MCPHTTPServers
				if len(servers) != 1 || servers[0].ServerLabel != "tickets" || servers[0].AllowedTools == nil || len(*servers[0].AllowedTools) != 0 || (servers[0].BearerToken != nil) != authenticated || authenticated && *servers[0].BearerToken != token {
					t.Fatal("MCP declaration changed")
				}
				handle := acknowledgePreparation(h, frame.ID)
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 2, State: "ready"})
				started := nextWorkerFrame(t, frames, proto.TypeExecutionStart)
				var start proto.ExecutionStartPayload
				if started.DecodePayload(&start) != nil || start.Prompt != "first" {
					t.Fatal("input changed")
				}
				h.write(frame.ID, proto.TypePreparationStatus, proto.PreparationStatusPayload{Handle: handle, Revision: 3, State: "started", RunID: start.RunID})
				h.write(start.RunID, proto.TypeDone, proto.DonePayload{Content: "done"})
				got := awaitWorkerEnvironmentRun(t, t.Context(), h.s, h.tenant, pending)
				if got.Turn.Status != store.TurnCompleted {
					t.Fatal("remote MCP work failed")
				}
				nextWorkerFrame(t, frames, proto.TypeExecutionRelease)
				stop()
			})
		}
	}
}
