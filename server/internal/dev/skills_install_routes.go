package dev

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
)

const (
	skillsRegistryName    = "skills.sh"
	skillInstallAgentName = "claude-code"
	skillInstallTmpPrefix = "teamgent-skill-*"
)

type installSkillRequest struct {
	Source     string `json:"source"`
	Slug       string `json:"slug"`
	RegistryID string `json:"registry_id"`
	Registry   string `json:"registry"`
}

type skillInstallCommandRunner interface {
	Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error)
}

type defaultSkillInstallRunner struct{}

func (defaultSkillInstallRunner) Run(ctx context.Context, dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

type skillInstallHTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// installSkillFromRegistry downloads a Skills.sh skill into a temp directory,
// packages the downloaded Skill folder as a zip, stores that zip in the same
// blob backend used by manual Skill zip imports, and then calls the existing
// capability import path. It intentionally does not parse SKILL.md itself.
//
//	@Summary		Install a Skills.sh Skill as a capability
//	@Description	Downloads a Skills.sh Skill with npx skills add, stores the Skill folder as a zip, and reuses the existing Skill zip import flow. Owner/admin only.
//	@Tags			capabilities
//	@ID				installSkillsShSkill
//	@Accept			json
//	@Produce		json
//	@Param			workspaceID	path	string				true	"Workspace UUID"
//	@Param			body		body	installSkillRequest	true	"Skills.sh install payload"
//	@Success		201 {object} map[string]interface{} "Created capability, version, and secret ids"
//	@Failure		400 {object} map[string]string
//	@Failure		403 {object} map[string]string
//	@Failure		422 {object} map[string]string
//	@Failure		502 {object} map[string]string
//	@Failure		503 {object} map[string]string
//	@Router			/api/v1/workspaces/{workspaceID}/skills/install [post]
func installSkillFromRegistry(runtimeStore RuntimeStore, blobStore blob.Store, runner skillInstallCommandRunner, httpClient skillInstallHTTPDoer) http.HandlerFunc {
	if runner == nil {
		runner = defaultSkillInstallRunner{}
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		if blobStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
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

		tmpDir, err := os.MkdirTemp("", skillInstallTmpPrefix)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create temporary skill directory"})
			return
		}
		defer os.RemoveAll(tmpDir)

		skillDir, err := downloadSkillToTemp(r.Context(), runner, tmpDir, body.Source, body.Slug)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		zipBytes, err := zipSkillDirectory(skillDir)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
			return
		}
		ossKey, httpErr := storeSkillZipBytes(r.Context(), blobStore, httpClient, workspaceID, body.Slug+".zip", zipBytes)
		if httpErr != nil {
			writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
			return
		}

		// Preview-equivalent parse through the existing zip import code so the
		// capability name/description come from the uploaded zip, not the catalog.
		spec, _, httpErr := rebuildSkillSpecFromOSS(r.Context(), workspaceID, ossKey, blobStore)
		if httpErr != nil {
			writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
			return
		}

		sourcePayload, err := json.Marshal(map[string]any{
			"registry":    skillsRegistryName,
			"registry_id": strings.TrimSpace(body.RegistryID),
			"source":      strings.TrimSpace(body.Source),
			"slug":        strings.TrimSpace(body.Slug),
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not encode skill source payload"})
			return
		}

		commitBody := commitCapabilityImportBody{
			Kind:          string(canonical.KindSkill),
			Name:          skillImportName(spec, body),
			Description:   skillImportDescription(spec),
			Type:          string(canonical.KindSkill),
			SourcePayload: sourcePayload,
			CanonicalSpec: spec,
			OssKey:        ossKey,
			UploadSource:  string(canonical.UploadSourceZip),
		}
		relayCapabilityImportCommit(w, r, runtimeStore, blobStore, workspaceID, commitBody)
	}
}

func relayCapabilityImportCommit(w http.ResponseWriter, r *http.Request, runtimeStore RuntimeStore, blobStore blob.Store, workspaceID string, body commitCapabilityImportBody) {
	payload, err := json.Marshal(body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not encode skill import commit payload"})
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "/api/v1/workspaces/"+workspaceID+"/capabilities/import/commit", bytes.NewReader(payload))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not build skill import commit request"})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	recorder := newSkillInstallCommitRecorder()
	commitCapabilityImport(runtimeStore, blobStore).ServeHTTP(recorder, req)
	for key, values := range recorder.Header() {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(recorder.Body.Bytes())
}

type skillInstallCommitRecorder struct {
	header http.Header
	status int
	Body   bytes.Buffer
}

func newSkillInstallCommitRecorder() *skillInstallCommitRecorder {
	return &skillInstallCommitRecorder{header: http.Header{}}
}

func (r *skillInstallCommitRecorder) Header() http.Header {
	return r.header
}

func (r *skillInstallCommitRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *skillInstallCommitRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.Body.Write(p)
}

func validateInstallSkillRequest(body installSkillRequest) string {
	if strings.TrimSpace(body.Registry) != skillsRegistryName {
		return "registry must be skills.sh"
	}
	if !validSkillSourceRef(body.Source) {
		return "source must be an owner/repo GitHub reference"
	}
	if !validSkillSlug(body.Slug) {
		return "slug is required and may only contain letters, numbers, dot, underscore, and hyphen"
	}
	if strings.TrimSpace(body.RegistryID) == "" {
		return "registry_id is required"
	}
	return ""
}

func validSkillSourceRef(source string) bool {
	parts := strings.Split(strings.TrimSpace(strings.Trim(source, "/")), "/")
	return len(parts) == 2 && validSkillRefPart(parts[0]) && validSkillRefPart(parts[1])
}

func validSkillSlug(slug string) bool {
	return validSkillRefPart(slug)
}

func validSkillRefPart(part string) bool {
	if part == "" || part != strings.TrimSpace(part) || part == "." || part == ".." {
		return false
	}
	for _, r := range part {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func downloadSkillToTemp(ctx context.Context, runner skillInstallCommandRunner, tmpDir, source, slug string) (string, error) {
	output, err := runner.Run(ctx, tmpDir, "npx", "--yes", "skills", "add", strings.TrimSpace(source), "--skill", strings.TrimSpace(slug), "--agent", skillInstallAgentName, "--copy", "--yes")
	if err != nil {
		return "", fmt.Errorf("skills add failed: %s", compactCommandOutput(output, err))
	}
	skillDir, err := findDownloadedSkillDir(tmpDir, slug)
	if err != nil {
		if len(output) > 0 {
			return "", fmt.Errorf("%w; skills output: %s", err, compactCommandOutput(output, nil))
		}
		return "", err
	}
	return skillDir, nil
}

func findDownloadedSkillDir(tmpDir, slug string) (string, error) {
	primary := filepath.Join(tmpDir, ".claude", "skills", slug)
	if containsSkillMD(primary) {
		return primary, nil
	}
	var candidates []string
	if err := filepath.WalkDir(tmpDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), slug) && containsSkillMD(path) {
			candidates = append(candidates, path)
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("could not inspect downloaded skill directory: %w", err)
	}
	if len(candidates) > 0 {
		return preferClaudeSkillDir(candidates), nil
	}
	return "", fmt.Errorf("downloaded skill %q did not contain .claude/skills/%s/SKILL.md", slug, slug)
}

func containsSkillMD(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "SKILL.md") {
			return true
		}
	}
	return false
}

func preferClaudeSkillDir(candidates []string) string {
	for _, candidate := range candidates {
		clean := filepath.ToSlash(filepath.Clean(candidate))
		if strings.Contains(clean, "/.claude/skills/") {
			return candidate
		}
	}
	return candidates[0]
}

func zipSkillDirectory(skillDir string) ([]byte, error) {
	if !containsSkillMD(skillDir) {
		return nil, fmt.Errorf("skill directory %q is missing SKILL.md", skillDir)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err := filepath.WalkDir(skillDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(skillDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || rel == "" {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()
		entry, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = io.Copy(entry, file)
		return err
	})
	if closeErr := zw.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("could not package skill directory: %w", err)
	}
	return buf.Bytes(), nil
}

func storeSkillZipBytes(ctx context.Context, blobStore blob.Store, httpClient skillInstallHTTPDoer, workspaceID, filename string, data []byte) (string, *importHTTPError) {
	if blobStore == nil {
		return "", &importHTTPError{status: http.StatusServiceUnavailable, message: "object storage is not configured on this deployment"}
	}
	if int64(len(data)) > blob.MaxBlobBytes {
		return "", &importHTTPError{status: http.StatusRequestEntityTooLarge, message: "skill zip exceeds max blob size"}
	}
	ref, err := blobStore.NewRef("skill", workspaceID, filename)
	if err != nil {
		return "", &importHTTPError{status: http.StatusInternalServerError, message: "could not allocate storage reference"}
	}
	if putter, ok := blobStore.(interface {
		PutBytes(context.Context, string, string, []byte) error
	}); ok {
		if err := putter.PutBytes(ctx, ref, workspaceID, data); err != nil {
			return "", blobPutHTTPError(err)
		}
		return ref, nil
	}
	spec, err := blobStore.UploadURL(ctx, ref, workspaceID, 0)
	if err != nil {
		return "", &importHTTPError{status: http.StatusInternalServerError, message: "could not generate upload URL"}
	}
	method := strings.TrimSpace(spec.Method)
	if method == "" {
		method = http.MethodPut
	}
	req, err := http.NewRequestWithContext(ctx, method, spec.URL, bytes.NewReader(data))
	if err != nil {
		return "", &importHTTPError{status: http.StatusInternalServerError, message: "could not build upload request"}
	}
	if len(spec.Headers) == 0 {
		req.Header.Set("Content-Type", "application/octet-stream")
	} else {
		for key, value := range spec.Headers {
			req.Header.Set(key, value)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", &importHTTPError{status: http.StatusBadGateway, message: "could not upload skill zip to object storage: " + err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := fmt.Sprintf("object storage upload failed: HTTP %d", resp.StatusCode)
		if trimmed := strings.TrimSpace(string(detail)); trimmed != "" {
			msg += ": " + trimmed
		}
		return "", &importHTTPError{status: http.StatusBadGateway, message: msg}
	}
	return ref, nil
}

func blobPutHTTPError(err error) *importHTTPError {
	switch {
	case errors.Is(err, blob.ErrTooLarge):
		return &importHTTPError{status: http.StatusRequestEntityTooLarge, message: "skill zip exceeds max blob size"}
	case errors.Is(err, blob.ErrInvalidRef):
		return &importHTTPError{status: http.StatusInternalServerError, message: "invalid storage reference"}
	default:
		return &importHTTPError{status: http.StatusInternalServerError, message: "could not store skill zip"}
	}
}

func skillImportName(spec canonical.Spec, body installSkillRequest) string {
	if spec.Skill != nil {
		if title := strings.TrimSpace(spec.Skill.Title); title != "" {
			return title
		}
		if slug := strings.TrimSpace(spec.Skill.Slug); slug != "" {
			return slug
		}
	}
	if name := strings.TrimSpace(body.Slug); name != "" {
		return name
	}
	return strings.TrimSpace(body.RegistryID)
}

func skillImportDescription(spec canonical.Spec) string {
	if spec.Skill == nil {
		return ""
	}
	return strings.TrimSpace(spec.Skill.Description)
}

func compactCommandOutput(output []byte, err error) string {
	text := strings.TrimSpace(string(output))
	if text == "" && err != nil {
		text = err.Error()
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 600 {
		return text[:600] + "..."
	}
	return text
}
