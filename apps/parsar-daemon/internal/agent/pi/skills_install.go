package pi

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

// installSkills materialises every skill under <root>/<name>/ and returns
// the local paths. Per skill:
//
//  1. Cache hit (<dir>/.cache-key == name@sha256) returns the dir without
//     a network round-trip — but still returns it, so --skill is injected
//     on every turn.
//  2. Fetch → verify SHA-256 → extract (single wrapping dir stripped,
//     __MACOSX/ ignored) → stamp .cache-key.
//
// Errors during fetch/verify/extract demote one skill to a warning and
// continue. A hard error means the root dir itself was uncreatable.
func installSkills(
	ctx context.Context,
	logger *slog.Logger,
	root string,
	skills []skillDescriptor,
) (SkillInstallResult, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(skills) == 0 {
		return SkillInstallResult{}, nil
	}
	if strings.TrimSpace(root) == "" {
		return SkillInstallResult{}, errors.New("pi skills: root is required")
	}
	unlock, err := installroot.Lock(ctx, root)
	if err != nil {
		return SkillInstallResult{}, err
	}
	defer unlock()

	result := SkillInstallResult{}
	for _, s := range skills {
		if err := s.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip skill (invalid descriptor): %v", err))
			logger.Warn("pi skills: invalid descriptor", "err", err.Error())
			continue
		}

		dir := filepath.Join(root, s.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := s.cacheKey()

		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("pi skills: cache hit", "name", s.Name, "version", s.Version, "dir", dir)
			result.SkillDirs = append(result.SkillDirs, dir)
			continue
		}

		perCtx, cancel := context.WithTimeout(ctx, skillInstallTimeout)
		err := installOneSkill(perCtx, logger, root, dir, cacheKey, expectedKey, s)
		cancel()
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skill %s@%s: %v", s.Name, s.Version, err))
			logger.Warn("pi skills: install failed", "name", s.Name, "version", s.Version, "err", err.Error())
			continue
		}
		result.SkillDirs = append(result.SkillDirs, dir)
		logger.Info("pi skills: installed", "name", s.Name, "version", s.Version, "dir", dir)
	}
	return result, nil
}

func installOneSkill(
	ctx context.Context,
	logger *slog.Logger,
	root, dir, cacheKey, expectedKey string,
	s skillDescriptor,
) error {
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}

	// Per-call uuid so concurrent installs of the same (name, version)
	// don't truncate each other's bytes, and nothing on disk between
	// verify and extract can be a different file than the one hashed.
	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", s.Name, s.Version, uuid.NewString()))
	defer func() { _ = os.Remove(zipPath) }()

	fd, err := fetchSkillZip(ctx, s.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	// Verify and extract BOTH read through the same FD (not the path):
	// Unix file semantics pin the inode, so a swap on disk between
	// hashing and extraction cannot change the bytes we use.
	if err := verifySHA256FromFD(fd, s.SHA256); err != nil {
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
	if err := extractSkillZipFromFD(fd, fi.Size(), dir); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}

	if err := os.WriteFile(cacheKey, []byte(expectedKey), 0o644); err != nil {
		logger.Warn("pi skills: write cache key failed", "path", cacheKey, "err", err.Error())
	}
	return nil
}
