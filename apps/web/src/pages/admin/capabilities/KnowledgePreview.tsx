import { useTranslation } from "react-i18next"
import type { KnowledgeSpec } from "../../../lib/knowledge"
import { MessageMarkdown } from "../../../components/conversation/MessageMarkdown"

export function KnowledgePreview({ value }: { value?: KnowledgeSpec }) {
  const { t } = useTranslation("admin")
  return <div className="space-y-3">
    <p className="text-sm text-fg-muted">{t("capabilities.knowledge.previewHint")}</p>
    {value?.documents.map((doc) => <section key={doc.name} className="min-w-0 overflow-hidden rounded-lg border border-line">
      <h3 className="border-b border-line bg-surface-muted px-4 py-2 text-sm font-medium break-words">{doc.name}</h3>
      <div className="max-h-96 overflow-auto p-4"><MessageMarkdown content={doc.content} /></div>
    </section>)}
  </div>
}
