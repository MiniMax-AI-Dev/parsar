package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceReadPreparationLeavesExecutionStateUntouched(t *testing.T) {
	privateHarnessTestHome(t)
	request, cfg, root := preparationFixture(t)
	cfg.harnessBinary = cfg.codexBinary
	// Put fixture controls in the executable, not in the sanitized child environment.
	body, err := os.ReadFile(cfg.codexBinary)
	if err != nil {
		t.Fatal(err)
	}
	controls := "export PARSAR_PREPARATION_FAKE=1 PARSAR_PRIVATE_HARNESS_FAKE=1\n"
	for _, key := range []string{"PARSAR_PREPARATION_FRAMES", "PARSAR_PREPARATION_STATUS"} {
		controls += "export " + key + "='" + strings.ReplaceAll(os.Getenv(key), "'", "'\\''") + "'\n"
	}
	if err := os.WriteFile(cfg.codexBinary, []byte(strings.Replace(string(body), "exec ", controls+"exec ", 1)), 0700); err != nil {
		t.Fatal(err)
	}
	request.WorkspaceReadOnly = true
	request.WorkDir, request.AgentOptions, request.FunctionTools = "", nil, nil
	stable, err := allocCodexHome(request.AgentStateKey)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"config.toml", "history.jsonl"} {
		if err := os.WriteFile(filepath.Join(stable, name), []byte("preserve original state"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := newPreparation(t.Context(), request, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.plan.Cwd == stable || privateHarnessEnv(p.plan.Env, "CODEX_HOME") != p.plan.Cwd || p.plan.Model != "" || p.plan.ModelProvider != "" {
		t.Fatal("read preparation reused execution configuration")
	}
	if _, err := p.Start(t.Context(), "run", "do work", make(chan proto.Envelope, 1)); err == nil {
		t.Fatal("read-only owner started execution")
	}
	assertPreparationOnly(t, root)
	for _, name := range []string{"config.toml", "history.jsonl"} {
		data, err := os.ReadFile(filepath.Join(stable, name))
		if err != nil || string(data) != "preserve original state" {
			t.Fatal("original execution state changed", name, err)
		}
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.plan.Cwd); !os.IsNotExist(err) {
		t.Fatal("successful close left read state", err)
	}
}

func TestWorkspaceReadEnvironmentExcludesAmbientCredentials(t *testing.T) {
	input := []string{"PATH=/usr/bin", "HOME=/operator", "HTTPS_PROXY=http://proxy", "OPENAI_API_KEY=sentinel", "ANTHROPIC_API_KEY=sentinel", "CUSTOM_PROVIDER_SECRET=sentinel", "CODEX_HOME=/execution", "LD_PRELOAD=/inject", "CODEX_EXEC_SERVER_NOISE_AUTH_TOKEN=old"}
	got := workspaceReadEnvironment(input)
	if strings.Join(got, "\n") != strings.Join(input[:3], "\n") {
		t.Fatal("read child inherited execution configuration or credentials")
	}
}

func TestWorkspaceReadPreparationRejectsExecutionConfiguration(t *testing.T) {
	request, cfg, _ := preparationFixture(t)
	request.WorkspaceReadOnly = true
	if _, err := newPreparation(context.Background(), request, cfg); err == nil {
		t.Fatal("execution settings accepted as read-only")
	}
	request.WorkDir, request.AgentOptions, request.FunctionTools = "", nil, nil
	if !proto.ValidWorkspaceReadPreparation(request) {
		t.Fatal("minimal read request rejected")
	}
	if _, err := newPreparation(context.Background(), request, cfg); err == nil {
		t.Fatal("stock harness admitted a read-only preparation")
	}
}
