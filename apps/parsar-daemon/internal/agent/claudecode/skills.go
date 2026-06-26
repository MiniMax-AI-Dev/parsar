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

	obslog "github.com/MiniMax-AI-Dev/parsar/internal/obs/log"
	"github.com/google/uuid"
)

// skillDescriptor is the daemon-side view of one server-sent skill entry
// under agent_options["skills"]. Wire-identical to pluginDescriptor.
type skillDescriptor struct {
	Name        string
	Version     string
	DownloadURL string
	SHA256      string
}

// SkillInstallResult carries warnings the session should surface. Unlike
// PluginInstallResult there is no Dirs list — skill targets are auto-
// scanned by Claude Code from <workDir>/.claude/skills/, no CLI flag.
type SkillInstallResult struct {
	Warnings []string
}

// installSkills materialises every skill under
// <workDir>/.claude/skills/<name>/. Pipeline mirrors installPlugins;
// only the target subdir differs (Claude Code auto-registers skills
// from that path).
func installSkills(
	ctx context.Context,
	logger *slog.Logger,
	workDir string,
	skills []skillDescriptor,
) (SkillInstallResult, error) {
	if logger == nil {
		logger = obslog.Bg()
	}
	if len(skills) == 0 {
		return SkillInstallResult{}, nil
	}
	if strings.TrimSpace(workDir) == "" {
		return SkillInstallResult{}, errors.New("claudecode skills: workDir is required")
	}

	root := filepath.Join(workDir, ".claude", "skills")
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SkillInstallResult{}, fmt.Errorf("claudecode skills: mkdir %s: %w", root, err)
	}

	result := SkillInstallResult{}
	for _, s := range skills {
		if err := s.validate(); err != nil {
			result.Warnings = append(result.Warnings, fmt.Sprintf("skip skill (invalid descriptor): %v", err))
			logger.Warn("claudecode skills: invalid descriptor", "err", err.Error())
			continue
		}

		dir := filepath.Join(root, s.Name)
		cacheKey := filepath.Join(dir, ".cache-key")
		expectedKey := s.cacheKey()

		if existing, err := os.ReadFile(cacheKey); err == nil && string(existing) == expectedKey {
			logger.Info("claudecode skills: cache hit",
				"name", s.Name, "version", s.Version, "dir", dir)
			continue
		}

		// Same timeout / cap as plugins — they share the install pipeline.
		perCtx, cancel := context.WithTimeout(ctx, pluginInstallTimeout)
		err := installOneSkill(perCtx, logger, root, dir, cacheKey, expectedKey, s)
		cancel()
		if err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("skill %s@%s: %v", s.Name, s.Version, err))
			logger.Warn("claudecode skills: install failed",
				"name", s.Name, "version", s.Version, "err", err.Error())
			continue
		}
		logger.Info("claudecode skills: installed",
			"name", s.Name, "version", s.Version, "dir", dir)
	}
	return result, nil
}

// installOneSkill: same shape as installOnePlugin, only target dir differs.
// Reuses fetchPluginZip / verifyPluginSHA256FromFD / extractPluginZipFromFD
// — the helpers are skill-agnostic and applying them to skill zips keeps
// the path-traversal / TOCTOU / SHA256 defences identical.
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

	zipPath := filepath.Join(tmpDir, fmt.Sprintf("%s-%s-%s.zip", s.Name, s.Version, uuid.NewString()))
	defer func() {
		_ = os.Remove(zipPath)
	}()

	fd, err := fetchPluginZip(ctx, s.DownloadURL, zipPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	if err := verifyPluginSHA256FromFD(fd, s.SHA256); err != nil {
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
		logger.Warn("claudecode skills: write cache key failed",
			"path", cacheKey, "err", err.Error())
	}
	return nil
}

// decodeSkillDescriptors converts agent_options["skills"] into typed
// descriptors. Mirrors decodePluginDescriptors.
func decodeSkillDescriptors(raw any) ([]skillDescriptor, []string) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, []string{fmt.Sprintf("agent_options[skills] must be array, got %T", raw)}
	}
	out := make([]skillDescriptor, 0, len(items))
	warnings := make([]string, 0)
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("skills[%d]: not an object", i))
			continue
		}
		s := skillDescriptor{
			Name:        stringField(obj, "name"),
			Version:     stringField(obj, "version"),
			DownloadURL: stringField(obj, "download_url"),
			SHA256:      stringField(obj, "sha256"),
		}
		if err := s.validate(); err != nil {
			warnings = append(warnings, fmt.Sprintf("skills[%d] (%s): %v", i, s.Name, err))
			continue
		}
		out = append(out, s)
	}
	return out, warnings
}

func (s skillDescriptor) validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("name is required")
	}
	if strings.ContainsAny(s.Name, "/\\") || s.Name == "." || s.Name == ".." {
		return fmt.Errorf("name %q contains path separator or dot-ref", s.Name)
	}
	if strings.TrimSpace(s.DownloadURL) == "" {
		return errors.New("download_url is required")
	}
	if len(s.SHA256) != 64 {
		return fmt.Errorf("sha256 must be 64 hex chars (got %d)", len(s.SHA256))
	}
	return nil
}

func (s skillDescriptor) cacheKey() string {
	return fmt.Sprintf("%s@%s", strings.TrimSpace(s.Name), strings.ToLower(s.SHA256))
}
