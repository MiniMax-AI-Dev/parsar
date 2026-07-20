import { useState } from "react"
import { Loader2, MessageSquare, Pencil, Trash2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import { useDeleteAgent, useUpdateAgent, useUpdateAgentProfile } from "../../../lib/api-agents"
import { createAgentConversation } from "../../../lib/api-conversations"
import type { Agent, Model, UserWorkspace } from "../../../lib/api-types"
import { useAdminView } from "../../../lib/admin-router"
import { CreateAgentDialog } from "../CreateAgentDialog"
import { DeleteAgentDialog } from "./DeleteAgentDialog"

export function AgentDetailActions({
  agent,
  workspaceID,
  workspaceName,
  workspaceRole,
  models,
  onToast,
}: {
  agent: Agent
  workspaceID: string | null
  workspaceName?: string
  workspaceRole?: UserWorkspace["role"]
  models: Model[]
  onToast: (message: string) => void
}) {
  const { t, i18n } = useTranslation("admin")
  const { navigate } = useAdminView()
  const [editOpen, setEditOpen] = useState(false)
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [chatPending, setChatPending] = useState(false)
  const updateMut = useUpdateAgent(workspaceID)
  const updateProfileMut = useUpdateAgentProfile(workspaceID)
  const deleteMut = useDeleteAgent(workspaceID)

  async function startChat() {
    if (!workspaceID || chatPending) return
    setChatPending(true)
    try {
      const conversation = await createAgentConversation(workspaceID, agent, i18n.language)
      navigate("conversations", { id: conversation.id, focus: "compose" })
    } finally {
      setChatPending(false)
    }
  }

  return (
    <>
      <Button
        variant="outline"
        size="sm"
        disabled={agent.status !== "active" || chatPending}
        onClick={() => void startChat()}
      >
        {chatPending ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : (
          <MessageSquare className="h-3.5 w-3.5" />
        )}
        {t("agents.actions.chat")}
      </Button>
      <Button size="sm" onClick={() => setEditOpen(true)}>
        <Pencil className="h-3.5 w-3.5" />
        {t("agents.actions.edit")}
      </Button>
      <Button
        variant="outline"
        size="sm"
        disabled={deleteMut.isPending}
        onClick={() => setDeleteOpen(true)}
      >
        {deleteMut.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Trash2 className="h-3.5 w-3.5" />}
        {t("agents.actions.delete")}
      </Button>

      <CreateAgentDialog
        open={editOpen}
        mode="edit"
        workspaceID={workspaceID}
        workspaceName={workspaceName}
        workspaceRole={workspaceRole}
        models={models}
        agent={agent}
        pending={updateMut.isPending || updateProfileMut.isPending}
        error={updateMut.error ?? updateProfileMut.error}
        onOpenChange={(open) => {
          setEditOpen(open)
          if (!open) {
            updateMut.reset()
            updateProfileMut.reset()
          }
        }}
        onSubmit={({ agentID, body, agentProfile }) => {
          if (!agentID) return
          void (async () => {
            try {
              await updateMut.mutateAsync({ agentID, body })
              if (agentProfile) await updateProfileMut.mutateAsync({ agentID, body: agentProfile })
              setEditOpen(false)
              onToast(t("agents.detail.config.saved"))
            } catch {
              return
            }
          })()
        }}
      />

      <DeleteAgentDialog
        agent={deleteOpen ? agent : null}
        pending={deleteMut.isPending}
        error={deleteMut.error}
        onCancel={() => {
          setDeleteOpen(false)
          deleteMut.reset()
        }}
        onConfirm={() => {
          const name = agent.name
          deleteMut.mutate(agent.id, {
            onSuccess: () => {
              setDeleteOpen(false)
              onToast(t("agents.delete.deletedToast", { name }))
              navigate("agents")
            },
          })
        }}
      />
    </>
  )
}
