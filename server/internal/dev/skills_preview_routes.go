package dev

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/paths"
)

// previewSkillFromRegistry downloads into disposable storage without creating
// a capability, installation identity, or persistent upload.
//
//	@Summary	Preview a Skills.sh Skill before installation
//	@Description	Downloads and parses the current repository content with the existing Skills installer and ZIP parser. Owner/admin only. Does not install a capability.
//	@Tags		capabilities
//	@ID			previewSkillsShSkill
//	@Accept		json
//	@Produce	json
//	@Param		workspaceID	path	string	true	"Workspace UUID"
//	@Param		body	body	installSkillRequest	true	"Skills.sh source"
//	@Success	200	{object}	map[string]interface{}	"canonical_spec, warnings, suggested_name"
//	@Failure	400	{object}	map[string]string
//	@Failure	401	{object}	map[string]string
//	@Failure	403	{object}	map[string]string
//	@Failure	404	{object}	map[string]string
//	@Failure	422	{object}	map[string]string
//	@Failure	500	{object}	map[string]string
//	@Failure	502	{object}	map[string]string
//	@Failure	503	{object}	map[string]string
//	@Router		/api/v1/workspaces/{workspaceID}/skills/preview [post]
func previewSkillFromRegistry(runtimeStore RuntimeStore, runner skillInstallCommandRunner) http.HandlerFunc {
	if runner == nil {
		runner = defaultSkillInstallRunner{}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore); !ok {
			return
		}
		var body installSkillRequest
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if msg := validateInstallSkillRequest(body); msg != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": msg})
			return
		}
		root, err := paths.Root()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not resolve skill preview directory"})
			return
		}
		previewDir := filepath.Join(root, "tmp", "skill-previews")
		if err := os.MkdirAll(previewDir, 0o700); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create skill preview directory"})
			return
		}
		tmpDir, err := os.MkdirTemp(previewDir, "preview-*")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create temporary skill directory"})
			return
		}
		defer os.RemoveAll(tmpDir)
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		skillDir, err := downloadSkillToTemp(ctx, runner, tmpDir, body.Source, body.Slug)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		zipBytes, err := zipSkillDirectory(skillDir)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		res, err := parser.ParseSkillZip(zipBytes)
		if err != nil {
			writeImportParseError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, previewCapabilityImportResponse{
			CanonicalSpec: res.Spec,
			Warnings:      ensureStringSlice(res.Warnings),
			SuggestedName: res.SuggestedName,
		})
	}
}
