package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func TestWorkspaceReadPreparationLeavesExecutionStateUntouched(t *testing.T) {
	privateHarnessTestHome(t)
	request, cfg, root := preparationFixture(t)
	cfg.harnessBinary = cfg.codexBinary
	t.Setenv("PARSAR_PRIVATE_HARNESS_FAKE", "1")
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
