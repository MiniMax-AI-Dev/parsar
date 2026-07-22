/**
 * Top-level dialog shell. ImportMCPForm and ImportSkillForm are controlled
 * children; this dialog owns the draft and POSTs /import/commit.
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
import { isImportSpecReady } from "./importValidation"
import type {
  CanonicalSpec,
  ImportCommitRequest,
  ImportInlineSecretInput,
  SourceFormat,
} from "./types"

interface Props {
  workspaceID: string | null
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Members may import Skills but cannot create MCP capabilities. */
  skillOnly?: boolean
  /** Optional callback after a successful import — the page can navigate to
   *  the new capability detail view, etc. */
  onCreated?: (capabilityID: string) => void
}

type AddCapabilityKind = "mcp" | "skill"

export function ImportCapabilityDialog({
  workspaceID,
  open,
  onOpenChange,
  skillOnly = false,
  onCreated,
}: Props) {
  const { t } = useTranslation("admin")
  const commitMut = useImportCommitMutation(workspaceID)

  // Draft is preserved across tab flips, but the spec itself is dropped
  // on kind change (an MCP spec is meaningless as a Skill spec).
  const [kind, setKind] = useState<AddCapabilityKind>(skillOnly ? "skill" : "mcp")
  const [name, setName] = useState("")
  const [description, setDescription] = useState("")
  const [spec, setSpec] = useState<CanonicalSpec | null>(null)
  const [inlineSecrets, setInlineSecrets] = useState<ImportInlineSecretInput[]>([])
  const [rawText, setRawText] = useState("")
  const [sourceFormat, setSourceFormat] = useState<SourceFormat>("json")
  /** Skill-only: ossKey of an uploaded zip (null when paste mode or
   *  when the user hasn't picked a zip yet). Threaded into the commit
   *  payload so the server can re-fetch + re-parse the same bytes. */
  const [skillOssKey, setSkillOssKey] = useState<string | null>(null)

  /** Tracks whether the user typed in the Name input. Once true we stop
   *  letting the preview's suggested_name overwrite their value.
   *  Skill imports ignore this flag entirely — for Skill, the frontmatter
   *  is the single source of truth and there is no Name input on screen. */
  const nameTouched = useRef(false)

  // Reset everything when the dialog opens (or closes-then-reopens) so a
  // previous run doesn't bleed in.
  useEffect(() => {
    if (!open) return
    setKind(skillOnly ? "skill" : "mcp")
    setName("")
    setDescription("")
    setSpec(null)
    setInlineSecrets([])
    setRawText("")
    setSourceFormat(skillOnly ? "markdown" : "json")
    setSkillOssKey(null)
    nameTouched.current = false
    commitMut.reset()
    // intentionally only on the open transition
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const onTabChange = (next: string) => {
    const nextKind = next as AddCapabilityKind
    if (skillOnly && nextKind !== "skill") return
    if (nextKind === kind) return
    setKind(nextKind)
    // Cross-kind drafts don't make sense — drop the parsed spec and
    // secrets so the new tab starts clean.
    setSpec(null)
    setInlineSecrets([])
    setRawText("")
    setSkillOssKey(null)
    setSourceFormat(nextKind === "skill" ? "markdown" : "json")
    // For Skill, frontmatter is the source of truth — reset name/desc
    // on a fresh tab. MCP keeps user input across tab flips.
    if (nextKind === "skill") {
      nameTouched.current = false
      setName("")
      setDescription("")
    }
    commitMut.reset()
  }

  const onSuggestedName = (suggested: string) => {
    // Skill always overrides (frontmatter is the truth). MCP only fills on
    // first preview to avoid clobbering user edits.
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
      : null

  const canSubmit =
    !commitMut.isPending &&
    !!workspaceID &&
    name.trim().length > 0 &&
    !!spec &&
    isImportSpecReady(kind, spec, inlineSecrets)

  const submit = () => {
    if (!canSubmit) return
    if (!spec) return
    const payload: ImportCommitRequest = {
      kind,
      name: name.trim(),
      description: description.trim() || undefined,
      canonical_spec: spec,
      inline_secrets: inlineSecrets.length === 0 ? undefined : inlineSecrets,
      source_payload: rawText
        ? { raw_text: rawText, source_format: sourceFormat }
        : undefined,
      // Skill zip commits: server uses oss_key to re-fetch the zip and
      // re-parse files[] into canonical_spec.skill.files.
      oss_key: kind === "skill" ? skillOssKey ?? undefined : undefined,
      upload_source: kind === "skill" && skillOssKey ? "zip" : undefined,
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
          if (commitMut.isPending) e.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>
            {t("capabilities.import.dialog.title", "Import capability")}
          </DialogTitle>
          <DialogDescription>
            {kind === "skill"
              ? t(
                  "capabilities.import.dialog.descriptionSkill",
                  "Paste a SKILL.md or upload a zip. The Skill name and description come from the frontmatter; the body is injected into the model as the instruction.",
                )
              : t(
                  "capabilities.import.dialog.description",
                  "Paste a third-party MCP config (JSON / TOML); we parse and preview it. Plain env values are imported as-is; only env values starting with $ trigger the credential prompt.",
                )}
          </DialogDescription>
        </DialogHeader>

        <Tabs value={kind} onValueChange={onTabChange}>
          {!skillOnly && (
            <TabsList>
              <TabsTrigger value="mcp">
                {t("capabilities.import.tab.mcp", "MCP")}
              </TabsTrigger>
              <TabsTrigger value="skill">
                {t("capabilities.import.tab.skill", "Skill")}
              </TabsTrigger>
            </TabsList>
          )}

          {/* ---- shared meta fields (MCP only) -----------------------
              Skill imports derive name + description from frontmatter;
              showing manual inputs would let the form value drift from
              the source-of-truth markdown and silently overwrite it. */}
          {kind !== "skill" && (
            <div className="mt-4 grid gap-3 md:grid-cols-2">
              <Field
                label={t("capabilities.import.dialog.name", "Name")}
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
                    "e.g. github-mcp",
                  )}
                />
              </Field>
              <Field
                label={t("capabilities.import.dialog.descriptionLabel", "Description")}
              >
                <Input
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder={t(
                    "capabilities.import.dialog.descriptionPlaceholder",
                    "One sentence describing what this capability does — Claude uses it to decide when to invoke.",
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
        </Tabs>

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
            disabled={commitMut.isPending}
            onClick={() => onOpenChange(false)}
          >
            {t("capabilities.actions.cancel", "Cancel")}
          </Button>
          <Button size="sm" disabled={!canSubmit} onClick={submit}>
            {commitMut.isPending && (
              <Loader2 className="h-3.5 w-3.5 animate-spin" />
            )}
            {t("capabilities.import.dialog.submit", "Import")}
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
      <span className="text-sm font-medium text-fg-muted">
        {label}
        {required && <span className="text-danger"> *</span>}
      </span>
      {children}
    </label>
  )
}
