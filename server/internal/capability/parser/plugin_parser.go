package parser

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
)

// PluginSource carries provenance the import handler knows but the validator
// doesn't. Filled in by the handler before calling ParsePlugin.
type PluginSource struct {
	OssKey       string
	UploadSource canonical.UploadSource
	GitHubRepo   string
	GitHubRef    string
	GitHubPath   string
}

// ErrPluginValidationFailed wraps the result of ValidatePluginZip when
// res.Valid is false. The wrapping message stitches in the first few
// validator errors; callers wanting the full list should propagate the
// PluginValidationResult.
var ErrPluginValidationFailed = errors.New("parser: plugin validation failed")

// ParsePlugin validates the zip, computes its SHA-256 (the source of truth
// for daemon-side integrity), and produces a populated canonical.Spec. Pure:
// no I/O — the caller fetches zipBytes.
func ParsePlugin(zipBytes []byte, source PluginSource) (canonical.Spec, *PluginValidationResult, error) {
	res, err := ValidatePluginZip(zipBytes)
	if err != nil {
		return canonical.Spec{}, res, err
	}
	if !res.Valid {
		return canonical.Spec{}, res, fmt.Errorf("%w: %s", ErrPluginValidationFailed, joinNonEmpty(res.Errors, "; "))
	}

	if err := source.validate(); err != nil {
		return canonical.Spec{}, res, err
	}

	digest := sha256.Sum256(zipBytes)
	hexDigest := hex.EncodeToString(digest[:])

	manifest := res.Manifest

	// canonical.PluginSpec requires a non-empty Version; Claude Code itself
	// tolerates an absent version. Default to "0.0.0" with a warning.
	version := manifest.Version
	if version == "" {
		version = "0.0.0"
		res.Warnings = append(res.Warnings, `plugin.json omits "version"; defaulted to "0.0.0"`)
	}

	spec := canonical.Spec{
		SchemaVersion: canonical.SchemaVersionCurrent,
		Kind:          canonical.KindPlugin,
		Plugin: &canonical.PluginSpec{
			Name:         manifest.Name,
			DisplayName:  manifest.DisplayName,
			Version:      version,
			Description:  manifest.Description,
			Author:       manifest.Author.Name,
			Keywords:     manifest.Keywords,
			OssKey:       source.OssKey,
			SHA256:       hexDigest,
			UploadSource: source.UploadSource,
			GitHubRepo:   source.GitHubRepo,
			GitHubRef:    source.GitHubRef,
			GitHubPath:   source.GitHubPath,
		},
	}

	if err := spec.Validate(); err != nil {
		return canonical.Spec{}, res, fmt.Errorf("parser: canonical Spec validate failed unexpectedly: %w", err)
	}
	return spec, res, nil
}

func (s PluginSource) validate() error {
	if s.OssKey == "" {
		return errors.New("parser: PluginSource.OssKey is required")
	}
	switch s.UploadSource {
	case canonical.UploadSourceZip, canonical.UploadSourceGitHub:
		return nil
	case "":
		return errors.New("parser: PluginSource.UploadSource is required")
	default:
		return fmt.Errorf("parser: unknown PluginSource.UploadSource %q", s.UploadSource)
	}
}

func joinNonEmpty(parts []string, sep string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += sep + p
	}
	return out
}
