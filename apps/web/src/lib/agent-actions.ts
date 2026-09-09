import { useState } from "react"
import { useTranslation } from "react-i18next"

import { useToast } from "../components/ui/toast"
import { useAdminView } from "./admin-router"
import { ApiError } from "./api-client"
import { createAgentConversation } from "./api-conversations"
import type { Agent, UserWorkspace } from "./api-types"

export function agentActionPermissions(role?: UserWorkspace["role"]) {
  const canManage = role === "owner" || role === "admin"
  return { canManage, canChat: canManage || role === "member" }
}

export function useAgentChat(workspaceID: string | null, canChat: boolean) {
  const { t, i18n } = useTranslation("admin")
  const { navigate } = useAdminView()
  const toast = useToast()
  const [pendingID, setPendingID] = useState<string | null>(null)

  async function startChat(agent: Agent) {
    if (!workspaceID || !canChat || pendingID || agent.status !== "active") return
    setPendingID(agent.id)
    try {
      const conversation = await createAgentConversation(workspaceID, agent, i18n.language)
      navigate("conversations", { id: conversation.id, focus: "compose" })
    } catch (error) {
      const forbidden = error instanceof ApiError && error.envelope.status === 403
      toast.show(t(forbidden ? "agents.actions.chatReadOnly" : "agents.actions.chatFailed"), {
        tone: "error",
        detail: !forbidden && error instanceof Error ? error.message : undefined,
      })
    } finally {
      setPendingID(null)
    }
  }

  return { startChat, pendingID }
}
