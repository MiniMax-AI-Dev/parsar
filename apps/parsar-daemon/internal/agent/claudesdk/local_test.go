package claudesdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestLocalWorkspaceBindingNetworkAndRequiredHistory(t *testing.T) {
	config := workspaceFixture(t)
	config.Workspace.PublicDirectory = config.Workspace.Directory
	config.Workspace.NetworkAccess = "enabled"
	req := workspaceRequest()
	req.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "enabled"}
	req.RequireExistingNativeSession = true
	start, _, err := prepare(config, req)
	if err != nil || !start.RequireHistory || start.Workspace.NetworkAccess != "enabled" {
		t.Fatal(start, err)
	}
	req.LocalEnvironment.NetworkAccess = "disabled"
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("accepted different Runtime network policy")
	}
	req.LocalEnvironment.NetworkAccess = "enabled"
	config.Workspace.PublicDirectory = config.Workspace.HomeDir
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("accepted a different public workspace")
	}
	alias := filepath.Join(filepath.Dir(config.Workspace.Directory), "alias")
	if err := os.Symlink(config.Workspace.Directory, alias); err != nil {
		t.Fatal(err)
	}
	config.Workspace.PublicDirectory = alias
	if _, _, err := prepare(config, req); err == nil {
		t.Fatal("accepted mutable workspace alias")
	}
}

func TestWorkspaceProviderCredentialsReplaceAmbientSelection(t *testing.T) {
	config := workspaceFixture(t)
	original := slices.Clone(config.Env)
	req := workspaceRequest()
	req.AgentOptions["claude_provider"] = map[string]any{"base_url": "https://provider.example/anthropic", "bearer_token": "selected-secret"}
	start, env, err := prepare(config, req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(start)
	if strings.Contains(string(raw), "selected-secret") || !slices.Equal(original, config.Env) {
		t.Fatal("provider leaked or mutated shared configuration")
	}
	if !slices.Contains(env, "ANTHROPIC_AUTH_TOKEN=selected-secret") || slices.Contains(env, "ANTHROPIC_AUTH_TOKEN=selected-provider-fixture") {
		t.Fatal("provider selection was not exclusive")
	}
	for _, value := range []any{nil, "secret", map[string]any{"base_url": "http://provider.example", "bearer_token": "secret"}, map[string]any{"base_url": "https://user:pass@provider.example", "bearer_token": "secret"}} {
		req.AgentOptions["claude_provider"] = value
		if _, _, err := prepare(config, req); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatal("unsafe provider accepted or disclosed")
		}
	}
}
