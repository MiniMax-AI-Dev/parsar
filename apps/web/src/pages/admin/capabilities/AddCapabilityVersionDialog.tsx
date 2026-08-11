/**
 * Edit / Add-new-version surface. Differences from ImportCapabilityDialog:
 *   - kind LOCKED to capability.type (backend also enforces; 422).
 *   - Name + description fields ARE editable here (PATCH'd before the version
 *     commit). The standalone "edit metadata only" dialog was removed in
 *     favor of this single surface.
 *   - The server assigns the next version automatically on save.
 *   - When the previous version was imported, prefill rawText + format so the
 *     user can tweak. inline_secret plaintexts CANNOT carry forward (server
 *     only stores ciphertext).
 *   - Plugin kinds can reuse the previous OSS bytes. Stored Skill zips are
 *     downloaded into the browser editor and uploaded again on save.
 *   - Commits to .../capabilities/{id}/versions/import/commit (after an
 *     optional PATCH for name/description).
 */
import { useEffect, useMemo, useState } from "react"
import { strFromU8, strToU8, unzipSync, zipSync } from "fflate"
import { Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { ApiError } from "../../../lib/api-client"
import { useUpdateCapability } from "../../../lib/api-capabilities"
import type { Capability, CapabilityVersion } from "../../../lib/api-types"

import {
  downloadStoredZip,
  putToPresignedURL,
  useImportCapabilityVersionMutation,
  usePresignUploadMutation,
} from "./api"
import { ImportMCPForm } from "./ImportMCPForm"
import { ImportSkillForm } from "./ImportSkillForm"
import { ImportPluginForm, type PluginUploadState } from "./ImportPluginForm"
import { SkillFileTree, type SkillFileTreeEntry } from "./SkillFileTree"
import { isImportSpecReady } from "./importValidation"
import type {
  CanonicalKind,
  CanonicalSpec,
  ImportCapabilityVersionCommitRequest,
  ImportInlineSecretInput,
  SourceFormat,
} from "./types"

interface Props {
  workspaceID: string | null
  capability: Capability
  /** Most-recent version, used for prefill. Undefined when capability has no versions yet. */
  latestVersion: CapabilityVersion | undefined
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Toast / parent feedback after a successful commit. */
  onCommitted: () => void
}

const SKILL_ZIP_MAX_BYTES = 8 * 1024 * 1024

interface SkillArchiveEntry {
  archivePath: string
  path: string
  bytes: Uint8Array
  text: string | null
  dirty: boolean
  directory: boolean
  hidden: boolean
  kind: SkillFileTreeEntry["kind"]
}

export function AddCapabilityVersionDialog({
  workspaceID,
  capability,
  latestVersion,
  open,
  onOpenChange,
  onCommitted,
}: Props) {
  const { t } = useTranslation("admin")
  const commitMut = useImportCapabilityVersionMutation(workspaceID, capability.id)
  const updateMut = useUpdateCapability(workspaceID)
  const presignMut = usePresignUploadMutation(workspaceID)

  const kind = capability.type as CanonicalKind

  const [name, setName] = useState(capability.name)
  const [description, setDescription] = useState(capability.description ?? "")
  const [spec, setSpec] = useState<CanonicalSpec | null>(null)
  const [inlineSecrets, setInlineSecrets] = useState<ImportInlineSecretInput[]>([])
  const [rawText, setRawText] = useState("")
  const [sourceFormat, setSourceFormat] = useState<SourceFormat>(
    kind === "skill" ? "markdown" : "json",
  )
  // Plugin commit sends oss_key + upload_source (not canonical_spec) so
  // the server rebuilds the spec from OSS bytes.
  const [pluginUpload, setPluginUpload] = useState<PluginUploadState>({
    ossKey: null,
    uploadSource: null,
    validation: null,
  })
  /** Skill zip ossKey from ImportSkillForm; null in paste mode. */
  const [skillOssKey, setSkillOssKey] = useState<string | null>(null)
  const [skillArchive, setSkillArchive] = useState<SkillArchiveEntry[] | null>(null)
  const [skillArchiveStatus, setSkillArchiveStatus] = useState<
    "idle" | "loading" | "ready" | "error"
  >("idle")
  const [skillArchiveError, setSkillArchiveError] = useState<string | null>(null)
  const [skillLoadAttempt, setSkillLoadAttempt] = useState(0)
  const [submitError, setSubmitError] = useState<string | null>(null)
  const [isSaving, setIsSaving] = useState(false)

  const prefill = usePrefillFromLatest(latestVersion)
  const latestSpec = latestVersion?.canonical_spec as CanonicalSpec | undefined
  const editsStoredSkillZip = kind === "skill" && !!latestVersion?.oss_key?.trim()
  const skillTreeFiles = useMemo<SkillFileTreeEntry[]>(
    () =>
      (skillArchive ?? [])
        .filter((file) => !file.directory && !file.hidden)
        .map((file) => ({
          path: file.path,
          content: file.text,
          kind: file.kind,
          size: file.bytes.byteLength,
        })),
    [skillArchive],
  )
  // For plugin / skill-zip rounds where the user keeps the previous OSS blob,
  // we display the existing filename (derived from the latest version's oss_key)
  // so the form feels like "edit", not "blank slate".
  const inheritedOssLabel = useMemo(() => {
    const key = latestVersion?.oss_key?.trim()
    if (!key) return null
    const tail = key.split("/").pop() ?? key
    return tail
  }, [latestVersion])

  // Reset only on the open transition — resetting on every render would
  // clobber the user's edits.
  useEffect(() => {
    if (!open) return
    setName(capability.name)
    setDescription(capability.description ?? "")
    setSpec(null)
    setInlineSecrets([])
    setRawText(prefill.rawText)
    setSourceFormat(prefill.format)
    setPluginUpload({ ossKey: null, uploadSource: null, validation: null })
    setSkillOssKey(null)
    setSkillArchive(null)
    setSkillArchiveStatus(editsStoredSkillZip ? "loading" : "idle")
    setSkillArchiveError(null)
    setSubmitError(null)
    setIsSaving(false)
    commitMut.reset()
    updateMut.reset()
    presignMut.reset()
    // intentionally only on the open transition
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  useEffect(() => {
    if (!open || !editsStoredSkillZip || !workspaceID || !latestVersion?.oss_key) return
    let cancelled = false
    void downloadStoredZip(workspaceID, latestVersion.oss_key)
      .then((bytes) => unpackSkillArchive(bytes))
      .then((archive) => {
        if (cancelled) return
        setSkillArchive(archive)
        setSkillArchiveStatus("ready")
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setSkillArchive(null)
        setSkillArchiveStatus("error")
        setSkillArchiveError(formatError(error))
      })
    return () => {
      cancelled = true
    }
  }, [editsStoredSkillZip, latestVersion?.oss_key, open, skillLoadAttempt, workspaceID])

  const errMsg =
    submitError ??
    (commitMut.error instanceof ApiError
      ? commitMut.error.envelope.message
      : commitMut.error instanceof Error
        ? commitMut.error.message
        : updateMut.error instanceof ApiError
          ? updateMut.error.envelope.message
          : updateMut.error instanceof Error
            ? updateMut.error.message
            : null)

  const trimmedName = name.trim()
  const nameError = !trimmedName
    ? t("capabilities.errors.nameRequired")
    : trimmedName.length > 50
      ? t("capabilities.errors.nameTooLong")
      : null
  // For plugin / skill-zip kinds we accept "no new upload" and let the server
  // reuse the previous OSS blob. So the canSubmit guard relaxes when an
  // inherited blob exists.
  const pluginHasUsableArtifact =
    kind !== "plugin"
      ? true
      : pluginUpload.ossKey
        ? (pluginUpload.validation?.valid ?? false)
        : !!inheritedOssLabel

  const skillSpecReady =
    kind !== "skill"
      ? true
      : editsStoredSkillZip
        ? skillArchiveStatus === "ready" &&
          skillTreeFiles.some((file) => file.path.toLowerCase() === "skill.md")
        : !!skillOssKey ||
          !!inheritedOssLabel ||
          (!!spec && isImportSpecReady(kind, spec, inlineSecrets))

  const mcpSpecReady =
    kind !== "mcp" ? true : !!spec && isImportSpecReady(kind, spec, inlineSecrets)

  const canSubmit =
    !commitMut.isPending &&
    !updateMut.isPending &&
    !presignMut.isPending &&
    !isSaving &&
    !!workspaceID &&
    !nameError &&
    pluginHasUsableArtifact &&
    skillSpecReady &&
    mcpSpecReady

  const submit = async () => {
    if (!canSubmit) return
    setSubmitError(null)
    setIsSaving(true)
    try {
      let editedSkillOssKey: string | undefined
      if (editsStoredSkillZip) {
        if (!workspaceID || !skillArchive) throw new Error("Skill archive is not ready")
        const zipFile = buildSkillZipFile(
          skillArchive,
          `${safeZipBase(latestSpec?.skill?.slug ?? capability.name)}.zip`,
        )
        if (zipFile.size > SKILL_ZIP_MAX_BYTES) {
          throw new Error("Edited Skill zip exceeds the 8 MiB upload limit")
        }
        const presign = await presignMut.mutateAsync({
          filename: zipFile.name,
          prefix: "skill",
        })
        await putToPresignedURL(presign, zipFile)
        editedSkillOssKey = presign.ossKey
      }

      const nextDesc = description.trim()
      const nameChanged = trimmedName !== capability.name
      const descChanged = nextDesc !== (capability.description ?? "").trim()
      if (nameChanged || descChanged) {
        await updateMut.mutateAsync({
          capabilityID: capability.id,
          body: {
            name: nameChanged ? trimmedName : undefined,
            description: descChanged ? nextDesc : undefined,
          },
        })
      }

      // The backend reparses Skill ZIP bytes and remains the canonical source.
      const fallbackSpec: CanonicalSpec | null =
        spec ??
        (editsStoredSkillZip ? (latestSpec ?? null) : null) ??
        (kind === "plugin"
          ? ({ kind: "plugin" } as unknown as CanonicalSpec)
          : kind === "skill"
            ? ({ kind: "skill" } as unknown as CanonicalSpec)
            : null)
      if (!fallbackSpec) throw new Error("Capability content is not ready")

      const ossKeyToSend =
        editedSkillOssKey ??
        (kind === "plugin"
          ? (pluginUpload.ossKey ?? undefined)
          : kind === "skill"
            ? (skillOssKey ?? undefined)
            : undefined)
      const uploadSourceToSend = editsStoredSkillZip
        ? "zip"
        : kind === "plugin"
          ? (pluginUpload.uploadSource ?? undefined)
          : kind === "skill" && skillOssKey
            ? "zip"
            : undefined

      const payload: ImportCapabilityVersionCommitRequest = {
        canonical_spec: fallbackSpec,
        inline_secrets: kind === "plugin" || inlineSecrets.length === 0 ? undefined : inlineSecrets,
        source_payload:
          editsStoredSkillZip && latestVersion
            ? editedSourcePayload(latestVersion)
            : rawText
              ? { raw_text: rawText, source_format: sourceFormat }
              : undefined,
        oss_key: ossKeyToSend,
        upload_source: uploadSourceToSend,
      }
      await commitMut.mutateAsync(payload)
      onOpenChange(false)
      onCommitted()
    } catch (error) {
      setSubmitError(formatError(error))
    } finally {
      setIsSaving(false)
    }
  }

  // Inherited inline_secret env entries (server-allocated secret_id from
  // a prior version) can't be reused — surface a warning.
  const inheritedInlineSecrets = useMemo(
    () => collectInheritedInlineSecrets(kind, spec),
    [kind, spec],
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-6xl overflow-x-hidden overflow-y-auto"
        onInteractOutside={(e) => {
          if (isSaving || commitMut.isPending || updateMut.isPending) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>
            {t("capabilities.versions.add.title", { name: capability.name })}
          </DialogTitle>
          <DialogDescription>{t("capabilities.versions.add.description")}</DialogDescription>
        </DialogHeader>

        {prefill.didPrefill && (
          <InfoBanner>
            {t("capabilities.versions.add.prefillFromLatest", {
              version: latestVersion?.version ?? "",
              defaultValue:
                "Pre-filled with the previous version ({{version}}). Edits will be submitted as a new version.",
            })}
          </InfoBanner>
        )}
        {editsStoredSkillZip && inheritedOssLabel && (
          <InfoBanner>
            {t("capabilities.versions.add.editStoredZip", {
              filename: inheritedOssLabel,
              defaultValue:
                "Editing {{filename}}. Saving uploads the complete folder as a new version.",
            })}
          </InfoBanner>
        )}
        {!editsStoredSkillZip && inheritedOssLabel && (kind === "plugin" || kind === "skill") && (
          <InfoBanner>
            {t("capabilities.versions.add.reuseExistingZip", {
              filename: inheritedOssLabel,
              defaultValue:
                "Current version package: {{filename}}. If you do not re-upload, the new version will reuse this package.",
            })}
          </InfoBanner>
        )}
        {inheritedInlineSecrets.length > 0 && (
          <WarningBanner>
            {t("capabilities.versions.add.inlineSecretLostWarning", {
              keys: inheritedInlineSecrets.map((e) => `${e.server}.${e.envKey}`).join(", "),
              defaultValue:
                "Previous-version inline secrets ({{keys}}) are hidden. Re-enter them in plaintext to keep, or switch to managed credentials.",
            })}
          </WarningBanner>
        )}

        <div className="mt-2 grid gap-3 md:grid-cols-2">
          <Field label={t("capabilities.fields.name.label")} required>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("capabilities.fields.name.placeholder")}
            />
          </Field>
          <Field label={t("capabilities.fields.description.label")}>
            <Input
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder={t("capabilities.fields.description.placeholder")}
            />
          </Field>
        </div>

        {nameError && (
          <div
            role="alert"
            className="mt-2 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
          >
            {nameError}
          </div>
        )}

        <div className="mt-3">
          {kind === "mcp" ? (
            <ImportMCPForm
              workspaceID={workspaceID}
              value={spec}
              onChange={setSpec}
              inlineSecrets={inlineSecrets}
              onInlineSecretsChange={setInlineSecrets}
              // add-version keeps capability.name; the preview's
              // suggested_name is ignored on purpose.
              onSuggestedName={() => {}}
              onRawTextChange={(raw, fmt) => {
                setRawText(raw)
                setSourceFormat(fmt)
              }}
              initialRawText={prefill.rawText}
              initialFormat={prefill.format}
            />
          ) : kind === "skill" && editsStoredSkillZip ? (
            <div className="grid gap-3">
              {skillArchiveStatus === "loading" && (
                <div className="flex min-h-40 items-center justify-center gap-2 rounded-lg border border-line bg-surface-subtle text-sm text-fg-muted">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t("capabilities.versions.add.loadingZip", "Loading the current Skill package…")}
                </div>
              )}
              {skillArchiveStatus === "error" && (
                <div className="rounded-lg border border-danger-border bg-danger-subtle p-4 text-sm text-danger-emphasis">
                  <p className="break-all">{skillArchiveError}</p>
                  <Button
                    className="mt-3"
                    size="sm"
                    variant="outline"
                    onClick={() => {
                      setSkillArchiveStatus("loading")
                      setSkillArchiveError(null)
                      setSkillLoadAttempt((attempt) => attempt + 1)
                    }}
                  >
                    {t("capabilities.actions.retry", "Retry")}
                  </Button>
                </div>
              )}
              {skillArchiveStatus === "ready" && (
                <SkillFileTree
                  files={skillTreeFiles}
                  editable
                  onFileChange={(path, content) => {
                    setSkillArchive(
                      (current) =>
                        current?.map((file) =>
                          file.path === path ? { ...file, text: content, dirty: true } : file,
                        ) ?? null,
                    )
                  }}
                />
              )}
            </div>
          ) : kind === "skill" ? (
            <ImportSkillForm
              workspaceID={workspaceID}
              value={spec}
              onChange={setSpec}
              onSuggestedName={() => {}}
              // add-version keeps capability.description; the version
              // body shouldn't silently rewrite it.
              onSuggestedDescription={() => {}}
              onRawTextChange={(raw, fmt) => {
                setRawText(raw)
                setSourceFormat(fmt)
              }}
              onOssKeyChange={setSkillOssKey}
              initialRawText={prefill.rawText}
            />
          ) : (
            <ImportPluginForm
              workspaceID={workspaceID}
              onChange={setSpec}
              onUploadStateChange={setPluginUpload}
              onSuggestedName={() => {}}
            />
          )}
        </div>

        {errMsg && (
          <div
            role="alert"
            className="break-all rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
          >
            {errMsg}
          </div>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            disabled={isSaving || commitMut.isPending || updateMut.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("capabilities.actions.cancel")}
          </Button>
          <Button size="sm" disabled={!canSubmit} onClick={submit}>
            {(isSaving || commitMut.isPending || updateMut.isPending) && (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            )}
            {t("capabilities.actions.addVersion")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

/**
 * Decode the latest version's stored source_payload into the rawText + format
 * the subforms expect. Falls back to {rawText:"", format:"json"|"markdown"}
 * when the previous version wasn't imported through the new flow (legacy
 * versions only have content/git fields).
 */
function usePrefillFromLatest(latestVersion: CapabilityVersion | undefined): {
  rawText: string
  format: SourceFormat
  didPrefill: boolean
} {
  return useMemo(() => {
    const sp = latestVersion?.source_payload as
      { raw_text?: string; source_format?: string; format?: string; body?: string } | undefined
    // Accepts two source_payload shapes for forward-compat:
    //   { raw_text, source_format } — new dialog
    //   { format, body }            — early server code
    const rawText = sp?.raw_text ?? sp?.body ?? ""
    const fmtRaw = (sp?.source_format ?? sp?.format ?? "").toLowerCase()
    const valid: SourceFormat[] = ["json", "toml", "markdown"]
    const format = (valid as string[]).includes(fmtRaw) ? (fmtRaw as SourceFormat) : "json"
    return { rawText, format, didPrefill: rawText.length > 0 }
  }, [latestVersion])
}

function unpackSkillArchive(bytes: Uint8Array): SkillArchiveEntry[] {
  const unpacked = unzipSync(bytes)
  const archivePaths = Object.keys(unpacked)
  const normalizedPaths = archivePaths.map(normalizeZipPath)
  const root = detectSingleRoot(normalizedPaths)
  const entries = archivePaths.map((archivePath): SkillArchiveEntry => {
    const normalized = normalizeZipPath(archivePath)
    const path = root && normalized.startsWith(root) ? normalized.slice(root.length) : normalized
    const directory = /[\\/]$/.test(archivePath) || path === ""
    const hidden = directory || isMacOSMetadata(path)
    const fileBytes = unpacked[archivePath]
    return {
      archivePath,
      path,
      bytes: fileBytes,
      text: directory ? null : decodeEditableText(path, fileBytes),
      dirty: false,
      directory,
      hidden,
      kind: inferSkillFileKind(path),
    }
  })
  if (!entries.some((entry) => !entry.directory && entry.path.toLowerCase() === "skill.md")) {
    throw new Error("The stored package does not contain a root SKILL.md")
  }
  return entries.sort((a, b) => {
    if (a.path.toLowerCase() === "skill.md") return -1
    if (b.path.toLowerCase() === "skill.md") return 1
    return a.path.localeCompare(b.path)
  })
}

function buildSkillZipFile(entries: SkillArchiveEntry[], filename: string): File {
  const files: Record<string, Uint8Array> = {}
  for (const entry of entries) {
    if (entry.directory) continue
    files[entry.archivePath] =
      entry.dirty && entry.text !== null ? strToU8(entry.text) : entry.bytes
  }
  const zipped = zipSync(files, { level: 6 })
  return new File([zipped], filename, { type: "application/zip" })
}

function decodeEditableText(path: string, bytes: Uint8Array): string | null {
  if (path.toLowerCase() === "skill.md") return strFromU8(bytes)
  if (!isTextPath(path)) return null
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}

function isTextPath(path: string): boolean {
  const lower = path.toLowerCase()
  const basename = lower.split("/").pop() ?? lower
  if (["dockerfile", "makefile", "license", ".gitignore", ".env"].includes(basename)) {
    return true
  }
  return [
    ".md",
    ".markdown",
    ".txt",
    ".json",
    ".yaml",
    ".yml",
    ".toml",
    ".py",
    ".sh",
    ".bash",
    ".js",
    ".jsx",
    ".mjs",
    ".cjs",
    ".ts",
    ".tsx",
    ".css",
    ".html",
    ".htm",
    ".xml",
    ".svg",
    ".csv",
    ".ini",
    ".cfg",
    ".conf",
    ".sql",
    ".graphql",
    ".gql",
    ".go",
    ".rs",
    ".java",
    ".rb",
    ".php",
    ".pl",
    ".ps1",
    ".bat",
    ".cmd",
  ].some((extension) => lower.endsWith(extension))
}

function inferSkillFileKind(path: string): SkillFileTreeEntry["kind"] {
  const lower = path.toLowerCase()
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) return "markdown"
  if (
    [".py", ".sh", ".bash", ".js", ".ts", ".mjs", ".cjs"].some((extension) =>
      lower.endsWith(extension),
    )
  ) {
    return "script"
  }
  return "asset"
}

function normalizeZipPath(path: string): string {
  return path.replace(/\\/g, "/").replace(/\/$/, "")
}

function detectSingleRoot(paths: string[]): string {
  const first = paths.find(
    (path) =>
      path !== "" && path !== "__MACOSX" && !path.startsWith("__MACOSX/") && path.includes("/"),
  )
  if (!first) return ""
  const slash = first.indexOf("/")
  if (slash <= 0) return ""
  const root = first.slice(0, slash + 1)
  if (root.startsWith(".")) return ""
  for (const path of paths) {
    if (path === "" || path === "__MACOSX" || path.startsWith("__MACOSX/")) continue
    if (`${path}/` === root) continue
    if (!path.startsWith(root)) return ""
  }
  return root
}

function isMacOSMetadata(path: string): boolean {
  return (
    path === "__MACOSX" ||
    path.startsWith("__MACOSX/") ||
    path.endsWith("/.DS_Store") ||
    path === ".DS_Store"
  )
}

function editedSourcePayload(version: CapabilityVersion): Record<string, unknown> {
  const existing = version.source_payload
  const source =
    existing && typeof existing === "object" && !Array.isArray(existing)
      ? (existing as Record<string, unknown>)
      : {}
  return {
    ...source,
    edited_from_version_id: version.id,
    edited_via: "web_file_editor",
  }
}

function safeZipBase(value: string): string {
  const safe = value
    .trim()
    .replace(/[^a-zA-Z0-9._-]+/g, "-")
    .replace(/^-+|-+$/g, "")
  return safe || "skill"
}

function formatError(error: unknown): string {
  if (error instanceof ApiError) return error.envelope.message
  return error instanceof Error ? error.message : String(error)
}

/**
 * Walks the parsed spec for inline_secret env entries that already carry a
 * server-allocated secret_id. Those secret rows belong to the PREVIOUS version
 * and cannot be reused — the new version needs either a fresh plaintext or a
 * switch to credential_ref. We render a single warning banner listing the
 * affected (server, env_key) pairs.
 */
function collectInheritedInlineSecrets(
  kind: CanonicalKind,
  spec: CanonicalSpec | null,
): Array<{ server: string; envKey: string }> {
  if (kind !== "mcp" || !spec?.mcp) return []
  const out: Array<{ server: string; envKey: string }> = []
  for (const srv of spec.mcp.servers) {
    for (const [envKey, value] of Object.entries(srv.env ?? {})) {
      if (value.mode === "inline_secret" && value.secret_id?.trim()) {
        out.push({ server: srv.name, envKey })
      }
    }
  }
  return out
}

function Field({
  label,
  required,
  children,
}: {
  label: string
  required?: boolean
  children: React.ReactNode
}) {
  return (
    <label className="grid gap-1.5">
      <span className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="text-danger"> *</span>}
      </span>
      {children}
    </label>
  )
}

function InfoBanner({ children }: { children: React.ReactNode }) {
  return (
    <div className="mt-2 rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm text-fg-muted">
      {children}
    </div>
  )
}

function WarningBanner({ children }: { children: React.ReactNode }) {
  return (
    <div
      role="alert"
      className="mt-2 break-all rounded-md border border-warning-border bg-warning-subtle px-3 py-2 text-sm text-warning-emphasis"
    >
      {children}
    </div>
  )
}
