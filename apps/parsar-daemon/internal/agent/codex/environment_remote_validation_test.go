package codex

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func remoteEnvironmentRequest() proto.PromptRequestPayload {
	return proto.PromptRequestPayload{AgentKind: "codex", AgentStateKey: "remote-test", ReleaseOnCompletion: true, StrictResume: true,
		RemoteEnvironment: &proto.RemoteEnvironment{ID: "environment-test", WorkspaceDirectory: "/executor-only",
			ConnectionURL: "https://registry.example", ConnectionToken: "synthetic-harness-token"}}
}

func TestRemoteEnvironmentValidatesBeforeLocalSetup(t *testing.T) {
	cases := map[string]func(*proto.PromptRequestPayload){
		"none conflict":     func(r *proto.PromptRequestPayload) { r.DisableExecutionEnvironment = true },
		"retained harness":  func(r *proto.PromptRequestPayload) { r.ReleaseOnCompletion = false },
		"silent new thread": func(r *proto.PromptRequestPayload) { r.StrictResume = false },
		"missing state":     func(r *proto.PromptRequestPayload) { r.AgentStateKey = "" },
		"authoring":         func(r *proto.PromptRequestPayload) { r.WorkspaceAuthoring = true },
		"skills":            func(r *proto.PromptRequestPayload) { r.AgentOptions = map[string]any{"skills": []any{"local"}} },
		"MCP": func(r *proto.PromptRequestPayload) {
			r.AgentOptions = map[string]any{"mcp_servers": map[string]any{"local": map[string]any{}}}
		},
		"plugins": func(r *proto.PromptRequestPayload) {
			r.AgentOptions = map[string]any{"plugin_dirs": []string{"/local"}}
		},
		"identity":           func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.ID = "../another" },
		"relative workspace": func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.WorkspaceDirectory = "relative" },
		"home workspace":     func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.WorkspaceDirectory = "~/remote" },
		"URL credentials": func(r *proto.PromptRequestPayload) {
			r.RemoteEnvironment.ConnectionURL = "https://private:secret@registry.example"
		},
		"URL query":        func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.ConnectionURL += "?token=secret" },
		"plaintext remote": func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.ConnectionURL = "http://registry.example" },
		"missing token":    func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.ConnectionToken = "" },
		"invalid token":    func(r *proto.PromptRequestPayload) { r.RemoteEnvironment.ConnectionToken += "\n" },
		"transport options": func(r *proto.PromptRequestPayload) {
			r.AgentOptions = map[string]any{"env": map[string]any{"CODEX_EXEC_SERVER_URL": "none"}}
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			r := remoteEnvironmentRequest()
			r.WorkDir = filepath.Join(t.TempDir(), "must-not-be-created")
			change(&r)
			_, err := newSession(context.Background(), r, make(chan proto.Envelope, 8), defaultSessionConfig())
			if err == nil || strings.Contains(err.Error(), "synthetic-harness-token") || strings.Contains(err.Error(), "private:secret") {
				t.Fatalf("invalid or unsafe rejection: %v", err)
			}
			if _, err := os.Stat(r.WorkDir); !os.IsNotExist(err) {
				t.Fatal("invalid binding reached local setup")
			}
		})
	}
}

func TestRemoteEnvironmentRejectsAmbientTransport(t *testing.T) {
	t.Setenv("CODEX_EXEC_SERVER_URL", "none")
	if err := validateRemoteEnvironmentRequest(remoteEnvironmentRequest()); err == nil {
		t.Fatal("ambient native transport accepted")
	}
}

func TestRemoteEnvironmentKeepsPathsAndCredentialsSeparate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PARSAR_HOME", home)
	r := remoteEnvironmentRequest()
	r.WorkDir = filepath.Join(home, "harness")
	r.RemoteEnvironment.ConnectionURL = "http://127.0.0.1:34567/"
	r.RemoteEnvironment.WorkspaceDirectory = "/remote-environment-" + filepath.Base(home)
	r.AgentOptions = map[string]any{"skills": []any{}, "mcp_servers": map[string]any{}}
	if err := validateRemoteEnvironmentRequest(r); err != nil {
		t.Fatal(err)
	}
	plan, skillRoot, err := prepareSessionPlan(t.Context(), r, defaultSessionConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if plan.Cwd != r.WorkDir || skillRoot != "" || len(plan.Environments) != 1 || plan.Environments[0].Cwd != r.RemoteEnvironment.WorkspaceDirectory || plan.Environments[0].EnvironmentID != "remote" {
		t.Fatal("remote execution changed harness cwd or installed local skills")
	}
	if _, err := os.Stat(r.RemoteEnvironment.WorkspaceDirectory); !os.IsNotExist(err) {
		t.Fatal("remote workspace exists on the harness host")
	}
	env := map[string]string{}
	for _, entry := range plan.Env {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	if env["CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN"] != r.RemoteEnvironment.ConnectionToken || env["CODEX_EXEC_SERVER_NOISE_ENVIRONMENT_ID"] != r.RemoteEnvironment.ID || env["CODEX_EXEC_SERVER_NOISE_REGISTRY_URL"] != "http://127.0.0.1:34567" {
		t.Fatal("native launch did not consume the transient connection")
	}
	config := map[string]string{}
	for _, pair := range plan.ExtraConfig {
		config[pair[0]] = pair[1]
	}
	if config["shell_environment_policy.inherit"] != `"core"` || config["shell_environment_policy.ignore_default_excludes"] != "false" {
		t.Fatal("credential inheritance policy is not explicit")
	}
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if strings.Contains(string(data), r.RemoteEnvironment.ConnectionToken) {
			t.Errorf("connection credential persisted in %s", path)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(plan.Environments)
	if err != nil || strings.Contains(string(wire), r.RemoteEnvironment.ConnectionToken) {
		t.Fatal("secret in native selection")
	}
}

func TestRemoteEnvironmentCapabilityRequiresVerifiedVersion(t *testing.T) {
	for _, version := range []string{"", "codex-cli 0.141.0", "codex-cli 0.153.4", "codex-cli 0.154.0", "some binary"} {
		if SupportsRemoteEnvironment(version) != (version == "codex-cli 0.153.4") {
			t.Fatalf("unexpected capability for %q", version)
		}
	}
}
