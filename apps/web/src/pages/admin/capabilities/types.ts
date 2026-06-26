/**
 * TypeScript mirror of the canonical capability spec + import wire types.
 *
 * Source of truth: server/internal/capability/canonical/ and
 * server/internal/dev/capability_import_routes.go.
 *
 * Hand-written; regenerate via openapi-typescript when the OpenAPI
 * surface stabilizes.
 */
import type { Capability, CapabilityVersion } from "../../../lib/api-types"

/* ---------- canonical.Spec ----------------------------------------------- */

export type CanonicalKind = "mcp" | "skill" | "plugin" | "system_prompt"

export type EnvMode = "literal" | "inline_secret" | "credential_ref"

/**
 * Mirrors canonical.EnvValue. Per the import contract, only ONE per-mode
 * field family should be populated:
 *   literal              → literal (string)
 *   inline_secret        → secret_id (server fills on commit; empty in preview/edit)
 *   credential_ref       → credential_kind_code
 */
export interface CanonicalEnvValue {
  mode: EnvMode
  literal?: string
  secret_id?: string
  credential_kind_code?: string
}

export interface CanonicalMCPServer {
  name: string
  command: string
  args?: string[]
  env?: Record<string, CanonicalEnvValue>
  startup_timeout_sec?: number
}

export interface CanonicalMCPSpec {
  servers: CanonicalMCPServer[]
}

/**
 * Mirrors canonical.SkillFile. One entry per non-SKILL.md file from a
 * Skill zip; SKILL.md itself stays in `instruction`. `kind` lets the UI
 * pick rendering without parsing the extension.
 */
export type SkillFileKind = "markdown" | "script" | "asset"

export interface SkillFile {
  path: string
  content: string
  kind: SkillFileKind
}

export interface CanonicalSkillSpec {
  slug: string
  title: string
  description?: string
  instruction: string
  trigger?: string
  /**
   * Non-SKILL.md files carried with the skill (references/*, scripts/*,
   * etc). Empty/omitted for single-file paste imports. Populated by
   * server-side ParseSkillZip; the client never authors these.
   */
  files?: SkillFile[]
}

/**
 * Mirrors canonical.PluginSpec. Server is authoritative for
 * oss_key / sha256 / version / name — frontend never authors these
 * directly. Same type also used for Skill zip uploads via upload_source.
 */
export type PluginUploadSource = "zip" | "github"

export interface CanonicalPluginSpec {
  name: string
  display_name?: string
  version: string
  description?: string
  author?: string
  keywords?: string[]
  oss_key: string
  sha256: string
  upload_source: PluginUploadSource
  github_repo?: string
  github_ref?: string
  github_path?: string
}

export interface CanonicalSpec {
  schema_version: number
  kind: CanonicalKind
  mcp?: CanonicalMCPSpec
  skill?: CanonicalSkillSpec
  plugin?: CanonicalPluginSpec
  system_prompt?: CanonicalSystemPromptSpec
}

/**
 * Mirrors canonical.SystemPromptSpec. mode defaults to "append" server-side
 * when omitted; the create form sends one explicitly so the UI choice
 * round-trips into the version row.
 */
export type SystemPromptMode = "append" | "override"

export interface CanonicalSystemPromptSpec {
  prompt: string
  mode?: SystemPromptMode
}

/* ---------- Parser source format ---------------------------------------- */

/**
 * Parser source format discriminator. The combinations the server accepts:
 *   kind="mcp"    → "json" | "toml"
 *   kind="skill"  → "markdown" (single-file paste) | "zip" (multi-file)
 *   kind="plugin" → "zip" (only — plugins are zip-only)
 */
export type SourceFormat = "json" | "toml" | "markdown" | "zip"

/* ---------- /import/preview --------------------------------------------- */

/**
 * Wire shape for POST .../import/preview. The body is a union: mcp/skill
 * use raw_text + source_format; plugin uses oss_key + upload_source +
 * (optional) github_* fields. The server discriminates by `kind`.
 */
export interface ImportPreviewRequest {
  kind: CanonicalKind
  raw_text?: string
  source_format?: SourceFormat

  // plugin-only
  oss_key?: string
  upload_source?: PluginUploadSource
  github_repo?: string
  github_ref?: string
  github_path?: string
}

/**
 * Mirrors parser.PluginValidationResult on the server. Surfaced when the
 * preview endpoint runs the plugin validator — errors are blocking,
 * warnings advisory.
 */
export interface PluginValidationResult {
  valid: boolean
  errors?: string[]
  warnings?: string[]
  manifest?: {
    name?: string
    display_name?: string
    version?: string
    description?: string
    author?: { name?: string; email?: string; url?: string }
    keywords?: string[]
  }
}

export interface ImportPreviewResponse {
  canonical_spec: CanonicalSpec
  warnings: string[]
  suggested_name: string
  /** Plugin-only structured validation. Empty for mcp/skill previews. */
  plugin_validation?: PluginValidationResult
}

/* ---------- /import/commit ---------------------------------------------- */

export interface ImportInlineSecretInput {
  server_name: string
  env_key: string
  /** Plaintext. Encrypted server-side; never echoed back. */
  plaintext: string
}

export interface ImportCommitRequest {
  kind: CanonicalKind
  name: string
  description?: string
  /** capability.visibility — "workspace" (default) | "public". */
  visibility?: "workspace" | "public"
  /** Defaults server-side to "1.0.0" when empty. */
  version?: string
  /** capability.type column — falls back to kind when empty. */
  type?: string
  /** Opaque blob the server writes verbatim to capability_version.source_payload. */
  source_payload?: unknown
  /** Required for mcp/skill; ignored for plugin (server rebuilds from OSS). */
  canonical_spec: CanonicalSpec
  inline_secrets?: ImportInlineSecretInput[]

  // plugin-only — server uses these to rebuild canonical_spec
  oss_key?: string
  upload_source?: PluginUploadSource
  github_repo?: string
  github_ref?: string
  github_path?: string
}

export interface ImportCommitResponse {
  capability: Capability
  capability_version: CapabilityVersion
  created_secret_ids: string[]
}

/* ---------- /capabilities/{id}/versions/import/commit ------------------- */

/**
 * Wire shape for adding a new version to an existing capability via the import
 * flow. Same canonical_spec + inline_secrets contract as ImportCommitRequest,
 * but Name/Description/Visibility/Type are not accepted — those live on the
 * capability row, not the version row.
 *
 * The server still requires canonical_spec.kind to match capability.type;
 * we lock kind in the UI but the server enforces it as a 422 anyway.
 */
export interface ImportCapabilityVersionCommitRequest {
  /** Defaults server-side to "1.0.0" when empty. */
  version?: string
  /** Opaque blob the server writes verbatim to capability_version.source_payload. */
  source_payload?: unknown
  canonical_spec: CanonicalSpec
  inline_secrets?: ImportInlineSecretInput[]

  // plugin-only — the server rebuilds canonical_spec from these
  // (oss_key + upload_source) and ignores the client-supplied spec
  // body. Mirrors ImportCommitRequest so the add-version path keeps
  // the same rebuild trust boundary as create.
  oss_key?: string
  upload_source?: PluginUploadSource
  github_repo?: string
  github_ref?: string
  github_path?: string
}

/* ---------- credential_kinds -------------------------------------------- */

export type CredentialKindSource = "platform_oauth" | "platform_model" | "user_defined"

export interface CredentialKindRead {
  id: string
  code: string
  display_name: string
  description: string
  value_schema: Record<string, unknown>
  built_in: boolean
  source: CredentialKindSource
  created_by?: string
  created_at: string
  updated_at: string
}

export interface ListCredentialKindsResponse {
  items: CredentialKindRead[]
}

export interface CreateCredentialKindRequest {
  code: string
  display_name: string
  description?: string
  source?: CredentialKindSource
  value_schema?: Record<string, unknown>
}
