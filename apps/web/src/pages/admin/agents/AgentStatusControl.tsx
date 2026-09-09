import { useRef, type ReactNode } from "react"
import { Loader2, Pause, Play } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import { ErrorDialog } from "../../../components/ui/error-dialog"
import { useToast } from "../../../components/ui/toast"
import { useSetAgentStatus } from "../../../lib/api-agents"
import type { Agent } from "../../../lib/api-types"

export function AgentStatusControl({ agent, workspaceID, children }: { agent: Agent; workspaceID: string | null; children: (action: ReactNode) => ReactNode }) {
  const { t } = useTranslation(["admin", "common"])
  const mutation = useSetAgentStatus(workspaceID, agent.id)
  const toast = useToast()
  const scope = useRef<HTMLElement | null>(null)
  const enable = agent.status === "disabled"
  const Icon = mutation.isPending ? Loader2 : enable ? Play : Pause

  const action = (
    <Button data-agent-status-action={agent.id} variant="outline" disabled={mutation.isPending} onClick={() => {
      if (mutation.isPending) return
      mutation.mutate(enable, {
        onSuccess: () => toast.show(t(enable ? "agents.statusAction.enabled" : "agents.statusAction.disabled", { name: agent.name })),
      })
    }}>
      <Icon className={mutation.isPending ? "animate-spin" : undefined} strokeWidth={1.5} aria-hidden="true" />
      {t(enable ? "agents.statusAction.enable" : "agents.statusAction.disable")}
    </Button>
  )

  return (
    <>
      {children(action)}
      {mutation.isError && <ErrorDialog
        title={t("agents.statusAction.failed")}
        message={t("common:errors.submitRetryHint")}
        detail={mutation.error.message}
        onClose={() => mutation.reset()}
        onRestoreFocus={() => {
          const buttons = document.querySelectorAll<HTMLButtonElement>(`[data-agent-status-action="${CSS.escape(agent.id)}"]`)
          const target = buttons[buttons.length - 1]
          scope.current = target?.parentElement ?? null
          target?.focus()
        }}
        focusScopeRef={scope}
      />}
    </>
  )
}
