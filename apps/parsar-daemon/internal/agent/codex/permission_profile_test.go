package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestPermissionProfileRejectsIncompatiblePreparationBeforeState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		req     proto.PromptRequestPayload
	}{
		{"builtin", ":danger-full-access", proto.PromptRequestPayload{}},
		{"whitespace", " ", proto.PromptRequestPayload{}},
		{"remote", "managed-workspace", proto.PromptRequestPayload{RemoteEnvironment: &proto.RemoteEnvironment{}}},
		{"none", "managed-workspace", proto.PromptRequestPayload{DisableExecutionEnvironment: true}},
		{"read-owner", "managed-workspace", proto.PromptRequestPayload{WorkspaceReadOnly: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated")
			t.Setenv("PARSAR_HOME", root)
			if _, _, err := prepareSessionPlan(context.Background(), tc.req, sessionConfig{permissionProfile: tc.profile}); err == nil {
				t.Fatal("incompatible profile accepted")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("rejection created state", err)
			}
		})
	}
}

func TestPermissionProfileSelectsNativeStartupConfig(t *testing.T) {
	t.Setenv("PARSAR_HOME", t.TempDir())
	t.Setenv("PARSAR_CODEX_PERMISSION_PROFILE", "managed-workspace")
	plan, _, err := prepareSessionPlan(context.Background(), proto.PromptRequestPayload{AgentStateKey: "session", DisableSubagents: true}, defaultSessionConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	config := map[string]string{}
	for _, kv := range plan.ExtraConfig {
		config[kv[0]] = kv[1]
	}
	if config["default_permissions"] != `"managed-workspace"` || plan.Sandbox != "" || plan.Permissions != "managed-workspace" {
		t.Fatal("native managed selection missing")
	}
	if config["shell_environment_policy.inherit"] != `"core"` || config["shell_environment_policy.ignore_default_excludes"] != "false" {
		t.Fatal("shell can inherit model credentials")
	}
	if config["features.shell_snapshot"] != "false" {
		t.Fatal("managed shell depends on inaccessible private snapshots")
	}
}

func TestManagedNetworkPolicySelectsNativeProfileAndRejectsMismatchBeforeState(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated")
			t.Setenv("PARSAR_HOME", root)
			req := proto.PromptRequestPayload{AgentStateKey: "session", LocalEnvironment: &proto.LocalEnvironment{ID: "environment", NetworkAccess: mode}}
			cfg := sessionConfig{permissionProfile: "managed-workspace", runtimeNetworkAccess: mode}
			wrong := req
			wrong.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "restricted"}
			if _, _, err := prepareSessionPlan(t.Context(), wrong, cfg); err == nil {
				t.Fatal("policy mismatch accepted")
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatal("policy rejection created native state")
			}
			plan, _, err := prepareSessionPlan(t.Context(), req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer plan.Cleanup()
			expected := "managed-workspace"
			if mode == "enabled" {
				expected += "-enabled"
			}
			if plan.Permissions != expected || plan.Sandbox != "" {
				t.Fatal("wrong native profile", plan.Permissions)
			}
			if _, _, err := prepareSessionPlan(t.Context(), req, sessionConfig{permissionProfile: "managed-workspace"}); err == nil {
				t.Fatal("unbound Runtime accepted explicit policy")
			}
		})
	}
}
