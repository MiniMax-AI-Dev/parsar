import { useEffect, useRef } from "react"
import { useQueryClient } from "@tanstack/react-query"
import { useAdminView } from "./admin-router"
import { createConversation, sendUserMessage } from "./api-conversations"
import { writeConversationViewState } from "./conversation-view-state"

export function useNewConversation(wsId: string | null, selectedAgentId: string, entityId: string | null) {
  const qc = useQueryClient()
  const { navigate } = useAdminView()
  const firstSendPending = useRef<Promise<boolean> | null>(null)
  const firstSend = useRef<{
    workspaceId: string
    agentId: string
    conversationId?: string
  } | null>(null)

  useEffect(() => {
    firstSendPending.current = null
    firstSend.current = null
  }, [wsId, selectedAgentId, entityId])

  // "New conversation" navigates to an empty composer without pre-creating a
  // conv — the conv is created on first send via handleSendFromEmpty,
  // so the list only shows rows with a real first user turn.
  const openCreate = () => {
    firstSend.current = null
    firstSendPending.current = null
    navigate("conversations", { id: "", focus: "compose", agent: selectedAgentId })
  }

  // First-send creates the conv + posts the message + navigates in.
  // Title derives from the first 30 chars so the list gets a
  // meaningful name immediately (server defaults to "Untitled conversation").
  const handleSendFromEmpty = (content: string): Promise<boolean> => {
    if (firstSendPending.current) return firstSendPending.current
    const pending = sendFromEmpty(content).finally(() => { if (firstSendPending.current === pending) firstSendPending.current = null })
    firstSendPending.current = pending
    return pending
  }
  const sendFromEmpty = async (content: string): Promise<boolean> => {
    if (!wsId || !selectedAgentId) {
      throw new Error("workspace_id and agent_id required for empty-state send")
    }
    let attempt = firstSend.current
    if (!attempt || attempt.workspaceId !== wsId || attempt.agentId !== selectedAgentId) {
      attempt = { workspaceId: wsId, agentId: selectedAgentId }
      firstSend.current = attempt
    }
    let cid = attempt.conversationId
    if (!cid) {
      const conv = await createConversation(wsId, {
        title: content.slice(0, 30),
        surface: "web",
        form: "thread",
        agent_id: selectedAgentId,
      })
      cid = conv.id
      const createdAttempt = { ...attempt, conversationId: cid }
      if (firstSend.current === attempt) firstSend.current = createdAttempt
      attempt = createdAttempt
    }
    try {
      await sendUserMessage(cid, { content })
    } finally {
      // A failed first message still leaves a real conversation in the list.
      qc.invalidateQueries({
        predicate: (q) =>
          q.queryKey[0] === "admin" && q.queryKey[1] === "conversations" && q.queryKey[2] === wsId,
      })
      qc.invalidateQueries({ queryKey: ["admin", "conversationTimeline", cid] })
    }
    if (firstSend.current !== attempt) return false
    firstSend.current = null
    writeConversationViewState(wsId, {
      agentId: selectedAgentId,
      conversationId: cid,
    })
    navigate("conversations", { id: cid })
    return true
  }

  return { openCreate, handleSendFromEmpty, resetFirstSend: () => { firstSend.current = null; firstSendPending.current = null } }
}
