package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/codex"
	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/paths"
)

const codexInstallScriptURL = "https://chatgpt.com/codex/install.sh"

func EnsureCodex(ctx context.Context) (string, string, error) {
	root, err := paths.Root()
	if err != nil {
		return "", "", err
	}
	toolRoot := filepath.Join(root, "tools", "codex")
	binDir := filepath.Join(toolRoot, "bin")
	binary := filepath.Join(binDir, "codex")
	if version, err := codex.CheckCLIAvailable(ctx, binary); err == nil {
		return binary, version, nil
	}
	lockDir := filepath.Join(root, "tools", ".locks", "codex")
	if err := acquireDirLock(ctx, lockDir); err != nil {
		return "", "", err
	}
	defer os.Remove(lockDir)
	if version, err := codex.CheckCLIAvailable(ctx, binary); err == nil {
		return binary, version, nil
	}
	if err := os.MkdirAll(toolRoot, 0o700); err != nil {
		return "", "", fmt.Errorf("create codex tool dir: %w", err)
	}
	installer, err := os.CreateTemp(toolRoot, "codex-install-*.sh")
	if err != nil {
		return "", "", fmt.Errorf("create codex installer file: %w", err)
	}
	installerPath := installer.Name()
	if err := installer.Close(); err != nil {
		_ = os.Remove(installerPath)
		return "", "", fmt.Errorf("close codex installer file: %w", err)
	}
	defer os.Remove(installerPath)
	download := exec.CommandContext(ctx, "curl", "-fsSL", codexInstallScriptURL, "-o", installerPath)
	if output, err := download.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("download codex installer: %w: %s", err, output)
	}
	cmd := exec.CommandContext(ctx, "sh", installerPath)
	cmd.Env = append(os.Environ(),
		"CODEX_INSTALL_DIR="+binDir,
		"CODEX_HOME="+filepath.Join(toolRoot, "home"),
		"CODEX_NON_INTERACTIVE=true",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.Remove(binary)
		return "", "", fmt.Errorf("run codex installer: %w: %s", err, output)
	}
	version, err := codex.CheckCLIAvailable(ctx, binary)
	if err != nil {
		_ = os.Remove(binary)
		return "", "", fmt.Errorf("validate installed codex: %w", err)
	}
	return binary, version, nil
}

func acquireDirLock(ctx context.Context, lockDir string) error {
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o700); err != nil {
		return err
	}
	for {
		if err := os.Mkdir(lockDir, 0o700); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
