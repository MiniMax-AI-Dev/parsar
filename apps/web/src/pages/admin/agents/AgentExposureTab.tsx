import { useState } from "react"
import { useTranslation } from "react-i18next"
import { ArrowUpRight } from "lucide-react"

import { FeishuConnectorPanel } from "../../../components/admin/FeishuConnectorPanel"
import { Button } from "../../../components/ui/button"
import { StatusIcon, type StatusKind } from "../../../components/ui/status-icon"
import { useAdminView } from "../../../lib/admin-router"
import { useConversations } from "../../../lib/api-conversations"
import type { FeishuConnectorConfig } from "../../../lib/api-agents"
import type { AgentDetail } from "../../../lib/api-types"
import { DetailSection } from "./DetailSection"
import type { ShowToast } from "../../../components/ui/toast"

/**
 * Where an Agent can be reached from.
 *
 * An Agent is only useful once someone outside this console can talk to it, so
 * its exits are a first-class part of the Agent rather than a setting hidden
 * elsewhere. The list is deliberately complete — the always-open exits are
 * shown alongside the ones you configure, because "which doors are open" is
 * the question, and an answer that omits the open ones does not answer it.
 *
 * A conversation is one exit, not two: the console frames it in admin chrome
 * and `/c/<id>` frames it bare, but it is the same thread behind the same
 * workspace membership. The share affordance therefore lives on a conversation,
 * not here.
 */
export function AgentExposureTab({ agent, workspaceID, canEdit, onToast }: {
  agent: AgentDetail
  workspaceID: string | null
  canEdit: boolean
  onToast: ShowToast
}) {
  const { t } = useTranslation("admin")
  const { navigate } = useAdminView()
  const conversationsQ = useConversations(workspaceID, agent.id)
  const conversations = conversationsQ.data?.conversations ?? []
  // Clearing `id` would restore whichever conversation was last open — which
  // can belong to a different agent — so the exit names one of this agent's.
  const latest = conversations[0]
  const feishu = readFeishuConnector(agent)
  const [feishuOpen, setFeishuOpen] = useState(Boolean(feishu?.enabled))
  // The rail swaps content rather than remounting, so reset with the agent.
  const [shownID, setShownID] = useState(agent.id)
  if (agent.id !== shownID) {
    setShownID(agent.id)
    setFeishuOpen(Boolean(feishu?.enabled))
  }

  return (
    <>
      <DetailSection title={t("agents.exposure.conversation.title")}>
        <Exit
          status="completed"
          state={t("agents.exposure.state.alwaysOn")}
          action={
            latest ? (
              <Button variant="link" onClick={() => navigate("conversations", { id: latest.id })}>
                {t("agents.exposure.conversation.open", { count: conversations.length })}
                <ArrowUpRight strokeWidth={1.5} aria-hidden="true" />
              </Button>
            ) : undefined
          }
        />
      </DetailSection>

      <DetailSection title={t("agents.exposure.feishu.title")}>
        <Exit
          status={feishu?.enabled ? "completed" : "cancelled"}
          state={t(feishu?.enabled ? "agents.exposure.state.on" : "agents.exposure.state.off")}
          action={
            canEdit ? (
              <Button variant="outline" onClick={() => setFeishuOpen((v) => !v)}>
                {t(feishuOpen ? "agents.exposure.feishu.hide" : "agents.exposure.feishu.configure")}
              </Button>
            ) : undefined
          }
        />
        {feishuOpen && workspaceID && (
          <div className="mt-3">
            <FeishuConnectorPanel
              agentID={agent.id}
              workspaceID={workspaceID}
              current={feishu}
              canEdit={canEdit}
              onToast={onToast}
            />
          </div>
        )}
      </DetailSection>

      <DetailSection title={t("agents.exposure.mcp.title")}>
        <Exit status="queued" state={t("agents.exposure.state.notYet")} />
      </DetailSection>
    </>
  )
}

/** One exit: whether it is open, and the one thing you can do about it. */
function Exit({ status, state, action }: {
  status: StatusKind
  state: string
  action?: React.ReactNode
}) {
  return (
    <div className="flex min-h-8 items-center gap-2">
      <StatusIcon status={status} title={state} />
      <span className="text-sm text-fg">{state}</span>
      {action && <span className="ml-auto">{action}</span>}
    </div>
  )
}

/** `agents.config.connectors.feishu`, or undefined when never configured. */
function readFeishuConnector(agent: AgentDetail): FeishuConnectorConfig | undefined {
  const connectors = (agent.config ?? {}).connectors
  if (!connectors || typeof connectors !== "object") return undefined
  const feishu = (connectors as Record<string, unknown>).feishu
  return feishu && typeof feishu === "object" ? (feishu as FeishuConnectorConfig) : undefined
}
