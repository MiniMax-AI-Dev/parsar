//go:build unix

package claudesdk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

const readyReport = `{"type":"runtime_ready","protocol":1,"node":"22.22.2","sdk":"0.3.269","mcp":"1.30.0","native":"2.1.269 (Claude Code)"}`

func TestHTTPMCPRejectsOldPackagedRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PARSAR_HOME", root)
	config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "main.js"), StateDir: filepath.Join(root, "state"), Env: []string{
		"GO_CLAUDE_READINESS_HELPER=1", "READINESS_MODE=ready", "GORACE=atexit_sleep_ms=0",
	}}
	info, err := CheckRuntime(t.Context(), config)
	if err != nil || info.SupportsHTTPMCP() {
		t.Fatal("old runtime acquired MCP support", err)
	}
	info.Features = []string{"mcp_http_tools"}
	if !info.SupportsHTTPMCP() {
		t.Fatal("runtime feature not recognized")
	}
	req := proto.PromptRequestPayload{RunID: "run", Prompt: "hello", DisableExecutionEnvironment: true,
		AgentOptions: map[string]any{"model": "fixture"}, MCPHTTPServers: &[]proto.MCPHTTPServer{{ServerLabel: "fixture", ServerURL: "https://example.invalid/mcp"}}}
	if _, err := NewFactory(config)(t.Context(), req, make(chan proto.Envelope, 1)); err == nil || !strings.Contains(err.Error(), "packaged runtime does not support HTTP MCP") {
		t.Fatalf("old runtime was not rejected before execution: %v", err)
	}
}

func TestRuntimeReadiness(t *testing.T) {
	for _, mode := range []string{"ready", "malformed", "wrong-protocol", "missing-version", "multiple", "failed", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "main.js"), Env: []string{
				"GO_CLAUDE_READINESS_HELPER=1", "READINESS_MODE=" + mode, "GORACE=atexit_sleep_ms=0",
			}}
			result, err := CheckRuntime(context.Background(), config)
			if mode == "ready" {
				if err != nil || result.SDK != "0.3.269" || result.Native != "2.1.269 (Claude Code)" {
					t.Fatalf("unexpected readiness: %+v, %v", result, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private-diagnostic") {
				t.Fatalf("failure was accepted or leaked diagnostics: %v", err)
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("probe created execution state")
			}
		})
	}
}

func TestRuntimeReadinessRejectsPathsAndMissingNode(t *testing.T) {
	for _, config := range []Config{
		{Node: "must-not-start", Entrypoint: "relative/main.js"},
		{Node: filepath.Join(t.TempDir(), "missing-node"), Entrypoint: filepath.Join(t.TempDir(), "main.js")},
	} {
		if _, err := CheckRuntime(context.Background(), config); err == nil {
			t.Fatal("accepted invalid installation")
		}
	}
}

func TestRuntimeReadinessCancellationReleasesProbe(t *testing.T) {
	root := t.TempDir()
	config := Config{Node: os.Args[0], Entrypoint: filepath.Join(root, "main.js"), Env: []string{
		"GO_CLAUDE_READINESS_HELPER=1", "READINESS_MODE=wait", "GORACE=atexit_sleep_ms=0",
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := CheckRuntime(ctx, config)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 2*time.Second {
		t.Fatalf("probe did not release on deadline: %v", err)
	}
}

func runReadinessHelper() {
	if len(os.Args) != 3 || filepath.Base(os.Args[1]) != "runtime_check.js" || filepath.Base(os.Args[2]) != "main.js" {
		os.Exit(4)
	}
	// Drain a large stderr stream without returning any of it to the caller.
	_, _ = fmt.Fprint(os.Stderr, strings.Repeat("private-diagnostic", 8192))
	switch os.Getenv("READINESS_MODE") {
	case "ready":
		_, _ = fmt.Fprintln(os.Stdout, readyReport)
	case "malformed":
		_, _ = fmt.Fprintln(os.Stdout, "not-json")
	case "wrong-protocol":
		_, _ = fmt.Fprintln(os.Stdout, strings.Replace(readyReport, `"protocol":1`, `"protocol":2`, 1))
	case "missing-version":
		_, _ = fmt.Fprintln(os.Stdout, strings.Replace(readyReport, `"mcp":"1.30.0"`, `"mcp":""`, 1))
	case "multiple":
		_, _ = fmt.Fprintln(os.Stdout, readyReport+"\n"+readyReport)
	case "failed":
		_, _ = fmt.Fprintln(os.Stdout, readyReport)
		os.Exit(2)
	case "oversized":
		_, _ = fmt.Fprintln(os.Stdout, strings.Repeat("x", 32*1024))
		time.Sleep(time.Minute)
	case "wait":
		time.Sleep(time.Minute)
	default:
		os.Exit(3)
	}
}
