import { useMemo, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { Bot, Check, Loader2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "../../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import { noUnreachableRetry } from "../../../lib/api-client"
import { useAgents } from "../../../lib/api-agents"
import {
  KEY_AGENT_CAPABILITIES,
  listAgentCapabilities,
  useCapabilityQuery,
  useCapabilityVersionsQuery,
  useEnableAgentCapabilityMutation,
} from "../../../lib/api-capabilities"
import { useAuth } from "../../../lib/auth-context"

interface AddCapabilityToAgentDialogProps {
  workspaceID: string | null
  capabilityID: string
  onOpenChange: (open: boolean) => void
  onAdded: (capabilityName: string, agentName: string) => void
}

export function AddCapabilityToAgentDialog({
  workspaceID,
  capabilityID,
  onOpenChange,
  onAdded,
}: AddCapabilityToAgentDialogProps) {
  const { t } = useTranslation("admin")
  const { user } = useAuth()
  const [selectedAgentID, setSelectedAgentID] = useState<string | null>(null)
  const capabilityQ = useCapabilityQuery(workspaceID, capabilityID)
  const versionsQ = useCapabilityVersionsQuery(workspaceID, capabilityID)
  const agentsQ = useAgents(workspaceID)

  const eligibleAgents = useMemo(
    () =>
      (agentsQ.data?.agents ?? []).filter(
        (agent) => !agent.created_by_user_id || agent.created_by_user_id === user?.user_id,
      ),
    [agentsQ.data?.agents, user?.user_id],
  )
  const agentCapabilityQueries = useQueries({
    queries: eligibleAgents.map((agent) => ({
      queryKey: KEY_AGENT_CAPABILITIES(workspaceID ?? "_none", agent.id),
      queryFn: () => listAgentCapabilities(workspaceID, agent.id),
      enabled: !!workspaceID,
      retry: noUnreachableRetry,
      staleTime: 30_000,
    })),
  })
  const installedAgentIDs = new Set(
    eligibleAgents
      .filter((_, index) =>
        agentCapabilityQueries[index].data?.installed.some(
          (item) => item.capability_id === capabilityID,
        ),
      )
      .map((agent) => agent.id),
  )

  const latestVersion = versionsQ.data?.versions[0]
  const selectedAgent = eligibleAgents.find((agent) => agent.id === selectedAgentID)
  const enableMutation = useEnableAgentCapabilityMutation(workspaceID, selectedAgentID)
  const loading = capabilityQ.isLoading || versionsQ.isLoading || agentsQ.isLoading
  const loadError = capabilityQ.error ?? versionsQ.error ?? agentsQ.error

  const submit = () => {
    if (!latestVersion || !selectedAgent) return
    enableMutation.mutate(
      { capabilityVersionID: latestVersion.id, pinningMode: "latest" },
      {
        onSuccess: () => {
          onAdded(capabilityQ.data?.name ?? capabilityID, selectedAgent.name)
          onOpenChange(false)
        },
      },
    )
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>
            {t("capabilities.mcpDirectory.addToAgent.title", {
              name:
                capabilityQ.data?.name ??
                t("capabilities.mcpDirectory.addToAgent.connectorFallback"),
            })}
          </DialogTitle>
          <DialogDescription>
            {t("capabilities.mcpDirectory.addToAgent.description")}
          </DialogDescription>
        </DialogHeader>

        {loading ? (
          <div className="flex min-h-32 items-center justify-center text-fg-muted">
            <Loader2 className="h-5 w-5 animate-spin" />
          </div>
        ) : loadError || !latestVersion ? (
          <div
            className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
            role="alert"
          >
            {t("capabilities.mcpDirectory.addToAgent.loadError")}
          </div>
        ) : eligibleAgents.length === 0 ? (
          <div className="rounded-md border border-line bg-surface-subtle px-4 py-6 text-center text-sm text-fg-muted">
            {t("capabilities.mcpDirectory.addToAgent.empty")}
          </div>
        ) : (
          <div
            className="max-h-72 space-y-2 overflow-y-auto pr-1"
            role="radiogroup"
            aria-label={t("capabilities.mcpDirectory.addToAgent.agentListLabel")}
          >
            {eligibleAgents.map((agent, index) => {
              const installed = installedAgentIDs.has(agent.id)
              const checking = agentCapabilityQueries[index]?.isLoading
              const selected = selectedAgentID === agent.id
              return (
                <label
                  key={agent.id}
                  className={`flex items-center gap-3 rounded-lg border px-3 py-3 transition-colors ${
                    installed || checking
                      ? "cursor-not-allowed border-line-muted bg-surface-subtle text-fg-muted"
                      : selected
                        ? "cursor-pointer border-line-strong bg-surface-subtle ring-1 ring-line-strong"
                        : "cursor-pointer border-line bg-surface hover:border-line-strong hover:bg-surface-subtle"
                  }`}
                >
                  <input
                    type="radio"
                    name="capability-agent"
                    value={agent.id}
                    checked={selected}
                    disabled={installed || checking}
                    onChange={() => setSelectedAgentID(agent.id)}
                    className="h-4 w-4"
                  />
                  <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md bg-surface-muted text-fg-muted">
                    <Bot className="h-4 w-4" />
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-sm font-medium text-fg">{agent.name}</span>
                    {agent.description && (
                      <span className="block truncate text-xs text-fg-subtle">
                        {agent.description}
                      </span>
                    )}
                  </span>
                  {checking ? (
                    <Loader2 className="h-4 w-4 shrink-0 animate-spin text-fg-faint" />
                  ) : installed ? (
                    <span className="flex shrink-0 items-center gap-1 text-xs font-medium text-success-emphasis">
                      <Check className="h-3.5 w-3.5" />
                      {t("capabilities.mcpDirectory.addToAgent.added")}
                    </span>
                  ) : null}
                </label>
              )
            })}
          </div>
        )}

        {enableMutation.isError && (
          <div
            className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis"
            role="alert"
          >
            {enableMutation.error instanceof Error
              ? enableMutation.error.message
              : t("capabilities.mcpDirectory.addToAgent.submitError")}
          </div>
        )}

        <DialogFooter>
          <Button
            variant="outline"
            onClick={() => onOpenChange(false)}
            disabled={enableMutation.isPending}
          >
            {t("capabilities.mcpDirectory.addToAgent.cancel")}
          </Button>
          <Button
            onClick={submit}
            disabled={
              !selectedAgent ||
              !latestVersion ||
              installedAgentIDs.has(selectedAgent.id) ||
              enableMutation.isPending
            }
          >
            {enableMutation.isPending && <Loader2 className="h-4 w-4 animate-spin" />}
            {t("capabilities.mcpDirectory.actions.addToAgent")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
