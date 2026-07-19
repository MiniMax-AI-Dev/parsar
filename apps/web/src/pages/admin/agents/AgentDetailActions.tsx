import { useState } from "react"
import { Loader2, MessageSquare, Pencil, Power, PowerOff } from "lucide-react"
import { useTranslation } from "react-i18next"

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { Button } from "../../../components/ui/button"
import { useSetAgentStatus, useUpdateAgent, useUpdateAgentProfile } from "../../../lib/api-agents"
import { createConversation } from "../../../lib/api-conversations"
import type { Agent, Model, UserWorkspace } from "../../../lib/api-types"
import { useAdminView } from "../../../lib/admin-router"
import { CreateAgentDialog } from "../CreateAgentDialog"

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
  const [disableOpen, setDisableOpen] = useState(false)
  const [chatPending, setChatPending] = useState(false)
  const updateMut = useUpdateAgent(workspaceID)
  const updateProfileMut = useUpdateAgentProfile(workspaceID)
  const statusMut = useSetAgentStatus(workspaceID)

  async function startChat() {
    if (!workspaceID || chatPending) return
    setChatPending(true)
    try {
      const conversation = await createConversation(workspaceID, {
        title: conversationTitle(agent.name, i18n.language),
        surface: "web",
        form: "thread",
        agent_id: agent.id,
      })
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
        disabled={statusMut.isPending}
        onClick={() => {
          if (agent.status === "active") {
            setDisableOpen(true)
            return
          }
          statusMut.mutate(
            { agentID: agent.id, enabled: true },
            {
              onSuccess: () => onToast(t("agents.listActions.enabledToast", { name: agent.name })),
            },
          )
        }}
      >
        {agent.status === "active" ? (
          <PowerOff className="h-3.5 w-3.5" />
        ) : (
          <Power className="h-3.5 w-3.5" />
        )}
        {t(agent.status === "active" ? "agents.actions.disable" : "agents.actions.enable")}
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

      <AlertDialog
        open={disableOpen}
        onOpenChange={(open) => {
          if (!statusMut.isPending) setDisableOpen(open)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("agents.listActions.disableConfirmTitle", { name: agent.name })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t("agents.listActions.disableConfirmDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel asChild>
              <Button variant="outline" size="sm" disabled={statusMut.isPending}>
                {t("agents.listActions.cancel")}
              </Button>
            </AlertDialogCancel>
            <Button
              variant="destructive"
              size="sm"
              disabled={statusMut.isPending}
              onClick={() =>
                statusMut.mutate(
                  { agentID: agent.id, enabled: false },
                  {
                    onSuccess: () => {
                      setDisableOpen(false)
                      onToast(t("agents.listActions.disabledToast", { name: agent.name }))
                    },
                  },
                )
              }
            >
              {statusMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              {t("agents.listActions.disableConfirm")}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}

function conversationTitle(agentName: string, language: string): string {
  const name = agentName.trim()
  if (!name) return ""
  return language.startsWith("zh") ? `和 ${name} 对话` : `Chat with ${name}`
}
