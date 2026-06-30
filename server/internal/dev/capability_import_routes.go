package dev

// AES-GCM encryption stays in the handler so the store stays oblivious to
// the master key, mirroring the runtime_credential.go pattern.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/canonical"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/capability/parser"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/secrets"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/storage/blob"
	"github.com/MiniMax-AI-Dev/parsar/server/internal/store"
)

// previewCapabilityImportBody is the wire shape for POST .../import/preview.
//
// SourceFormat values map to parser.SourceFormat ("json", "toml", "markdown").
// For plugins the zip is already in OSS (PUT via presign-upload), so callers
// pass OssKey instead of RawText.
type previewCapabilityImportBody struct {
	Kind         string `json:"kind"`          // "mcp" | "skill" | "plugin"
	RawText      string `json:"raw_text"`      // user paste (mcp / skill)
	SourceFormat string `json:"source_format"` // matches parser.SourceFormat (mcp / skill)

	// Plugin-only fields. UploadSource discriminates "zip" (OssKey
	// carries the body) vs "github" (server fetches the tarball).
	OssKey       string `json:"oss_key,omitempty"`
	UploadSource string `json:"upload_source,omitempty"`
	GitHubRepo   string `json:"github_repo,omitempty"`
	GitHubRef    string `json:"github_ref,omitempty"`
	GitHubPath   string `json:"github_path,omitempty"`
}

type previewCapabilityImportResponse struct {
	CanonicalSpec canonical.Spec `json:"canonical_spec"`
	Warnings      []string       `json:"warnings"`
	SuggestedName string         `json:"suggested_name"`

	// PluginValidation carries structured errors/warnings from
	// parser.ValidatePluginZip. Empty for mcp/skill previews.
	PluginValidation *parser.PluginValidationResult `json:"plugin_validation,omitempty"`
}

// commitInlineSecretBody is one cleartext-bearing entry from the commit body.
// Plaintext is encrypted server-side before the secret hits the DB.
type commitInlineSecretBody struct {
	ServerName string `json:"server_name"`
	EnvKey     string `json:"env_key"`
	Plaintext  string `json:"plaintext"`
}

// commitCapabilityImportBody is the wire shape for POST .../import/commit.
//
// CanonicalSpec is the user-edited spec from preview. inline_secret entries
// may have empty SecretID (server fills them); credential_ref entries must
// already have a valid credential_kind_code.
type commitCapabilityImportBody struct {
	Kind          string                   `json:"kind"`        // "mcp" | "skill" | "plugin"
	Name          string                   `json:"name"`        // capability display name
	Description   string                   `json:"description"` // optional
	Visibility    string                   `json:"visibility"`  // "private" | "public" | …
	Version       string                   `json:"version"`     // capability_version.version; "1.0.0" default
	Type          string                   `json:"type"`        // capability.type column ("mcp" | "skill" | "plugin")
	SourcePayload json.RawMessage          `json:"source_payload,omitempty"`
	CanonicalSpec canonical.Spec           `json:"canonical_spec"`
	InlineSecrets []commitInlineSecretBody `json:"inline_secrets,omitempty"`

	// Plugin-only fields. For kind="plugin" the commit handler ignores
	// CanonicalSpec and rebuilds the spec from these fields + OSS bytes
	// (the on-disk zip is the only authoritative source for name /
	// sha256 / version).
	OssKey       string `json:"oss_key,omitempty"`
	UploadSource string `json:"upload_source,omitempty"`
	GitHubRepo   string `json:"github_repo,omitempty"`
	GitHubRef    string `json:"github_ref,omitempty"`
	GitHubPath   string `json:"github_path,omitempty"`
}

type commitCapabilityImportResponse struct {
	Capability        store.CapabilityRead        `json:"capability"`
	CapabilityVersion store.CapabilityVersionRead `json:"capability_version"`
	CreatedSecretIDs  []string                    `json:"created_secret_ids"`
}

// previewCapabilityImport handles POST .../capabilities/import/preview.
// Pure parse for mcp/skill (no DB writes). The plugin branch downloads
// the just-uploaded zip from OSS so ValidatePluginZip can run against
// the actual bytes.
func previewCapabilityImport(runtimeStore RuntimeStore, blobStore blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		var body previewCapabilityImportBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		kind := strings.ToLower(strings.TrimSpace(body.Kind))
		if kind == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "kind is required (mcp|skill|plugin)"})
			return
		}
		switch kind {
		case "mcp":
			previewMCPOrSkillImport(w, body, parseAsMCP)
		case "skill":
			previewSkillImport(r.Context(), w, workspaceID, body, blobStore)
		case "plugin":
			previewPluginImport(r.Context(), w, workspaceID, body, blobStore)
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown kind %q (want mcp|skill|plugin)", kind)})
		}
	}
}

// previewSkillImport routes Skill preview by source_format:
// "markdown" runs parser.ParseSkill on RawText; "zip" re-downloads
// the OSS upload and runs parser.ParseSkillZip. The zip branch
// enforces workspace ownership on the OSS key (403 on cross-tenant).
// Empty/unknown source_format defaults to markdown for back-compat.
func previewSkillImport(ctx context.Context, w http.ResponseWriter, workspaceID string, body previewCapabilityImportBody, blobStore blob.Store) {
	format := parser.SourceFormat(strings.ToLower(strings.TrimSpace(body.SourceFormat)))
	switch format {
	case parser.SourceFormatZip:
		previewSkillZipImport(ctx, w, workspaceID, body, blobStore)
		return
	case "", parser.SourceFormatMarkdown:
		previewMCPOrSkillImport(w, body, parseAsSkill)
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("unsupported source_format %q for kind=skill (want markdown|zip)", body.SourceFormat),
		})
	}
}

func previewSkillZipImport(ctx context.Context, w http.ResponseWriter, workspaceID string, body previewCapabilityImportBody, blobStore blob.Store) {
	if blobStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
		return
	}
	ossKey := strings.TrimSpace(body.OssKey)
	if ossKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "oss_key is required for skill zip preview (call /uploads/presign-upload first)"})
		return
	}
	// Cross-tenant check: RBAC proved caller is admin in workspaceID
	// but not that oss_key was minted under it; without this gate
	// preview would happily download another tenant's zip.
	owned, err := blobStore.BelongsToWorkspace(ctx, ossKey, workspaceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not verify storage reference"})
		return
	}
	if !owned {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "oss key does not belong to this workspace"})
		return
	}
	zipBytes, err := blobStore.Download(ctx, ossKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not fetch uploaded zip from object storage: " + err.Error()})
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

type previewParseResult struct {
	Spec          canonical.Spec
	Warnings      []string
	SuggestedName string
}

// previewParser is the shared shape parseAsMCP / parseAsSkill conform
// to so previewMCPOrSkillImport can route either through one helper.
// Zip-shaped parsers have their own dedicated handlers.
type previewParser func(raw string, format parser.SourceFormat) (*previewParseResult, error)

func parseAsMCP(raw string, format parser.SourceFormat) (*previewParseResult, error) {
	res, err := parser.ParseMCP(raw, format)
	if err != nil {
		return nil, err
	}
	return &previewParseResult{Spec: res.Spec, Warnings: res.Warnings, SuggestedName: res.SuggestedName}, nil
}

func parseAsSkill(raw string, format parser.SourceFormat) (*previewParseResult, error) {
	res, err := parser.ParseSkill(raw, format)
	if err != nil {
		return nil, err
	}
	return &previewParseResult{Spec: res.Spec, Warnings: res.Warnings, SuggestedName: res.SuggestedName}, nil
}

func previewMCPOrSkillImport(w http.ResponseWriter, body previewCapabilityImportBody, fn previewParser) {
	raw := body.RawText
	format := parser.SourceFormat(strings.ToLower(strings.TrimSpace(body.SourceFormat)))
	if strings.TrimSpace(raw) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "raw_text is required"})
		return
	}
	res, err := fn(raw, format)
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

// previewPluginImport runs ValidatePluginZip against the OSS-hosted zip
// for kind=plugin previews. Hard validation failures still return 200
// with valid=false in plugin_validation so the UI can render each error
// inline; Commit button stays disabled. Workspace ownership on the OSS
// key is enforced (403) to prevent cross-tenant read.
func previewPluginImport(ctx context.Context, w http.ResponseWriter, workspaceID string, body previewCapabilityImportBody, blobStore blob.Store) {
	if blobStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "object storage is not configured on this deployment", "code": "OSS_NOT_CONFIGURED"})
		return
	}
	uploadSource := canonical.UploadSource(strings.ToLower(strings.TrimSpace(body.UploadSource)))
	switch uploadSource {
	case canonical.UploadSourceZip:
		// fall through
	case canonical.UploadSourceGitHub:
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "github plugin sync is not implemented yet; upload a zip instead"})
		return
	case "":
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_source is required for plugin kind (\"zip\" | \"github\")"})
		return
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown upload_source %q", body.UploadSource)})
		return
	}

	ossKey := strings.TrimSpace(body.OssKey)
	if ossKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "oss_key is required for plugin upload (call /uploads/presign-upload first)"})
		return
	}
	owned, err := blobStore.BelongsToWorkspace(ctx, ossKey, workspaceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not verify storage reference"})
		return
	}
	if !owned {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "oss key does not belong to this workspace"})
		return
	}

	zipBytes, err := blobStore.Download(ctx, ossKey)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "could not fetch uploaded zip from object storage: " + err.Error()})
		return
	}

	spec, validation, err := parser.ParsePlugin(zipBytes, parser.PluginSource{
		OssKey:       ossKey,
		UploadSource: uploadSource,
		GitHubRepo:   strings.TrimSpace(body.GitHubRepo),
		GitHubRef:    strings.TrimSpace(body.GitHubRef),
		GitHubPath:   strings.TrimSpace(body.GitHubPath),
	})
	if err != nil && !errors.Is(err, parser.ErrPluginValidationFailed) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Hard failure path: ParsePlugin returned zero Spec but validation
	// carries the checklist errors.
	resp := previewCapabilityImportResponse{
		Warnings:         ensureStringSlice(nil),
		PluginValidation: validation,
	}
	if err == nil {
		resp.CanonicalSpec = spec
		resp.SuggestedName = spec.Plugin.Name
		if validation != nil {
			resp.Warnings = ensureStringSlice(validation.Warnings)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// commitCapabilityImport handles POST .../capabilities/import/commit.
// Encrypts inline_secrets, then hands the spec to store.ImportCapability
// which runs the whole import in a single tx. For kind=plugin the spec
// is rebuilt from OSS bytes (the on-disk zip is authoritative).
func commitCapabilityImport(runtimeStore RuntimeStore, blobStore blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		var body commitCapabilityImportBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if strings.TrimSpace(body.Name) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		kind := strings.ToLower(strings.TrimSpace(body.Kind))
		if kind == "" {
			kind = string(body.CanonicalSpec.Kind)
		}
		if kind != string(canonical.KindMCP) && kind != string(canonical.KindSkill) && kind != string(canonical.KindPlugin) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("unknown kind %q (want mcp|skill|plugin)", kind)})
			return
		}

		// Plugin and Skill-zip imports rebuild canonical_spec server-side
		// from the OSS zip; the client-supplied spec is discarded so a
		// forged files/oss_key/sha256 cannot reach the DB.
		//
		// Gate ordering: skill is matched first so kind=skill + OssKey +
		// UploadSource (confused client) doesn't accidentally satisfy the
		// plugin gate and yield a misleading "plugin validation failed".
		spec := body.CanonicalSpec
		var skillSHA256 string
		skillZipShaped := kind == string(canonical.KindSkill) && strings.TrimSpace(body.OssKey) != ""
		pluginShaped := kind == string(canonical.KindPlugin) ||
			(strings.TrimSpace(body.OssKey) != "" && strings.TrimSpace(body.UploadSource) != "")
		switch {
		case skillZipShaped:
			rebuilt, sum, httpErr := rebuildSkillSpecFromOSS(r.Context(), workspaceID, body.OssKey, blobStore)
			if httpErr != nil {
				writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
				return
			}
			spec = rebuilt
			skillSHA256 = sum
		case pluginShaped:
			// Lock kind to plugin so any later read sees the real shape.
			kind = string(canonical.KindPlugin)
			rebuilt, httpErr := rebuildPluginSpecFromOSS(r.Context(), workspaceID, body, blobStore)
			if httpErr != nil {
				writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
				return
			}
			spec = rebuilt
		default:
			if strings.TrimSpace(body.UploadSource) != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_source / github_* fields are only allowed for kind=plugin"})
				return
			}
		}

		// inline_secrets only make sense for mcp (skill / plugin have
		// no env map).
		if kind != string(canonical.KindMCP) && len(body.InlineSecrets) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("inline_secrets is only allowed for kind=mcp (got kind=%q)", kind)})
			return
		}

		// Skill slug fallback when frontmatter didn't yield one. Only
		// mutates Skill specs.
		ensureSkillSlug(&spec, body.Name)

		encryptedInline, httpErr := encryptInlineSecretsForImport(body.InlineSecrets, body.Name)
		if httpErr != nil {
			writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
			return
		}

		// DB column is NOT NULL; write empty object if client omits.
		sourcePayload := body.SourcePayload
		if len(sourcePayload) == 0 {
			sourcePayload = json.RawMessage(`{}`)
		}

		importInput := store.ImportCapabilityInput{
			WorkspaceID:   workspaceID,
			Name:          body.Name,
			Description:   body.Description,
			Visibility:    body.Visibility,
			Type:          fallback(body.Type, kind),
			CreatorID:     actorID,
			Version:       body.Version,
			SourcePayload: sourcePayload,
			Spec:          spec,
			InlineSecrets: encryptedInline,
		}
		if skillZipShaped {
			importInput.OssKey = body.OssKey
			importInput.SHA256 = skillSHA256
		}
		result, err := runtimeStore.ImportCapability(r.Context(), importInput)
		if err != nil {
			writeImportCommitError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, commitCapabilityImportResponse{
			Capability:        result.Capability,
			CapabilityVersion: result.CapabilityVersion,
			CreatedSecretIDs:  ensureStringSlice(result.CreatedSecretIDs),
		})
	}
}

// rebuildPluginSpecFromOSS re-runs the plugin parser against the OSS-hosted
// zip; client-supplied CanonicalSpec is discarded so a forged sha256 cannot
// reach the DB. workspaceID scopes the oss_key ownership check.
func rebuildPluginSpecFromOSS(ctx context.Context, workspaceID string, body commitCapabilityImportBody, blobStore blob.Store) (canonical.Spec, *importHTTPError) {
	if blobStore == nil {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusServiceUnavailable,
			message: "object storage is not configured on this deployment",
		}
	}
	uploadSource := canonical.UploadSource(strings.ToLower(strings.TrimSpace(body.UploadSource)))
	switch uploadSource {
	case canonical.UploadSourceZip:
		// fall through
	case canonical.UploadSourceGitHub:
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusNotImplemented,
			message: "github plugin sync is not implemented yet; upload a zip instead",
		}
	case "":
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusBadRequest,
			message: "upload_source is required for plugin commit (\"zip\" | \"github\")",
		}
	default:
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusBadRequest,
			message: fmt.Sprintf("unknown upload_source %q", body.UploadSource),
		}
	}
	ossKey := strings.TrimSpace(body.OssKey)
	if ossKey == "" {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusBadRequest,
			message: "oss_key is required for plugin commit",
		}
	}
	// Keys minted by this workspace MUST start with
	// capabilities/plugins/<workspaceID>/. 403 over 404 because the key
	// path already encodes the workspace.
	owned, err := blobStore.BelongsToWorkspace(ctx, ossKey, workspaceID)
	if err != nil {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusInternalServerError,
			message: "could not verify storage reference",
		}
	}
	if !owned {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusForbidden,
			message: "oss key does not belong to this workspace",
		}
	}

	zipBytes, err := blobStore.Download(ctx, ossKey)
	if err != nil {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusBadGateway,
			message: "could not fetch uploaded zip from object storage: " + err.Error(),
		}
	}
	spec, _, err := parser.ParsePlugin(zipBytes, parser.PluginSource{
		OssKey:       ossKey,
		UploadSource: uploadSource,
		GitHubRepo:   strings.TrimSpace(body.GitHubRepo),
		GitHubRef:    strings.TrimSpace(body.GitHubRef),
		GitHubPath:   strings.TrimSpace(body.GitHubPath),
	})
	if err != nil {
		return canonical.Spec{}, &importHTTPError{
			status:  http.StatusUnprocessableEntity,
			message: "plugin validation failed: " + err.Error(),
		}
	}
	return spec, nil
}

// rebuildSkillSpecFromOSS is the Skill-zip twin of rebuildPluginSpecFromOSS.
// Re-fetches the zip from OSS and re-runs ParseSkillZip; client-supplied
// spec is discarded. Also returns the SHA-256 of the zip body so the
// caller can write it into capability_version.sha256.
func rebuildSkillSpecFromOSS(ctx context.Context, workspaceID, ossKey string, blobStore blob.Store) (canonical.Spec, string, *importHTTPError) {
	if blobStore == nil {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusServiceUnavailable,
			message: "object storage is not configured on this deployment",
		}
	}
	ossKey = strings.TrimSpace(ossKey)
	if ossKey == "" {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusBadRequest,
			message: "oss_key is required for skill zip commit",
		}
	}
	owned, err := blobStore.BelongsToWorkspace(ctx, ossKey, workspaceID)
	if err != nil {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusInternalServerError,
			message: "could not verify storage reference",
		}
	}
	if !owned {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusForbidden,
			message: "oss key does not belong to this workspace",
		}
	}
	zipBytes, err := blobStore.Download(ctx, ossKey)
	if err != nil {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusBadGateway,
			message: "could not fetch uploaded zip from object storage: " + err.Error(),
		}
	}
	res, err := parser.ParseSkillZip(zipBytes)
	if err != nil {
		return canonical.Spec{}, "", &importHTTPError{
			status:  http.StatusUnprocessableEntity,
			message: "skill zip validation failed: " + err.Error(),
		}
	}
	sum := sha256.Sum256(zipBytes)
	return res.Spec, hex.EncodeToString(sum[:]), nil
}

// commitCapabilityVersionImport adds a new version to an existing capability.
// Wire shape mirrors commitCapabilityImportBody but Name/Description/Visibility/
// Type are ignored (they live on the capability row). Spec.Kind must match
// capability.type or the store returns ErrCapabilityKindMismatch.
func commitCapabilityVersionImport(runtimeStore RuntimeStore, blobStore blob.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaceID, ok := requireWorkspaceCapabilityAdmin(w, r, runtimeStore)
		if !ok {
			return
		}
		actorID, ok := devActorID(w, r)
		if !ok {
			return
		}
		capabilityID := chi.URLParam(r, "capabilityID")
		if !isUUID(capabilityID) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "capability_id must be a valid uuid"})
			return
		}

		var body commitCapabilityImportBody
		if err := decodeBody(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		// Kind must be present on the spec — it's how the store verifies the
		// new version matches capability.type. We don't take Kind off the body
		// envelope here (would be redundant with the spec field).
		if body.CanonicalSpec.Kind == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "canonical_spec.kind is required"})
			return
		}

		// Edit-as-new-version: when the client omits oss_key for a kind that
		// requires OSS bytes (plugin / skill-zip), reuse the previous version's
		// blob so users who only tweak name/description aren't forced to re-upload.
		// Skill markdown (no oss_key, no upload_source) is untouched here.
		if strings.TrimSpace(body.OssKey) == "" {
			needsOSS := body.CanonicalSpec.Kind == canonical.KindPlugin ||
				(body.CanonicalSpec.Kind == canonical.KindSkill && strings.EqualFold(strings.TrimSpace(body.UploadSource), "zip"))
			if needsOSS {
				prevVersions, err := runtimeStore.ListCapabilityVersions(r.Context(), capabilityID)
				if err != nil {
					writeCapabilityError(w, err, "failed to load previous capability version")
					return
				}
				if len(prevVersions) == 0 || strings.TrimSpace(prevVersions[0].OssKey) == "" {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "oss_key is required (no previous version with stored bytes to reuse)"})
					return
				}
				body.OssKey = prevVersions[0].OssKey
				if body.CanonicalSpec.Kind == canonical.KindPlugin && strings.TrimSpace(body.UploadSource) == "" {
					// rebuildPluginSpecFromOSS keys off UploadSource being set
					// in addition to OssKey; supply the canonical upload tag
					// so plugins reuse the same OSS path as the previous version.
					body.UploadSource = "zip"
				}
			}
		}

		// Plugin / Skill-zip parity with commitCapabilityImport: rebuild
		// the spec from OSS bytes. Skill matched first; see
		// commitCapabilityImport for the ordering rationale.
		spec := body.CanonicalSpec
		var skillSHA256 string
		skillZipShaped := body.CanonicalSpec.Kind == canonical.KindSkill && strings.TrimSpace(body.OssKey) != ""
		pluginShaped := body.CanonicalSpec.Kind == canonical.KindPlugin ||
			(strings.TrimSpace(body.OssKey) != "" && strings.TrimSpace(body.UploadSource) != "")
		switch {
		case skillZipShaped:
			rebuilt, sum, httpErr := rebuildSkillSpecFromOSS(r.Context(), workspaceID, body.OssKey, blobStore)
			if httpErr != nil {
				writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
				return
			}
			spec = rebuilt
			skillSHA256 = sum
		case pluginShaped:
			rebuilt, httpErr := rebuildPluginSpecFromOSS(r.Context(), workspaceID, body, blobStore)
			if httpErr != nil {
				writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
				return
			}
			spec = rebuilt
		default:
			if strings.TrimSpace(body.UploadSource) != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "upload_source / github_* fields are only allowed for kind=plugin"})
				return
			}
		}

		// inline_secrets only make sense for mcp.
		if spec.Kind != canonical.KindMCP && len(body.InlineSecrets) > 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("inline_secrets is only allowed for kind=mcp (got kind=%q)", spec.Kind)})
			return
		}

		// body.Name is ignored on the version-import path but still
		// passed through so callers that opt to send it can drive the
		// slug fallback.
		ensureSkillSlug(&spec, body.Name)

		// capability_id is the seed (avoids a roundtrip to fetch the name);
		// uniqueness still comes from the unix-nano suffix.
		encryptedInline, httpErr := encryptInlineSecretsForImport(body.InlineSecrets, capabilityID)
		if httpErr != nil {
			writeJSON(w, httpErr.status, map[string]string{"error": httpErr.message})
			return
		}

		sourcePayload := body.SourcePayload
		if len(sourcePayload) == 0 {
			sourcePayload = json.RawMessage(`{}`)
		}

		versionInput := store.ImportCapabilityVersionInput{
			WorkspaceID:   workspaceID,
			CapabilityID:  capabilityID,
			CreatorID:     actorID,
			Version:       body.Version,
			SourcePayload: sourcePayload,
			Spec:          spec,
			InlineSecrets: encryptedInline,
		}
		if skillZipShaped {
			versionInput.OssKey = body.OssKey
			versionInput.SHA256 = skillSHA256
		}
		result, err := runtimeStore.ImportCapabilityVersion(r.Context(), versionInput)
		if err != nil {
			writeImportCommitError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, commitCapabilityImportResponse{
			Capability:        result.Capability,
			CapabilityVersion: result.CapabilityVersion,
			CreatedSecretIDs:  ensureStringSlice(result.CreatedSecretIDs),
		})
	}
}

// importHTTPError is a small carrier so encryptInlineSecretsForImport can bubble
// the right (status, message) pair back to its caller, which writes the JSON.
type importHTTPError struct {
	status  int
	message string
}

// encryptInlineSecretsForImport validates each inline secret and runs the
// AES-GCM envelope encryption. secretSlugSeed is mixed into the secret
// row's name (workspace_id, name unique across active rows). 503 if
// the master key is unset, 400 for empty fields, 500 on encrypt failure.
func encryptInlineSecretsForImport(
	rawSecrets []commitInlineSecretBody,
	secretSlugSeed string,
) ([]store.ImportInlineSecret, *importHTTPError) {
	out := make([]store.ImportInlineSecret, 0, len(rawSecrets))
	if len(rawSecrets) == 0 {
		return out, nil
	}
	masterKey := os.Getenv("PARSAR_MASTER_KEY")
	if masterKey == "" {
		return nil, &importHTTPError{
			status:  http.StatusServiceUnavailable,
			message: "server has no PARSAR_MASTER_KEY configured; refusing to write inline secrets that could not be decrypted later",
		}
	}
	secretService, err := secrets.New(masterKey)
	if err != nil {
		return nil, &importHTTPError{status: http.StatusInternalServerError, message: "secrets service unavailable: " + err.Error()}
	}
	for i, raw := range rawSecrets {
		server := strings.TrimSpace(raw.ServerName)
		envKey := strings.TrimSpace(raw.EnvKey)
		plaintext := raw.Plaintext // do NOT trim — whitespace in a secret is significant
		if server == "" || envKey == "" {
			return nil, &importHTTPError{
				status:  http.StatusBadRequest,
				message: fmt.Sprintf("inline_secrets[%d]: server_name and env_key are required", i),
			}
		}
		if plaintext == "" {
			return nil, &importHTTPError{
				status:  http.StatusBadRequest,
				message: fmt.Sprintf("inline_secrets[%d] (%s.%s): plaintext is empty", i, server, envKey),
			}
		}
		payload := map[string]any{"value": plaintext}
		encrypted, err := secretService.Encrypt(payload)
		if err != nil {
			return nil, &importHTTPError{
				status:  http.StatusInternalServerError,
				message: fmt.Sprintf("failed to encrypt inline_secrets[%d]", i),
			}
		}
		out = append(out, store.ImportInlineSecret{
			ServerName:       server,
			EnvKey:           envKey,
			SecretName:       deriveImportSecretName(secretSlugSeed, server, envKey),
			EncryptedPayload: encrypted,
			KeyVersion:       "v1",
		})
	}
	return out, nil
}

// deriveImportSecretName builds a workspace-unique name for the secrets
// table. Format: "cap:<name-slug>:<server-slug>:<env-slug>:<unix-nano>".
// The unix-nano suffix guarantees distinctness across repeat imports.
func deriveImportSecretName(capabilityName, serverName, envKey string) string {
	return fmt.Sprintf("cap:%s:%s:%s:%d",
		slugForSecretName(capabilityName),
		slugForSecretName(serverName),
		slugForSecretName(envKey),
		time.Now().UTC().UnixNano(),
	)
}

// slugForSecretName lowercases + replaces non-alnum with '-' so the assembled
// name stays human-readable in admin tooling.
func slugForSecretName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "x"
	}
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b = append(b, c)
		default:
			if len(b) > 0 && b[len(b)-1] != '-' {
				b = append(b, '-')
			}
		}
	}
	out := strings.Trim(string(b), "-")
	if out == "" {
		return "x"
	}
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}

// ensureSkillSlug fills in an empty SkillSpec.Slug at commit time, so the
// import flow can't be blocked by undecodable frontmatter. Fallback order:
// existing spec slug, kebab(formName), "skill-<hex>". Returns false only
// when the random fallback was taken.
func ensureSkillSlug(spec *canonical.Spec, formName string) bool {
	if spec == nil || spec.Skill == nil {
		return true
	}
	if strings.TrimSpace(spec.Skill.Slug) != "" {
		return true
	}
	if derived := skillSlugFromName(formName); derived != "" {
		spec.Skill.Slug = derived
		return true
	}
	spec.Skill.Slug = "skill-" + randomSlugSuffix(6)
	return false
}

// skillSlugFromName mirrors parser.kebabFromName. Duplicated rather than
// re-exported so the parser package doesn't take an http-layer dep.
func skillSlugFromName(name string) string {
	clean := strings.ToLower(strings.TrimSpace(name))
	if clean == "" {
		return ""
	}
	var b strings.Builder
	lastWasDash := true
	for _, r := range clean {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastWasDash = false
		default:
			if !lastWasDash {
				b.WriteByte('-')
				lastWasDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// randomSlugSuffix returns `bytesLen` random bytes hex-encoded. Used only
// for the "skill-<hex>" last-resort slug; collisions don't break uniqueness
// (capability.name is the workspace-scoped unique key).
func randomSlugSuffix(bytesLen int) string {
	b := make([]byte, bytesLen)
	if _, err := rand.Read(b); err != nil {
		// rand.Read in stdlib never fails on linux/darwin; timestamp
		// fallback keeps the slug populated regardless.
		return fmt.Sprintf("%x", time.Now().UTC().UnixNano())
	}
	return hex.EncodeToString(b)
}

// writeImportParseError maps parser errors to HTTP statuses. ErrEmptyInput
// and ErrUnsupportedSourceFormat get 400; everything else is 422.
func writeImportParseError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, parser.ErrEmptyInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, parser.ErrUnsupportedSourceFormat):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	}
}

// writeImportCommitError maps store errors to HTTP. Known sentinels surface
// as 4xx; unrecognized ones come back as 500 with a fallback message.
func writeImportCommitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrCredentialKindNotFound):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrUnknownWorkspace):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "workspace not found"})
	case errors.Is(err, store.ErrUnknownCapability):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "capability not found"})
	case errors.Is(err, store.ErrCapabilityKindMismatch):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrCapabilityNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "当前工作空间已有同名能力。请修改源文件里的 name 字段后重新打包上传,或先在能力列表中删除已有的同名能力。",
		})
	default:
		// Spec validation errors come back without a sentinel; surface
		// as 422 so the UI can render them next to the offending field.
		msg := err.Error()
		if strings.Contains(msg, "spec invalid") || strings.Contains(msg, "post-patch spec invalid") || strings.Contains(msg, "inline_secret") {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": msg})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
	}
}

// ensureStringSlice replaces nil with an empty slice so JSON never emits
// "null" for these never-null-by-contract fields.
func ensureStringSlice(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// fallback returns primary if non-empty, else alt.
func fallback(primary, alt string) string {
	if strings.TrimSpace(primary) == "" {
		return alt
	}
	return primary
}
