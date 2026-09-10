import { useRef, useState, type ReactNode } from "react"
import { Loader2, Pause, Play } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import { ErrorDialog } from "../../../components/ui/error-dialog"
import { useToast } from "../../../components/ui/toast"
import { useSetAgentStatus } from "../../../lib/api-agents"
import type { Agent } from "../../../lib/api-types"

export function AgentStatusControl({ workspaceID, children }: { workspaceID: string | null; children: (renderAction: (agent: Agent) => ReactNode) => ReactNode }) {
  const { t } = useTranslation(["admin", "common"])
  const mutation = useSetAgentStatus(workspaceID)
  const toast = useToast()
  const scope = useRef<HTMLElement | null>(null)
  const [requestedAgent, setRequestedAgent] = useState<Agent | null>(null)

  const renderAction = (agent: Agent) => {
    const enable = agent.status === "disabled"
    const pending = mutation.isPending && mutation.variables.agentID === agent.id
    const Icon = pending ? Loader2 : enable ? Play : Pause
    return (
      <Button data-agent-status-action={agent.id} variant="outline" disabled={mutation.isPending} onClick={() => {
        if (mutation.isPending) return
        setRequestedAgent(agent)
        mutation.mutate({ agentID: agent.id, enabled: enable }, {
          onSuccess: () => toast.show(t(enable ? "agents.statusAction.enabled" : "agents.statusAction.disabled", { name: agent.name })),
        })
      }}>
        <Icon className={pending ? "animate-spin" : undefined} strokeWidth={1.5} aria-hidden="true" />
        {t(enable ? "agents.statusAction.enable" : "agents.statusAction.disable")}
      </Button>
    )
  }

  return (
    <>
      {children(renderAction)}
      {mutation.isError && <ErrorDialog
        title={t("agents.statusAction.failed")}
        message={`${requestedAgent?.name ?? ""}: ${t("common:errors.submitRetryHint")}`}
        detail={mutation.error.message}
        onClose={() => mutation.reset()}
        onRestoreFocus={() => {
          const buttons = document.querySelectorAll<HTMLButtonElement>(`[data-agent-status-action="${CSS.escape(requestedAgent?.id ?? "")}"]`)
          const target = buttons[buttons.length - 1]
            ?? document.querySelector<HTMLElement>("[data-agent-status-action], input[type=search]")
          scope.current = target?.parentElement ?? null
          target?.focus()
        }}
        focusScopeRef={scope}
      />}
    </>
  )
}
