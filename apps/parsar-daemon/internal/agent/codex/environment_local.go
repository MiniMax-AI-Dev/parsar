package codex

import (
	"os"
	"runtime"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/localworkspace"
)

// SupportsLocalEnvironment checks deployment prerequisites, not public admission.
func SupportsLocalEnvironment(version string) bool {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || !SupportsRemoteEnvironment(version) || os.Getenv("PARSAR_CODEX_PERMISSION_PROFILE") == "" || os.Getenv("PARSAR_CODEX_HARNESS_BIN") != "" {
		return false
	}
	binding, err := localworkspace.Load()
	return err == nil && binding != nil
}
