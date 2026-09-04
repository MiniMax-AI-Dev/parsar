package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestSetSkillExtraRootsUsesCodexRPC(t *testing.T) {
	client, server, cleanup := NewTestClient()
	defer cleanup()

	result := make(chan error, 1)
	go func() {
		result <- setSkillExtraRoots(context.Background(), client.JSONRPCClient, []string{"/managed/skills"})
	}()

	decoder := json.NewDecoder(server.FromClient)
	var request struct {
		ID     string                    `json:"id"`
		Method string                    `json:"method"`
		Params SkillsExtraRootsSetParams `json:"params"`
	}
	if err := decoder.Decode(&request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Method != "skills/extraRoots/set" {
		t.Fatalf("method = %q", request.Method)
	}
	if len(request.Params.ExtraRoots) != 1 || request.Params.ExtraRoots[0] != "/managed/skills" {
		t.Fatalf("params = %+v", request.Params)
	}
	response, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{}})
	if _, err := server.ToClient.Write(append(response, '\n')); err != nil {
		t.Fatalf("write response: %v", err)
	}
	if err := <-result; err != nil {
		t.Fatalf("setSkillExtraRoots: %v", err)
	}
}

func TestPrepareManagedSkillsPrunesWhenPayloadOmitsSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARSAR_HOME", home)
	stale := filepath.Join(home, "runtime", "codex", "state", "conv-1", "agent-1", "codex", "skills", "stale")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := prepareManagedSkills(context.Background(), nil, proto.PromptRequestPayload{
		AgentStateKey: "conv-1/agent-1/codex",
	})
	if err != nil {
		t.Fatalf("prepareManagedSkills: %v", err)
	}
	if root != "" {
		t.Fatalf("root = %q, want empty", root)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale skill still exists: %v", err)
	}
}

func TestEffectiveAgentStateKeyFallsBackToConversation(t *testing.T) {
	got := effectiveAgentStateKey(proto.PromptRequestPayload{
		ConversationID: "conv-legacy",
		RunID:          "run-ignored",
	})
	if got != "_legacy_conversation/conv-legacy/codex" {
		t.Fatalf("state key = %q", got)
	}
}
