/**
 * Top-level dialog shell. Per-kind subforms (ImportMCPForm /
 * ImportSkillForm / ImportPluginForm) are controlled children; this
 * dialog owns the draft and POSTs /import/commit.
 *
 * Contract: the Name input is NEVER auto-overwritten by re-parses.
 * suggested_name applies only while nameTouched is false.
 */
import { useEffect, useRef, useState } from "react"
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../../../components/ui/tabs"
import { ApiError } from "../../../lib/api-client"

import { useImportCommitMutation } from "./api"
import { ImportMCPForm } from "./ImportMCPForm"
import { ImportSkillForm } from "./ImportSkillForm"
import { ImportPluginForm, type PluginUploadState } from "./ImportPluginForm"
import { ImportSystemPromptForm, type SystemPromptDraft } from "./ImportSystemPromptForm"
import { isImportSpecReady } from "./importValidation"
import { systemPromptCapabilityPayload } from "../../../lib/api-capabilities"
import { useCreateCapability } from "../../../lib/api-capabilities"
import type {
  CanonicalKind,
  CanonicalSpec,
  ImportCommitRequest,
  ImportInlineSecretInput,
  SourceFormat,
} from "./types"

interface Props {
  workspaceID: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Optional callback after a successful import — the page can navigate to
   *  the new capability detail view, etc. */
  onCreated?: (capabilityID: string) => void
}

export function ImportCapabilityDialog({
  workspaceID,
  open,
  onOpenChange,
  onCreated,
}: Props) {
  const { t } = useTranslation("admin")
  const commitMut = useImportCommitMutation(workspaceID)
  const createMut = useCreateCapability(workspaceID)

  // Draft is preserved across tab flips, but the spec itself is dropped
  // on kind change (an MCP spec is meaningless as a Skill spec).
  const [kind, setKind] = useState<CanonicalKind>("mcp")
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [spec, setSpec] = useState<CanonicalSpec | null>(null)
  const [inlineSecrets, setInlineSecrets] = useState<ImportInlineSecretInput[]>([])
  const [rawText, setRawText] = useState("")
  const [sourceFormat, setSourceFormat] = useState<SourceFormat>("json")
  /** Plugin-only: ossKey + validation result. Tracked separately
   *  because plugin's commit payload differs from mcp/skill — it
   *  needs to reference an OSS object rather than carry a spec body. */
  const [pluginUpload, setPluginUpload] = useState<PluginUploadState>({
    ossKey: null,
    uploadSource: null,
    validation: null,
  })
  /** Skill-only: ossKey of an uploaded zip (null when paste mode or
   *  when the user hasn't picked a zip yet). Threaded into the commit
   *  payload so the server can re-fetch + re-parse the same bytes. */
  const [skillOssKey, setSkillOssKey] = useState<string | null>(null)
  /** system_prompt-only: prompt body + append/override + version label. */
  const [systemPromptDraft, setSystemPromptDraft] = useState<SystemPromptDraft>({
    prompt: "",
    mode: "append",
    version: "1.0.0",
  })

  /** Tracks whether the user typed in the Name input. Once true we stop
   *  letting the preview's suggested_name overwrite their value.
   *  Skill imports ignore this flag entirely — for Skill, the frontmatter
   *  is the single source of truth and there is no Name input on screen. */
  const nameTouched = useRef(false)

  // Reset everything when the dialog opens (or closes-then-reopens) so a
  // previous run doesn't bleed in.
  useEffect(() => {
    if (!open) return
    setKind("mcp")
    setName("")
    setDescription("")
    setSpec(null)
    setInlineSecrets([])
    setRawText("")
    setSourceFormat("json")
    setPluginUpload({ ossKey: null, uploadSource: null, validation: null })
    setSkillOssKey(null)
    setSystemPromptDraft({ prompt: "", mode: "append", version: "1.0.0" })
    nameTouched.current = false
    commitMut.reset()
    createMut.reset()
    // intentionally only on the open transition
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const onTabChange = (next: string) => {
    const nextKind = next as CanonicalKind
    if (nextKind === kind) return
    setKind(nextKind)
    // Cross-kind drafts don't make sense — drop the parsed spec and
    // secrets so the new tab starts clean.
    setSpec(null)
    setInlineSecrets([])
    setRawText("")
    setPluginUpload({ ossKey: null, uploadSource: null, validation: null })
    setSkillOssKey(null)
    setSystemPromptDraft({ prompt: "", mode: "append", version: "1.0.0" })
    setSourceFormat(nextKind === "skill" ? "markdown" : "json")
    // For Skill, frontmatter is the source of truth — reset name/desc
    // on a fresh tab. MCP/Plugin keep user input across tab flips.
    if (nextKind === "skill") {
      nameTouched.current = false
      setName("")
      setDescription("")
    }
    commitMut.reset()
  }

  const onSuggestedName = (suggested: string) => {
    // Skill always overrides (frontmatter is the truth). MCP/Plugin
    // only fill on first preview to avoid clobbering user edits.
    if (kind !== "skill" && nameTouched.current) return
    if (!suggested) return
    setName(suggested)
  }

  const onSuggestedDescription = (suggested: string) => {
    // Only Skill imports get auto-filled description (the field is
    // hidden in the UI; this keeps capability.description in sync with
    // frontmatter.description).
    if (kind !== "skill") return
    setDescription(suggested)
  }

  const errMsg = commitMut.error instanceof ApiError
    ? commitMut.error.envelope.message
    : commitMut.error instanceof Error
      ? commitMut.error.message
      : createMut.error instanceof ApiError
        ? createMut.error.envelope.message
        : createMut.error instanceof Error
          ? createMut.error.message
          : null

  const canSubmit =
    !commitMut.isPending &&
    !createMut.isPending &&
    !!workspaceID &&
    name.trim().length > 0 &&
    (kind === "system_prompt"
      ? systemPromptDraft.prompt.trim().length > 0 && systemPromptDraft.version.trim().length > 0
      : !!spec &&
        (kind === "plugin"
          ? !!pluginUpload.ossKey && (pluginUpload.validation?.valid ?? false)
          : isImportSpecReady(kind, spec, inlineSecrets)))

  const submit = () => {
    if (!canSubmit) return
    if (kind === "system_prompt") {
      createMut.mutate(
        systemPromptCapabilityPayload({
          name: name.trim(),
          description: description.trim(),
          version: systemPromptDraft.version.trim(),
          prompt: systemPromptDraft.prompt,
          mode: systemPromptDraft.mode,
        }),
        {
          onSuccess: (cap) => {
            onOpenChange(false)
            onCreated?.(cap.id)
          },
        },
      )
      return
    }
    if (!spec) return
    const payload: ImportCommitRequest = {
      kind,
      name: name.trim(),
      description: description.trim() || undefined,
      canonical_spec: spec,
      inline_secrets: kind === "plugin" || inlineSecrets.length === 0 ? undefined : inlineSecrets,
      source_payload: rawText
        ? { raw_text: rawText, source_format: sourceFormat }
        : undefined,
      // Plugin commits: server rebuilds canonical_spec from oss_key +
      // upload_source, ignoring the client spec body.
      // Skill zip commits: server uses oss_key to re-fetch the zip and
      // re-parse files[] into canonical_spec.skill.files.
      oss_key:
        kind === "plugin"
          ? pluginUpload.ossKey ?? undefined
          : kind === "skill"
            ? skillOssKey ?? undefined
            : undefined,
      upload_source:
        kind === "plugin"
          ? pluginUpload.uploadSource ?? undefined
          : kind === "skill" && skillOssKey
            ? "zip"
            : undefined,
    }
    commitMut.mutate(payload, {
      onSuccess: (res) => {
        onOpenChange(false)
        onCreated?.(res.capability.id)
      },
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className="max-h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-6xl overflow-x-hidden overflow-y-auto"
        // Keep typing in the textareas from triggering submit on Enter etc.
        onInteractOutside={(e) => {
          if (commitMut.isPending || createMut.isPending) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>
            {t("capabilities.import.dialog.title", "导入能力")}
          </DialogTitle>
          <DialogDescription>
            {kind === "skill"
              ? t(
                  "capabilities.import.dialog.descriptionSkill",
                  "粘贴 SKILL.md 或上传 zip 包。Skill 的名称和描述由 frontmatter 决定,正文会作为 instruction 注入模型。",
                )
              : kind === "plugin"
                ? t(
                    "capabilities.import.dialog.descriptionPlugin",
                    "上传 .claude-plugin 打包好的 zip,服务端会校验 manifest 并解析。",
                  )
                : kind === "system_prompt"
                  ? t(
                      "capabilities.import.dialog.descriptionSystemPrompt",
                      "把一段 system prompt 注册成可复用的能力。Append 模式拼在用户 system prompt 前;Override 模式完全替换。",
                    )
                  : t(
                      "capabilities.import.dialog.description",
                      "粘贴第三方 MCP 配置(JSON / TOML),会自动解析并预览。普通 env 值原样导入;只有 env 值以 $ 开头时才会提示凭据处理。",
                    )}
          </DialogDescription>
        </DialogHeader>

        <Tabs value={kind} onValueChange={onTabChange}>
          <TabsList>
            <TabsTrigger value="mcp">
              {t("capabilities.import.tab.mcp", "MCP")}
            </TabsTrigger>
            <TabsTrigger value="skill">
              {t("capabilities.import.tab.skill", "Skill")}
            </TabsTrigger>
            <TabsTrigger value="plugin">
              {t("capabilities.import.tab.plugin", "Plugin")}
            </TabsTrigger>
            <TabsTrigger value="system_prompt">
              {t("capabilities.import.tab.systemPrompt", "System Prompt")}
            </TabsTrigger>
          </TabsList>

          {/* ---- shared meta fields (MCP/Plugin only) ----------------
              Skill imports derive name + description from frontmatter;
              showing manual inputs would let the form value drift from
              the source-of-truth markdown and silently overwrite it. */}
          {kind !== "skill" && (
            <div className="mt-4 grid gap-3 md:grid-cols-2">
              <Field
                label={t("capabilities.import.dialog.name", "名称")}
                required
              >
                <Input
                  value={name}
                  onChange={(e) => {
                    nameTouched.current = true
                    setName(e.target.value)
                  }}
                  placeholder={t(
                    "capabilities.import.dialog.namePlaceholder",
                    "如 github-mcp",
                  )}
                />
              </Field>
              <Field
                label={t("capabilities.import.dialog.descriptionLabel", "描述")}
              >
                <Input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder={t(
                    "capabilities.import.dialog.descriptionPlaceholder",
                    "用一句话说明这个能力做什么,Claude 会用它判断是否调用",
                  )}
                />
              </Field>
            </div>
          )}

          {/* ---- per-kind paste + preview surface --------------------- */}
          <TabsContent value="mcp" className="mt-3">
            {kind === "mcp" && (
              <ImportMCPForm
                workspaceID={workspaceID}
                value={spec}
                onChange={setSpec}
                inlineSecrets={inlineSecrets}
                onInlineSecretsChange={setInlineSecrets}
                onSuggestedName={onSuggestedName}
                onRawTextChange={(raw, fmt) => {
                  setRawText(raw)
                  setSourceFormat(fmt)
                }}
              />
            )}
          </TabsContent>
          <TabsContent value="skill" className="mt-3">
            {kind === "skill" && (
              <ImportSkillForm
                workspaceID={workspaceID}
                value={spec}
                onChange={setSpec}
                onSuggestedName={onSuggestedName}
                onSuggestedDescription={onSuggestedDescription}
                onRawTextChange={(raw, fmt) => {
                  setRawText(raw)
                  setSourceFormat(fmt)
                }}
                onOssKeyChange={setSkillOssKey}
              />
            )}
          </TabsContent>
          <TabsContent value="plugin" className="mt-3">
            {kind === "plugin" && (
              <ImportPluginForm
                workspaceID={workspaceID}
                value={spec}
                onChange={setSpec}
                onUploadStateChange={setPluginUpload}
                onSuggestedName={onSuggestedName}
              />
            )}
          </TabsContent>
          <TabsContent value="system_prompt" className="mt-3">
            {kind === "system_prompt" && (
              <ImportSystemPromptForm
                value={systemPromptDraft}
                onChange={setSystemPromptDraft}
              />
            )}
          </TabsContent>
        </Tabs>

        {errMsg && (
          <div
            role="alert"
            className="break-all rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[13px] text-red-800"
          >
            {errMsg}
          </div>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            size="sm"
            disabled={commitMut.isPending || createMut.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("capabilities.actions.cancel", "取消")}
          </Button>
          <Button size="sm" disabled={!canSubmit} onClick={submit}>
            {(commitMut.isPending || createMut.isPending) && (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            )}
            {t("capabilities.import.dialog.submit", "导入")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
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
      <span className="text-[13px] font-medium text-slate-700">
        {label}
        {required && <span className="text-red-500"> *</span>}
      </span>
      {children}
    </label>
  )
}
