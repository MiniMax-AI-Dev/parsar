/**
 * Edit / Add-new-version surface. Differences from ImportCapabilityDialog:
 *   - kind LOCKED to capability.type (backend also enforces; 422).
 *   - Name + description fields ARE editable here (PATCH'd before the version
 *     commit). The standalone "edit metadata only" dialog was removed in
 *     favor of this single surface.
 *   - Version field is REQUIRED to differ from the current latest version.
 *   - When the previous version was imported, prefill rawText + format so the
 *     user can tweak. inline_secret plaintexts CANNOT carry forward (server
 *     only stores ciphertext).
 *   - Plugin / skill-zip kinds: if the user doesn't upload a new zip, the
 *     server reuses the previous version's OSS bytes (commit handler treats
 *     missing oss_key as "reuse latest"). UI shows the existing filename.
 *   - Commits to .../capabilities/{id}/versions/import/commit (after an
 *     optional PATCH for name/description).
 */
import { useEffect, useMemo, useState } from "react"
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

import { useImportCapabilityVersionMutation } from "./api"
import { ImportMCPForm } from "./ImportMCPForm"
import { ImportSkillForm } from "./ImportSkillForm"
import { ImportPluginForm, type PluginUploadState } from "./ImportPluginForm"
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

  const kind = capability.type as CanonicalKind

  const [name, setName] = useState(capability.name)
  const [description, setDescription] = useState(capability.description ?? "")
  const [version, setVersion] = useState("")
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

  const prefill = usePrefillFromLatest(latestVersion)
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
    setVersion("")
    setSpec(null)
    setInlineSecrets([])
    setRawText(prefill.rawText)
    setSourceFormat(prefill.format)
    setPluginUpload({ ossKey: null, uploadSource: null, validation: null })
    setSkillOssKey(null)
    commitMut.reset()
    updateMut.reset()
    // intentionally only on the open transition
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const errMsg = commitMut.error instanceof ApiError
    ? commitMut.error.envelope.message
    : commitMut.error instanceof Error
      ? commitMut.error.message
      : updateMut.error instanceof ApiError
        ? updateMut.error.envelope.message
        : updateMut.error instanceof Error
          ? updateMut.error.message
          : null

  const trimmedName = name.trim()
  const trimmedVersion = version.trim()
  // Version-uniqueness guard: the user is always required to bump the version
  // even when only name/description changed (see plan: "永远要求填新版本号").
  const versionConflict =
    !!trimmedVersion && !!latestVersion && trimmedVersion === latestVersion.version
  const nameError =
    !trimmedName
      ? t("capabilities.errors.nameRequired")
      : trimmedName.length > 50
        ? t("capabilities.errors.nameTooLong")
        : null
  const versionError = !trimmedVersion
    ? t("capabilities.errors.versionRequired", { defaultValue: "请填写新版本号" })
    : versionConflict
      ? t("capabilities.errors.versionMustBump", {
          version: latestVersion?.version ?? "",
          defaultValue: "新版本号必须不同于当前最新版本（{{version}}）",
        })
      : null

  // For plugin / skill-zip kinds we accept "no new upload" and let the server
  // reuse the previous OSS blob. So the canSubmit guard relaxes when an
  // inherited blob exists.
  const pluginHasUsableArtifact =
    kind !== "plugin"
      ? true
      : !!pluginUpload.ossKey
        ? pluginUpload.validation?.valid ?? false
        : !!inheritedOssLabel

  const skillSpecReady =
    kind !== "skill"
      ? true
      : !!skillOssKey || !!inheritedOssLabel || (!!spec && isImportSpecReady(kind, spec, inlineSecrets))

  const mcpSpecReady = kind !== "mcp" ? true : !!spec && isImportSpecReady(kind, spec, inlineSecrets)

  const canSubmit =
    !commitMut.isPending &&
    !updateMut.isPending &&
    !!workspaceID &&
    !nameError &&
    !versionError &&
    pluginHasUsableArtifact &&
    skillSpecReady &&
    mcpSpecReady

  const submit = async () => {
    if (!canSubmit) return
    // PATCH name/description only when they actually changed.
    const nextDesc = description.trim()
    const nameChanged = trimmedName !== capability.name
    const descChanged = nextDesc !== (capability.description ?? "").trim()
    try {
      if (nameChanged || descChanged) {
        await updateMut.mutateAsync({
          capabilityID: capability.id,
          body: {
            name: nameChanged ? trimmedName : undefined,
            description: descChanged ? nextDesc : undefined,
          },
        })
      }
    } catch {
      // updateMut.error surfaces via errMsg; abort the version commit.
      return
    }
    // For plugin / skill-zip without a new upload, the server falls back to
    // the previous version's oss_key. We still need a canonical_spec on the
    // wire (kind at minimum) so the backend's spec.Kind check passes.
    const fallbackSpec: CanonicalSpec | null = spec
      ? spec
      : kind === "plugin"
        ? ({ kind: "plugin" } as unknown as CanonicalSpec)
        : kind === "skill"
          ? ({ kind: "skill" } as unknown as CanonicalSpec)
          : null
    if (!fallbackSpec) return

    const ossKeyToSend =
      kind === "plugin"
        ? pluginUpload.ossKey ?? undefined
        : kind === "skill"
          ? skillOssKey ?? undefined
          : undefined
    const uploadSourceToSend =
      kind === "plugin"
        ? pluginUpload.uploadSource ?? undefined
        : kind === "skill" && skillOssKey
          ? "zip"
          : undefined

    const payload: ImportCapabilityVersionCommitRequest = {
      version: trimmedVersion,
      canonical_spec: fallbackSpec,
      inline_secrets: kind === "plugin" || inlineSecrets.length === 0 ? undefined : inlineSecrets,
      source_payload: rawText
        ? { raw_text: rawText, source_format: sourceFormat }
        : undefined,
      // omit oss_key on plugin/skill-zip reuse — backend treats missing key
      // as "carry forward the previous version's blob".
      oss_key: ossKeyToSend,
      upload_source: uploadSourceToSend,
    }
    commitMut.mutate(payload, {
      onSuccess: () => {
        onOpenChange(false)
        onCommitted()
      },
    })
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
          if (commitMut.isPending || updateMut.isPending) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>
            {t("capabilities.versions.add.title", { name: capability.name })}
          </DialogTitle>
          <DialogDescription>
            {t("capabilities.versions.add.description")}
          </DialogDescription>
        </DialogHeader>

        {prefill.didPrefill && (
          <InfoBanner>
            {t("capabilities.versions.add.prefillFromLatest", {
              version: latestVersion?.version ?? "",
              defaultValue: "已用上一版（{{version}}）的内容预填。修改后会作为新版本提交。",
            })}
          </InfoBanner>
        )}
        {inheritedOssLabel && (kind === "plugin" || kind === "skill") && (
          <InfoBanner>
            {t("capabilities.versions.add.reuseExistingZip", {
              filename: inheritedOssLabel,
              defaultValue: "当前版本的包：{{filename}}。如不重新上传，新版本将复用此包。",
            })}
          </InfoBanner>
        )}
        {inheritedInlineSecrets.length > 0 && (
          <WarningBanner>
            {t("capabilities.versions.add.inlineSecretLostWarning", {
              keys: inheritedInlineSecrets
                .map((e) => `${e.server}.${e.envKey}`)
                .join(", "),
              defaultValue: "上一版的 inline secret（{{keys}}）已隐藏。如需保留，请重新输入明文；或改成已托管的凭据。",
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
          <Field label={t("capabilities.fields.version.label")} required>
            <Input
              value={version}
              onChange={(e) => setVersion(e.target.value)}
              placeholder={
                latestVersion?.version
                  ? t("capabilities.fields.version.placeholderBump", {
                      version: latestVersion.version,
                      defaultValue: "> {{version}}",
                    })
                  : "1.0.3"
              }
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

        {(nameError || versionError) && (
          <div
            role="alert"
            className="mt-2 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
          >
            {nameError ?? versionError}
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
              value={spec}
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
            disabled={commitMut.isPending || updateMut.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("capabilities.actions.cancel")}
          </Button>
          <Button size="sm" disabled={!canSubmit} onClick={submit}>
            {(commitMut.isPending || updateMut.isPending) && (
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
      | { raw_text?: string; source_format?: string; format?: string; body?: string }
      | undefined
    // Accepts two source_payload shapes for forward-compat:
    //   { raw_text, source_format } — new dialog
    //   { format, body }            — early server code
    const rawText = sp?.raw_text ?? sp?.body ?? ""
    const fmtRaw = (sp?.source_format ?? sp?.format ?? "").toLowerCase()
    const valid: SourceFormat[] = ["json", "toml", "markdown"]
    const format = (valid as string[]).includes(fmtRaw)
      ? (fmtRaw as SourceFormat)
      : "json"
    return { rawText, format, didPrefill: rawText.length > 0 }
  }, [latestVersion])
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
