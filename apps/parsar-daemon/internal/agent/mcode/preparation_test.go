package mcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

func workspaceFixture(t *testing.T) (WorkspaceConfig, proto.PromptRequestPayload, string) {
	t.Helper()
	r := executionRequest(t)
	r.RunID, r.Prompt, r.ConversationID = "", "", ""
	r.DisableExecutionEnvironment = false
	r.LocalEnvironment = &proto.LocalEnvironment{ID: "environment", NetworkAccess: "disabled"}
	r.WorkDir = t.TempDir()
	record := filepath.Join(t.TempDir(), "calls")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "native")
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	script := "#!/bin/sh\nexport PARSAR_MCODE_TEST_HELPER=prepared\nexport PARSAR_MCODE_TEST_RECORD=" + quote(record) + "\nexec " + quote(exe) + " -test.run=^TestMCodeProcess$ -- \"$@\"\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return WorkspaceConfig{Binary: binary, Node: "/usr/bin/node", Bridge: "/opt/bridge.mjs", Directory: r.WorkDir, Network: "disabled", Scratch: t.TempDir(), ProtectedDirs: []string{os.Getenv("PARSAR_HOME")}}, r, record
}

func TestPreparedWorkspaceHasOneInputAndOutputOwner(t *testing.T) {
	c, r, record := workspaceFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	resource, err := NewPreparationFactory(c)(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	p := resource.(*prepared)
	t.Cleanup(func() { _ = p.Cancel(context.Background()) })
	raw, err := os.ReadFile(record)
	if err != nil || strings.Contains(string(raw), "session/prompt") {
		t.Fatalf("preparation consumed input: %q %v", raw, err)
	}
	if p.session.opts.Dir == r.WorkDir || !strings.HasPrefix(p.session.opts.Dir, p.session.opts.DataDir+string(filepath.Separator)) {
		t.Fatal("native cwd is not private")
	}
	out := make(chan proto.Envelope)
	var wg sync.WaitGroup
	winners := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := p.Start(ctx, "run", "input", out); winners <- err == nil }()
	}
	wg.Wait()
	close(winners)
	n := 0
	for winner := range winners {
		if winner {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("owners=%d", n)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	deltas, done := 0, 0
	for e := range out {
		if e.Type == proto.TypeError {
			t.Fatalf("execution: %s", e.Payload)
		}
		if e.Type == proto.TypeDelta {
			deltas++
		}
		if e.Type == proto.TypeDone {
			done++
			select {
			case <-p.session.exited:
			default:
				t.Fatal("Done before native settlement")
			}
			var d proto.DonePayload
			_ = json.Unmarshal(e.Payload, &d)
			if d.Metadata[proto.DoneMetaAgentSessionID] != "native-1" {
				t.Fatal("native identity lost")
			}
		}
	}
	if deltas != 100 || done != 1 {
		t.Fatalf("deltas=%d done=%d", deltas, done)
	}
	raw, _ = os.ReadFile(record)
	if strings.Count(string(raw), "session/prompt") != 1 {
		t.Fatalf("inputs=%s", raw)
	}
}

func TestPreparedWorkspaceCloseBeforeStart(t *testing.T) {
	c, r, _ := workspaceFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	resource, err := NewPreparationFactory(c)(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = resource.Start(ctx, "run", "input", make(chan proto.Envelope)); err == nil {
		t.Fatal("released preparation started")
	}
	if err = resource.Close(); err != nil {
		t.Fatal(err)
	}
}
