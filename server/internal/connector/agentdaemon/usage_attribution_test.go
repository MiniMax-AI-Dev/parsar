package agentdaemon

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/agentdaemon/binding"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/connector"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

func TestManagedUsageUsesLaunchedProviderAcrossEngines(t *testing.T) {
	for kind, reported := range map[string]string{"claude_code": "claude_code", "codex": "openai", "pi": "parsar", "opencode": "opencode"} {
		t.Run(kind, func(t *testing.T) {
			c, _, session, conn, binder := newWiredHarness(t, "dev-1", "conv-1", "pa-1")
			defer session.Close("test done")
			feedAgentKinds(t, conn, session, []proto.SupportedAgentKind{{Kind: kind, Available: true, Capabilities: proto.AgentKindCapabilities{Streaming: true, Usage: true}}})
			if err := binder.Bind(t.Context(), binding.Binding{ConversationID: "conv-1", AgentID: "pa-1", DeviceID: "dev-1", AgentKind: kind, WorkDir: "/workspace"}); err != nil {
				t.Fatal(err)
			}
			secretService, err := secrets.New("test-master-key")
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := secretService.Encrypt(map[string]any{"api_key": "sk-private-test-value"})
			if err != nil {
				t.Fatal(err)
			}
			resolver := &fakeModelResolver{
				runtime: store.ModelRuntime{ModelID: "model-1", ModelKey: "MiniMax-M3", ProviderType: "minimax-cn", Adapter: "@ai-sdk/anthropic", BaseURL: "https://model.example.com", SecretID: "secret-1", ProviderConfig: map[string]any{"supported_endpoint_types": []string{"anthropic", "openai", "openai-response"}}},
				secret:  store.SecretPayload{SecretRead: store.SecretRead{Status: "active"}, EncryptedPayload: encrypted},
			}
			c.modelResolver, c.secrets = resolver, secretService
			input := basicInput()
			input.WorkspaceID = "ws-1"
			input.AgentConfig = map[string]any{"agent_kind": kind, "model_id": "model-1"}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			stream, err := c.StreamPrompt(ctx, input)
			if err != nil {
				t.Fatal(err)
			}
			if !waitForWrite(t, conn, proto.TypePromptRequest, 2*time.Second) {
				t.Fatal("prompt was not dispatched")
			}
			// A later catalog edit must not reattribute an already-started run.
			resolver.runtime.ProviderType = "different-provider"
			native := proto.Usage{Provider: reported, Model: "MiniMax-M3", InputTokens: 123, OutputTokens: 7, CostUSD: 0.25, Raw: map[string]any{"native_counter": float64(9)}}
			usage, _ := proto.NewEnvelope(proto.TypeUsage, input.RunID, proto.UsagePayload{Usage: native})
			done, _ := proto.NewEnvelope(proto.TypeDone, input.RunID, proto.DonePayload{Content: "ok", Usage: native})
			conn.Feed(usage)
			conn.Feed(done)
			var measured []store.UsageInput
			for event := range stream {
				if event.Type == connector.EventError {
					t.Fatal(event.Error)
				}
				if event.Usage != nil {
					measured = append(measured, *event.Usage)
				}
				if event.Final != nil {
					measured = append(measured, event.Final.Usage)
				}
			}
			if len(measured) != 2 || !reflect.DeepEqual(measured[0], measured[1]) {
				t.Fatalf("usage/done mismatch: %#v", measured)
			}
			got := measured[0]
			if got.Provider != "minimax-cn" || got.Model != native.Model || got.InputTokens != native.InputTokens || got.OutputTokens != native.OutputTokens || got.CostUSD != native.CostUSD {
				t.Fatalf("attribution or measurements changed: %#v", got)
			}
			want := map[string]any{"agent_kind": kind, "reported_provider": reported, "provider_source": "managed_model"}
			if !reflect.DeepEqual(got.Raw["parsar_usage"], want) || got.Raw["native_counter"] != float64(9) {
				t.Fatalf("metadata/raw fields lost: %#v", got.Raw)
			}
			encoded, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "sk-private-test-value") {
				t.Fatal("credential leaked into usage")
			}
			if resolver.resolveCalls+resolver.resolveUserCalls != 1 {
				t.Fatal("model resolved again while handling usage")
			}
		})
	}
}

func TestUsageAttributionPreservesUnmanagedAndAbsentUsage(t *testing.T) {
	attribution := usageAttribution{agentKind: "codex", modelProvider: "minimax-cn"}
	if got := attribution.fromProto(proto.Usage{}); !reflect.DeepEqual(got, store.UsageInput{}) {
		t.Fatalf("absent usage became a record: %#v", got)
	}
	native := proto.Usage{Provider: "native-provider", Model: "native-model", InputTokens: 10, Raw: map[string]any{"native": true}}
	unmanaged := (usageAttribution{agentKind: "pi"}).fromProto(native)
	if unmanaged.Provider != native.Provider || unmanaged.Model != native.Model || unmanaged.InputTokens != 10 {
		t.Fatalf("unmanaged usage changed: %#v", unmanaged)
	}
	if got := unmanaged.Raw["parsar_usage"].(map[string]any)["provider_source"]; got != "adapter" {
		t.Fatal(got)
	}
	if len(native.Raw) != 1 {
		t.Fatal("source raw map was mutated")
	}
	for _, usage := range []proto.Usage{{InputTokens: 10}, {Provider: "native-provider", CostUSD: 0}} {
		got := attribution.fromProto(usage)
		if got.Provider != "minimax-cn" || got.InputTokens != usage.InputTokens || got.CostUSD != usage.CostUSD {
			t.Fatalf("reported usage dropped: %#v", got)
		}
	}
}
