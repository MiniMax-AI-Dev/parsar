package codex

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// The controlled native response checks adapter ordering. Real MCP initialization
// and model execution are verified separately against the pinned harness.
func TestRequiredMCPWaitsForNativeThreadAndNeverRestartsFailedResume(t *testing.T) {
	for _, mode := range []string{"new ready", "new failed", "resume ready", "resume failed"} {
		t.Run(mode, func(t *testing.T) {
			req, cfg, root := preparationFixture(t)
			req.StrictResume = true
			req.AgentOptions = map[string]any{"model": "fixture-model"}
			servers := []proto.MCPHTTPServer{{ServerLabel: "docs", ServerURL: "https://docs.example/mcp", Required: true}}
			req.MCPHTTPServers = &servers
			method := "thread/start"
			if strings.HasPrefix(mode, "resume") {
				req.AgentSessionID = "fixture-native-thread"
				method = "thread/resume"
			}
			declarations, err := publicMCPHTTPServers(req)
			if err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "mcp-config.json")
			writeMCPHTTPConfigResponse(t, config, mcpHTTPConfigResponse(declarations))
			t.Setenv("PARSAR_PREPARATION_MCP_CONFIG", config)
			gate := filepath.Join(root, "required-initialization")
			t.Setenv("PARSAR_PREPARATION_THREAD_GATE", gate)
			p, err := newPreparation(t.Context(), req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			out := make(chan proto.Envelope, 16)
			s, err := p.start(t.Context(), "required-run", "actual prompt", out)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Cancel(context.Background())
			waitPreparationMethod(t, root, method)
			time.Sleep(100 * time.Millisecond)
			assertNoTurn := func() {
				t.Helper()
				threads := 0
				for _, frame := range preparationFrames(t, root) {
					if strings.HasPrefix(frame.Method, "turn/") {
						t.Fatal("Turn sent without initialized native thread", frame.Method)
					}
					if strings.HasPrefix(frame.Method, "thread/") {
						threads++
						if frame.Method != method || threads != 1 {
							t.Fatal("failed native initialization retried or replaced history")
						}
					}
				}
			}
			assertNoTurn()
			_, state, _ := strings.Cut(mode, " ")
			if err := os.WriteFile(gate, []byte(state), 0o600); err != nil {
				t.Fatal(err)
			}
			if state == "ready" {
				waitPreparationMethod(t, root, "turn/start")
				return
			}
			select {
			case <-s.waitDone:
			case <-time.After(4 * time.Second):
				t.Fatal("failed initialization did not terminate")
			}
			assertNoTurn()
			failed := false
			for len(out) > 0 {
				failed = (<-out).Type == proto.TypeError || failed
			}
			if !failed {
				t.Fatal("native initialization failure was not reported")
			}
		})
	}
}
