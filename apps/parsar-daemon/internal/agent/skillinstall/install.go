// Package skillinstall securely materialises server-resolved Skills for daemon-side engines.
package skillinstall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/google/uuid"
)

// Result reports the installed Skill directories and per-Skill warnings.
type Result struct {
	SkillDirs []string
	Warnings  []string
}

const skillInstallTimeout = 60 * time.Second

// Install materialises every descriptor beneath root.
func Install(ctx context.Context, logger *slog.Logger, root string, skills []Descriptor) (Result, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(skills) == 0 {
		return Result{}, nil
	}
	if strings.TrimSpace(root) == "" {
		return Result{}, errors.New("skill install: root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Result{}, fmt.Errorf("skill install: mkdir %s: %w", root, err)
	}
	result := Result{}
	for _, s := range skills {
		if err := s.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip skill (invalid descriptor): %v", err))
			logger.Warn("skill install: invalid descriptor", "err", err.Error())
			continue
		}
		dir := filepath.Join(root, s.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := s.cacheKey()
		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("skill install: cache hit", "name", s.Name, "version", s.Version, "dir", dir)
			result.SkillDirs = append(result.SkillDirs, dir)
			continue
		}
		var err error
		if s.isInline() {
			err = installInlineSkill(dir, cacheKey, expectedKey, s.Content)
		} else {
			perCtx, cancel := context.WithTimeout(ctx, skillInstallTimeout)
			err = installOneSkill(perCtx, logger, root, dir, cacheKey, expectedKey, s)
			cancel()
		}
		if err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skill %s@%s: %v", s.Name, s.Version, err))
			logger.Warn("skill install: install failed", "name", s.Name, "version", s.Version, "err", err.Error())
			continue
		}
		result.SkillDirs = append(result.SkillDirs, dir)
		logger.Info("skill install: installed", "name", s.Name, "version", s.Version, "dir", dir)
	}
	return result, nil
}

func installInlineSkill(dir, cacheKey, expectedKey, content string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("rm old dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("write SKILL.md: %w", err)
	}
	if err := os.WriteFile(cacheKey, []byte(expectedKey), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("write cache key: %w", err)
	}
	return nil
}

// Prune removes installer-owned directories absent from skills.
func Prune(root string, skills []Descriptor) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("skill install: read root %s: %w", root, err)
	}
	keep := make(map[string]bool, len(skills))
	for _, skill := range skills {
		if err := skill.validate(); err != nil {
			return fmt.Errorf("skill install: prune descriptor: %w", err)
		}
		keep[skill.Name] = true
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || keep[name] {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, name, ".cache-key")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("skill install: inspect %s: %w", name, err)
		}
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			return fmt.Errorf("skill install: prune %s: %w", name, err)
		}
	}
	return nil
}

func installOneSkill(ctx context.Context, logger *slog.Logger, root, dir, cacheKey, expectedKey string, s Descriptor) error {
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return fmt.Errorf("mkdir tmp: %w", err)
	}
	// A unique path prevents concurrent installs from truncating verified bytes.
	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", s.Name, s.Version, uuid.NewString()))
	defer func() { _ = os.Remove(zipPath) }()
	fd, err := fetchSkillZip(ctx, s.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()
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
		logger.Warn("skill install: write cache key failed", "path", cacheKey, "err", err.Error())
	}
	return nil
}
