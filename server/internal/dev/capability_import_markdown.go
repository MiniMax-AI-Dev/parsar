package dev

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// Reject invalid version destinations before allocating an archive.
func validateMarkdownSkillVersionDestination(ctx context.Context, runtimeStore RuntimeStore, workspaceID, capabilityID string) error {
	existing, err := runtimeStore.GetCapability(ctx, capabilityID)
	if err != nil {
		return err
	}
	if existing.WorkspaceID != workspaceID {
		return store.ErrUnknownCapability
	}
	if existing.Type != string(canonical.KindSkill) {
		return store.ErrCapabilityKindMismatch
	}
	return nil
}

// Markdown imports use the same stored ZIP contract as uploaded Skills.
// Existing archives and non-Skill imports pass through unchanged.
func ensureSkillImportArchive(ctx context.Context, workspaceID string, spec canonical.Spec, ref, digest string, blobs blob.Store) (string, string, *importHTTPError) {
	if spec.Kind != canonical.KindSkill || ref != "" {
		return ref, digest, nil
	}
	if err := spec.Validate(); err != nil {
		return "", "", &importHTTPError{status: http.StatusUnprocessableEntity, message: err.Error()}
	}
	if len(spec.Skill.Files) != 0 {
		return "", "", &importHTTPError{status: http.StatusUnprocessableEntity, message: "multi-file Skills must be imported as a ZIP"}
	}
	frontmatter, err := yaml.Marshal(struct {
		Name        string `yaml:"name"`
		Slug        string `yaml:"slug"`
		Title       string `yaml:"title,omitempty"`
		Description string `yaml:"description,omitempty"`
		Trigger     string `yaml:"trigger,omitempty"`
	}{spec.Skill.Slug, spec.Skill.Slug, spec.Skill.Title, spec.Skill.Description, spec.Skill.Trigger})
	if err != nil {
		return "", "", &importHTTPError{status: http.StatusInternalServerError, message: "could not encode Skill metadata"}
	}
	return storeSkillMarkdownArchive(ctx, workspaceID, "---\n"+string(frontmatter)+"---\n"+spec.Skill.Instruction+"\n", blobs)
}

// Package original Markdown when callers already have the complete Skill source.
func storeSkillMarkdownArchive(ctx context.Context, workspaceID, markdown string, blobs blob.Store) (string, string, *importHTTPError) {
	var archive bytes.Buffer
	w := zip.NewWriter(&archive)
	entry, err := w.Create("SKILL.md")
	if err == nil {
		_, err = io.WriteString(entry, markdown)
	}
	if closeErr := w.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", &importHTTPError{status: http.StatusInternalServerError, message: "could not package Skill Markdown"}
	}
	data := archive.Bytes()
	if _, err := parser.ParseSkillZip(data); err != nil {
		return "", "", &importHTTPError{status: http.StatusUnprocessableEntity, message: err.Error()}
	}
	ref, httpErr := storeSkillZipBytes(ctx, blobs, http.DefaultClient, workspaceID, "skill.zip", data)
	if httpErr != nil {
		return "", "", httpErr
	}
	sum := sha256.Sum256(data)
	return ref, hex.EncodeToString(sum[:]), nil
}
