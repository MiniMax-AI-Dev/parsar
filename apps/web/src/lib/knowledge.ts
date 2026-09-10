export interface KnowledgeDocument {
  name: string
  content: string
}

export interface KnowledgeSpec {
  documents: KnowledgeDocument[]
}

export const MAX_KNOWLEDGE_BYTES = 32 * 1024
export const MAX_KNOWLEDGE_DOCUMENTS = 16

export function knowledgeBytes(value: KnowledgeSpec): number {
  return value.documents.reduce((total, doc) => total + new TextEncoder().encode(doc.name + doc.content).length, 0)
}

// Codes are localized by the form. Match the canonical document limits.
export function knowledgeError(value: KnowledgeSpec): "required" | "size" | "duplicate" | "name" | "encoding" | null {
  if (!value.documents.length || value.documents.some((doc) => !doc.content.trim())) return "required"
  if (value.documents.length > MAX_KNOWLEDGE_DOCUMENTS || knowledgeBytes(value) > MAX_KNOWLEDGE_BYTES) return "size"
  if (value.documents.some((doc) => !doc.name.trim() || new TextEncoder().encode(doc.name.trim()).length > 255 || /[\r\n\0]/.test(doc.name))) return "name"
  if (value.documents.some((doc) => doc.content.includes("\0"))) return "encoding"
  if (new Set(value.documents.map((doc) => doc.name.trim())).size !== value.documents.length) return "duplicate"
  return null
}
