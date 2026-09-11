package execution

import (
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/services/agents-api/internal/store"
)

func resolveExecutionEnvironment(snapshot Snapshot) (string, bool, error) {
	if snapshot.Environment != nil {
		if snapshot.Environment.Type != "none" || snapshot.Daemon != nil {
			return "", false, store.ErrInvalidInput
		}
		return "", true, nil
	}
	if snapshot.Daemon == nil {
		return "", false, store.ErrInvalidInput
	}
	workDir := snapshot.Daemon.WorkDir
	if workDir != "" && !filepath.IsAbs(workDir) && !strings.HasPrefix(workDir, "~/") {
		return "", false, store.ErrInvalidInput
	}
	return workDir, false, nil
}
