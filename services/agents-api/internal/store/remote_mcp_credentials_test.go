package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/execution"
	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func TestRemoteMCPCredentialFailurePrecedesPreparation(t *testing.T) {
	for _, mode := range []string{"missing key", "deleted", "tampered"} {
		t.Run(mode, func(t *testing.T) {
			h := newDispatchHarness(t)
			configuration, token := mcpBearerWorkerConfiguration(t, h)
			enableWorkerEnvironment(t, h)
			configuration = strings.Replace(configuration, `"type":"none"`, `"type":"self_hosted","workspace_directory":"/remote"`, 1)
			session, err := h.s.CreateSession(t.Context(), h.tenant, store.CreateSessionInput{Creator: store.FixtureCreator(), Engine: "codex", IdempotencyKey: "remote-auth-failure", Configuration: json.RawMessage(configuration)})
			if err != nil {
				t.Fatal(err)
			}
			if err = h.s.BindSessionDevice(t.Context(), h.tenant, session.ID, h.device.ID); err != nil {
				t.Fatal(err)
			}
			pending, err := h.s.ReserveEnvironmentInput(t.Context(), h.tenant, session.ID, "work", []store.Input{{Kind: "message", Payload: json.RawMessage(`{"text":"must not execute"}`)}})
			if err != nil {
				t.Fatal(err)
			}
			var snapshot execution.Snapshot
			if json.Unmarshal([]byte(configuration), &snapshot) != nil {
				t.Fatal("invalid fixture")
			}
			binding := snapshot.MCPCredentials[0]
			public, pool := store.NewTestStore(t)
			executionStore := h.s
			switch mode {
			case "missing key":
				executionStore = public
			case "deleted":
				if _, err := h.s.DeleteCredential(t.Context(), h.tenant, binding.VaultID, binding.CredentialID); err != nil {
					t.Fatal(err)
				}
			case "tampered":
				if _, err := pool.Exec(t.Context(), "UPDATE vault_credentials SET token_ciphertext=set_byte(token_ciphertext,15,get_byte(token_ciphertext,15) # 1) WHERE id=$1", binding.CredentialID); err != nil {
					t.Fatal(err)
				}
			}
			lease, err := executionStore.AcquireExecutionLease(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = lease.Close(context.Background()) })
			h.d.Store = lease.Store()
			caps := workerEnvironmentCapabilities()
			caps.MCPHTTPTools, caps.MCPHTTPBearerAuth, caps.MCPHTTPRemoteEnvironment, caps.MCPHTTPRemoteBearerAuth = true, true, true, true
			h.write("", proto.TypeHeartbeat, proto.HeartbeatPayload{SupportedAgentKinds: []proto.SupportedAgentKind{{Kind: "codex", Available: true, Capabilities: caps}}})
			awaitDaemonRemoteCondition(t, t.Context(), time.Second, "remote authentication capability", func() bool {
				peer, _ := h.registry.LookupDevice(h.device.ID)
				info, _, _ := peer.AgentKindStatus("codex")
				return info.Capabilities.MCPHTTPRemoteBearerAuth
			})
			h.d.EnvironmentConnection = func(context.Context, store.Session, store.Environment) (execution.EnvironmentConnection, error) {
				t.Error("failed secret lookup reached connection setup")
				return execution.EnvironmentConnection{}, errors.New("unexpected connection")
			}
			run, err := h.d.RunEnvironmentInput(t.Context(), h.tenant, session.ID, pending.ID)
			if err == nil || run.Turn.ID != "" || strings.Contains(err.Error(), token) {
				t.Fatal("failed authentication started work or exposed secret")
			}
			if mode == "missing key" && !errors.Is(err, store.ErrCredentialStorageUnavailable) {
				t.Fatal("missing key failure changed", err)
			}
			if mode == "deleted" && !errors.Is(err, store.ErrNotFound) {
				t.Fatal("deleted binding failure changed", err)
			}
			current, err := h.s.GetEnvironmentInputReservation(t.Context(), h.tenant, session.ID, pending.ID)
			if err != nil || current.State != store.EnvironmentInputPending || len(current.Receipts) != 0 || !current.Deadline.Equal(pending.Deadline) {
				t.Fatal("secret failure mutated input", err)
			}
			assertEnvironmentExpiryHasNoHistory(t, pool, session.ID)
		})
	}
}
