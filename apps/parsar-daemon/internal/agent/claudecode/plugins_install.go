package claudecode

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/apps/parsar-daemon/internal/agent/installroot"
	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/google/uuid"
)

// installPlugins materialises every plugin under
// <workDir>/.claude/plugins/<name>/ and returns the local paths. Per
// plugin:
//
//  1. Skip when <dir>/.cache-key matches name+sha256 (recurring prompts
//     avoid the network round-trip).
//  2. Fetch the download URL into a temp file under .tmp/, capping at
//     maxPluginZipBytes.
//  3. Verify SHA-256 against the descriptor before touching the
//     extraction target — mismatch demotes to warning.
//  4. Extract to <workDir>/.claude/plugins/<name>/, stripping a single
//     wrapping directory and ignoring __MACOSX/.
//  5. Stamp .cache-key with "<name>@<sha256>".
//
// Errors during 2-4 demote the plugin to a warning and continue.
// Returning a hard error means we couldn't even create the parent
// directory.
func installPlugins(
	ctx context.Context,
	logger *slog.Logger,
	workDir string,
	plugins []pluginDescriptor,
) (PluginInstallResult, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(plugins) == 0 {
		return PluginInstallResult{}, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return PluginInstallResult{}, errors.New("claudecode plugins: workDir is required")
	}

	root := filepath.Join(workDir, ".claude", "plugins")
	unlock, err := installroot.Lock(ctx, root)
	if err != nil {
		return PluginInstallResult{}, err
	}
	defer unlock()

	result := PluginInstallResult{}
	for _, p := range plugins {
		if err := p.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip plugin (invalid descriptor): %v", err))
			logger.Warn("claudecode plugins: invalid descriptor", "err", err.Error())
			continue
		}

		dir := filepath.Join(root, p.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := p.cacheKey()

		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("claudecode plugins: cache hit",
				"name", p.Name, "version", p.Version, "dir", dir)
			result.PluginDirs = append(result.PluginDirs, dir)
			continue
		}

		perCtx, cancel := context.WithTimeout(ctx, pluginInstallTimeout)
		err := installOnePlugin(perCtx, logger, root, dir, cacheKey, expectedKey, p)
		cancel()
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("plugin %s@%s: %v", p.Name, p.Version, err))
			logger.Warn("claudecode plugins: install failed",
				"name", p.Name, "version", p.Version, "err", err.Error())
			continue
		}
		result.PluginDirs = append(result.PluginDirs, dir)
		logger.Info("claudecode plugins: installed",
			"name", p.Name, "version", p.Version, "dir", dir)
	}
	return result, nil
}

// installOnePlugin: download → verify → extract → stamp cache key.
// On error, best-effort cleanup of any partial extraction.
func installOnePlugin(
	ctx context.Context,
	logger *slog.Logger,
	root, dir, cacheKey, expectedKey string,
	p pluginDescriptor,
) error {
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}

	// Per-call uuid in the temp path so two concurrent installs of the
	// same (name, version) don't truncate each other's bytes, and so
	// nothing on disk between verifyPluginSHA256 and extract can be a
	// different file than the one we just hashed (TOCTOU).
	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", p.Name, p.Version, uuid.NewString()))
	defer func() {
		_ = os.Remove(zipPath)
	}()

	fd, err := fetchPluginZip(ctx, p.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	// Verify and extract BOTH read through the same FD (not the path).
	// Unix file semantics pin the inode, so a swap on disk between
	// hashing and extraction cannot change the bytes we're using.
	if err := verifyPluginSHA256FromFD(fd, p.SHA256); err != nil {
		return err
	}
	if _, err := fd.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek: %w", err)
	}
	fi, err := fd.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("rm old dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	if err := extractPluginZipFromFD(fd, fi.Size(), dir); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	if err := os.WriteFile(cacheKey, []byte(expectedKey), 0o644); err != nil {
		// Cache miss next time is recoverable — don't fail the install.
		logger.Warn("claudecode plugins: write cache key failed",
			"path", cacheKey, "err", err.Error())
	}
	return nil
}
