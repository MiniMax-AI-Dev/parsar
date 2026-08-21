package codex

import (
	"context"
	"encoding/json"
	"testing"
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
