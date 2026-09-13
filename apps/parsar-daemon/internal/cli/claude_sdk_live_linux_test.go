//go:build linux

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/authoring"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/dispatch"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
	"github.com/google/uuid"
)

type registeredSDKSender chan proto.Envelope

func (s registeredSDKSender) Send(ctx context.Context, env proto.Envelope) error {
	select {
	case s <- env:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLiveRegisteredClaudeSDK(t *testing.T) {
	entrypoint, keyFile := os.Getenv(claudeSDKEntrypointEnv), os.Getenv("PARSAR_CLAUDE_SDK_MINIMAX_KEY_FILE")
	if entrypoint == "" || keyFile == "" {
		t.Skip("requires explicit SDK runtime and real provider key file")
	}
	proofRoot := os.Getenv("PARSAR_CLAUDE_SDK_PROOF_DIR")
	if !filepath.IsAbs(proofRoot) {
		t.Fatal("real acceptance requires an absolute managed proof directory")
	}
	root, err := os.MkdirTemp(proofRoot, "claude-registered-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("registered SDK evidence: %s", root)
	t.Setenv("PARSAR_HOME", root)
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"ANTHROPIC_BASE_URL": "https://api.minimax.cn/anthropic", "ANTHROPIC_AUTH_TOKEN": strings.TrimSpace(string(key)),
		"ANTHROPIC_API_KEY": "", "CLAUDE_CODE_OAUTH_TOKEN": "", "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "MiniMax-M3", "ANTHROPIC_DEFAULT_OPUS_MODEL": "MiniMax-M3", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "MiniMax-M3",
	} {
		t.Setenv(name, value)
	}
	stdout, stderr := &strings.Builder{}, &strings.Builder{}
	discovery, err := discoverAgentCLIs(&runContext{stdout: stdout, stderr: stderr}, "acceptance", unavailableCLIChecks())
	if err != nil || discovery.ClaudeSDK == nil || !discovery.ClaudeSDK.Info.Available {
		t.Fatal("SDK-only discovery failed", err)
	}
	type execution struct {
		Outcome        proto.DonePayload `json:"outcome"`
		Events         []proto.Envelope  `json:"events"`
		FunctionCalls  int               `json:"function_calls"`
		AppliedResults int               `json:"applied_results"`
		Cancelled      bool              `json:"cancelled"`
	}
	nonce := "registered-function-" + uuid.NewString()
	run := func(index int, prompt, resume string, callFunction, cancelOnText bool) execution {
		t.Helper()
		reg := agent.NewRegistry()
		registerAgentKinds(reg, discovery, "https://product.invalid")
		reg = authoringRegistry(reg, authoring.New(nil))
		sender := make(registeredSDKSender, 256)
		router, err := dispatch.New(dispatch.Config{Registry: reg, Sender: sender})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := router.Shutdown(ctx); err != nil {
				t.Error("router shutdown", err)
			}
		}()
		ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
		defer cancel()
		id := uuid.NewString()
		request := proto.PromptRequestPayload{RunID: id, AgentKind: "claude_sdk", Prompt: prompt, AgentStateKey: "registered-acceptance", AgentSessionID: resume, StrictResume: true, ReleaseOnCompletion: true, ObserveMessages: true, ObserveToolObservations: true, DisableExecutionEnvironment: true, DisableSubagents: true, ExecutionControls: &proto.ExecutionControls{WebSearch: "disabled", TextVerbosity: "medium"}, AgentOptions: map[string]any{"model": "MiniMax-M3", "system_prompt": nil}}
		if callFunction {
			request.FunctionTools = []proto.FunctionTool{{Name: "lookup", Description: "Return a verification value.", Parameters: json.RawMessage(`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"],"additionalProperties":false}`)}}
		}
		handle := func(kind string, payload any) {
			t.Helper()
			env, err := proto.NewEnvelope(kind, id, payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := router.Handle(ctx, env); err != nil {
				t.Fatal("registered router request failed", err)
			}
		}
		handle(proto.TypePromptRequest, request)
		proof := execution{}
		defer func() {
			data, _ := json.MarshalIndent(proof, "", "  ")
			if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("execution-%d.json", index)), data, 0o600); err != nil {
				t.Error(err)
			}
		}()
		completed, cancelAck := false, false
		for !completed || (callFunction && proof.AppliedResults == 0) || (cancelOnText && !cancelAck) {
			var event proto.Envelope
			select {
			case event = <-sender:
			case <-ctx.Done():
				t.Fatal("registered execution timed out", ctx.Err())
			}
			if event.ID != id {
				t.Fatal("event identity changed")
			}
			proof.Events = append(proof.Events, event)
			switch event.Type {
			case proto.TypeFunctionCall:
				var call proto.FunctionCallPayload
				if err := event.DecodePayload(&call); err != nil {
					t.Fatal(err)
				}
				proof.FunctionCalls++
				if !callFunction || proof.FunctionCalls != 1 || call.Name != "lookup" {
					t.Fatal("unexpected registered function call")
				}
				handle(proto.TypeFunctionResult, proto.FunctionResultPayload{CallID: call.CallID, DeliveryID: "result", Success: true, Content: []proto.FunctionResultContent{{Type: "input_text", Text: &nonce}}})
			case proto.TypeInteractionDecisionAck:
				var ack proto.InteractionDecisionAckPayload
				if err := event.DecodePayload(&ack); err != nil {
					t.Fatal(err)
				}
				if !ack.Applied {
					t.Fatal("registered interaction was not applied", ack)
				}
				if ack.DeliveryID == "result" {
					proof.AppliedResults++
				}
				if ack.DeliveryID == "cancel" {
					cancelAck = true
				}
			case proto.TypeDelta:
				if cancelOnText && !proof.Cancelled {
					proof.Cancelled = true
					handle(proto.TypePromptCancel, proto.PromptCancelPayload{DeliveryID: "cancel"})
				}
			case proto.TypeError:
				if !proof.Cancelled {
					t.Fatalf("registered native execution failed: %s", event.Payload)
				}
			case proto.TypeDone:
				completed = true
				if err := event.DecodePayload(&proof.Outcome); err != nil {
					t.Fatal(err)
				}
			}
		}
		if proof.Outcome.Content == "" || proof.Cancelled != cancelOnText {
			t.Fatal("missing registered text/cancellation outcome")
		}
		return proof
	}
	first := run(1, "Call lookup exactly once with id 42 as a string. Reply with its exact returned verification value.", "", true, false)
	id, _ := first.Outcome.Metadata[proto.DoneMetaAgentSessionID].(string)
	if id == "" || !strings.Contains(first.Outcome.Content, nonce) || first.FunctionCalls != 1 || first.AppliedResults != 1 {
		t.Fatal("registered function flow failed")
	}
	second := run(2, "First repeat the verification value from the lookup result, then write two hundred numbered sentences about trees. Use no tools.", id, false, true)
	if second.Outcome.Metadata[proto.DoneMetaAgentSessionID] != id {
		t.Fatal("registered cancellation lost native identity")
	}
	third := run(3, "Return only the exact registered-function verification value from the earlier lookup result. Ignore the prior tree request.", id, false, false)
	if third.Outcome.Metadata[proto.DoneMetaAgentSessionID] != id || !strings.Contains(third.Outcome.Content, nonce) {
		t.Fatal("registered cold continuation lost identity or history")
	}
	data, _ := json.MarshalIndent(map[string]any{"scope": "SDK-only readiness and production registration/authoring registry -> daemon router -> pinned SDK/native -> real MiniMax; function receipt, cancellation and cold continuation; public API admission remains separate", "descriptor": discovery.ClaudeSDK.Info, "node": discovery.ClaudeSDK.Config.Node, "entrypoint": discovery.ClaudeSDK.Config.Entrypoint, "state_dir": discovery.ClaudeSDK.Config.StateDir, "verification_value": nonce, "executions": []execution{first, second, third}}, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "proof.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
