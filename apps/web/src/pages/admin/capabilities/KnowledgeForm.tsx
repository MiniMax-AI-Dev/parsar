import { useId, useRef, useState } from "react"
import { FilePlus2, Loader2, Trash2, Upload } from "lucide-react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Input } from "../../../components/ui/input"
import { Textarea } from "../../../components/ui/textarea"
import { Field } from "../../../components/ui/label"
import { knowledgeBytes, knowledgeError, MAX_KNOWLEDGE_BYTES, MAX_KNOWLEDGE_DOCUMENTS, type KnowledgeSpec } from "../../../lib/knowledge"
import { InlineNotice } from "./notices"

interface Props {
  value?: KnowledgeSpec
  onChange: (value: KnowledgeSpec) => void
}

export function KnowledgeForm({ value, onChange }: Props) {
  const { t } = useTranslation("admin")
  const id = useId()
  const upload = useRef<HTMLInputElement>(null)
  const [reading, setReading] = useState(false)
  const [uploadError, setUploadError] = useState<string | null>(null)
  const documents = value?.documents ?? [{ name: "reference.md", content: "" }]
  const validation = value ? knowledgeError(value) : null
  const change = (index: number, patch: Partial<typeof documents[number]>) => {
    setUploadError(null)
    onChange({ documents: documents.map((doc, i) => i === index ? { ...doc, ...patch } : doc) })
  }
  const readFiles = async (files: File[]) => {
    if (!files.length) return
    setReading(true)
    setUploadError(null)
    try {
      const existing = documents.filter((doc) => doc.content.trim() || doc.name !== "reference.md")
      if (files.some((file) => !/\.(md|markdown|txt)$/i.test(file.name))) throw new Error(t("capabilities.knowledge.errors.format"))
      if (existing.length + files.length > MAX_KNOWLEDGE_DOCUMENTS || files.reduce((sum, file) => sum + file.size, knowledgeBytes({ documents: existing })) > MAX_KNOWLEDGE_BYTES) throw new Error(t("capabilities.knowledge.errors.size"))
      const added = await Promise.all(files.map(async (file) => ({ name: file.name, content: new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer()) })))
      const next = { documents: [...existing, ...added] }
      const error = knowledgeError(next)
      if (error) throw new Error(t(`capabilities.knowledge.errors.${error}`))
      onChange(next)
    } catch (error) {
      setUploadError(error instanceof TypeError ? t("capabilities.knowledge.errors.encoding") : error instanceof Error ? error.message : t("capabilities.knowledge.errors.read"))
    } finally {
      setReading(false)
      if (upload.current) upload.current.value = ""
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" variant="outline" size="sm" disabled={reading} onClick={() => upload.current?.click()}>
          {reading ? <Loader2 className="animate-spin" /> : <Upload />}{t("capabilities.knowledge.upload")}
        </Button>
        <Button type="button" variant="ghost" size="sm" disabled={reading || documents.length >= MAX_KNOWLEDGE_DOCUMENTS} onClick={() => onChange({ documents: [...documents, { name: `reference-${documents.length + 1}.md`, content: "" }] })}>
          <FilePlus2 />{t("capabilities.knowledge.addDocument")}
        </Button>
        <span className="text-xs text-fg-muted">{t("capabilities.knowledge.limits")}</span>
        <input ref={upload} type="file" multiple accept=".md,.markdown,.txt" className="hidden" aria-label={t("capabilities.knowledge.upload")} onChange={(e) => void readFiles(Array.from(e.target.files ?? []))} />
      </div>
      {(uploadError || validation) && <InlineNotice tone="error">{uploadError ?? (validation ? t(`capabilities.knowledge.errors.${validation}`) : "")}</InlineNotice>}
      {documents.map((doc, index) => (
        <fieldset key={index} disabled={reading} className="min-w-0 space-y-3 rounded-lg border border-line p-3">
          <div className="flex items-end gap-2">
            <div className="min-w-0 flex-1">
              <Field label={t("capabilities.knowledge.documentName")} htmlFor={`${id}-${index}-name`}>
                <Input id={`${id}-${index}-name`} value={doc.name} onChange={(e) => change(index, { name: e.target.value })} />
              </Field>
            </div>
            {documents.length > 1 && <Button type="button" variant="ghost" size="icon" aria-label={t("capabilities.knowledge.remove", { name: doc.name })} onClick={() => onChange({ documents: documents.filter((_, i) => i !== index) })}><Trash2 /></Button>}
          </div>
          <Field label={t("capabilities.knowledge.content")} htmlFor={`${id}-${index}-content`}>
            <Textarea id={`${id}-${index}-content`} className="min-h-40 resize-y font-mono text-sm" value={doc.content} onChange={(e) => change(index, { content: e.target.value })} placeholder={t("capabilities.knowledge.placeholder")} />
          </Field>
        </fieldset>
      ))}
      <p className="text-xs text-fg-muted">{t("capabilities.knowledge.bindingHint")}</p>
    </div>
  )
}
