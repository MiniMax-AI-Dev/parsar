import { useState } from "react"
import { CreateAgentDialog as ManagedAgentDialog, type CreateAgentDialogProps, type AgentFormDraft } from "../CreateAgentDialog"
import { ExternalAgentDialog } from "./ExternalAgentDialog"

export function CreateAgentDialog(props: CreateAgentDialogProps) {
  return props.open ? <AgentDialogSession key={`${props.mode}:${props.agent?.id ?? "new"}`} {...props} /> : null
}

function AgentDialogSession(props: CreateAgentDialogProps) {
  const [external, setExternal] = useState(props.agent?.connector_type === "http")
  const [draft, setDraft] = useState<AgentFormDraft>()
  if (external) return <ExternalAgentDialog {...props} initialDraft={draft} onBack={props.mode === "create" && !props.agent ? (next) => { setDraft(next); setExternal(false) } : undefined} />
  return <ManagedAgentDialog {...props} initialDraft={draft} onSelectExternal={(next) => { setDraft(next); setExternal(true) }} />
}
