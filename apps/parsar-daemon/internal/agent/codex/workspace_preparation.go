package codex

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
	"github.com/MiniMax-AI-Dev/parsar/internal/agentdaemon/proto"
)

// SupportsWorkspaceReadPreparation checks local prerequisites, not public admission.
// Native connection and the installed executor helper are verified per operation.
func SupportsWorkspaceReadPreparation() bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return false
	}
	binary, helper := os.Getenv("PARSAR_CODEX_HARNESS_BIN"), os.Getenv("PARSAR_CODEX_DIRECTORY_HELPER")
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || !filepath.IsAbs(helper) || filepath.Clean(helper) != helper {
		return false
	}
	info, err := os.Stat(binary)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0
}

func workspaceReadPlan(req proto.PromptRequestPayload) (SessionPlan, error) {
	root, err := paths.Root()
	if err != nil {
		return SessionPlan{}, err
	}
	base := filepath.Join(root, "parsar-daemon", "workspace-read")
	if err := os.MkdirAll(base, 0o700); err != nil {
		return SessionPlan{}, err
	}
	state, err := os.MkdirTemp(base, "read-")
	if err != nil {
		return SessionPlan{}, err
	}
	plan := SessionPlan{Cwd: state, Env: []string{"CODEX_HOME=" + state, "DISABLE_TELEMETRY=1"}, Cleanup: func() { _ = os.RemoveAll(state) }}
	configureRemoteEnvironment(&plan, *req.RemoteEnvironment)
	return plan, nil
}

// A failed Close retains state until the same child exits. A successful Close
// performs cleanup synchronously, including another caller's ongoing cleanup.
func readPreparationCleanup(rpc *JSONRPCClient, cleanup func()) func() {
	return func() {
		rpc.mu.Lock()
		cmd := rpc.cmd
		rpc.mu.Unlock()
		if cmd == nil || cmd.Process == nil {
			cleanup()
			return
		}
		select {
		case <-rpc.Done():
			cleanup()
		default:
			go func() { <-rpc.Done(); cleanup() }()
		}
	}
}
