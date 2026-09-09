import { useMemo, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Field } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
import { Skeleton } from "../../../components/ui/skeleton"
import { useToast } from "../../../components/ui/toast"
import { useAgents } from "../../../lib/api-agents"
import { useAgentCapabilitiesQuery } from "../../../lib/api-capabilities"
import { useMyCredentials } from "../../../lib/api-credentials"
import { useMarketplaceList } from "../../../lib/api-marketplace"
import { useSecrets } from "../../../lib/api-secrets"
import { agentEngineOf, agentEngineSupportsCapability } from "../../../lib/agent-view-model"
import { CapabilityVersionDialog } from "./CapabilityVersionDialog"

export function MarketplaceInstallDialog({ capabilityID, workspaceID, agentID, workspaceRole, onSelectAgent, onDismiss, onInstalled }: {
  capabilityID: string
  workspaceID: string | null
  agentID: string | null
  workspaceRole?: string
  onSelectAgent: (id: string | null) => void
  onDismiss: () => void
  onInstalled: (id: string) => void
}) {
  const { t } = useTranslation(["admin", "common"])
  const toast = useToast()
  const [choice, setChoice] = useState(agentID ?? "")
  const marketplaceQ = useMarketplaceList(workspaceID)
  const agentsQ = useAgents(workspaceID)
  const capability = marketplaceQ.data?.find((item) => item.id === capabilityID)
  const agents = (agentsQ.data?.agents ?? []).filter((agent) => capability && agentEngineSupportsCapability(agentEngineOf(agent), capability.type))
  const agent = agents.find((item) => item.id === agentID)
  const bindingsQ = useAgentCapabilitiesQuery(workspaceID, agent?.id ?? null)
  const credentialsQ = useMyCredentials()
  const secretsQ = useSecrets(workspaceID)
  const sharedSecrets = useMemo(() => (secretsQ.data?.secrets ?? []).filter((secret) => secret.kind === "capability_inline" && secret.status === "active"), [secretsQ.data])
  const allowed = workspaceRole === "owner" || workspaceRole === "admin" || workspaceRole === "member"
  const loading = marketplaceQ.isLoading || agentsQ.isLoading || !workspaceRole
    || (!!agent && (bindingsQ.isLoading || credentialsQ.isLoading || secretsQ.isLoading))
  const error = marketplaceQ.error ?? agentsQ.error ?? (agent ? bindingsQ.error ?? credentialsQ.error ?? secretsQ.error : null)
  const installed = bindingsQ.data?.installed.some((item) => item.capability_id === capabilityID)

  if (allowed && !loading && !error && capability && agent && !installed) {
    const current = window.location.pathname + window.location.search
    const credentialHelp = agent.visibility !== "public" && capability.required_credentials?.some((item) => item.required)
      ? <a className="text-sm text-fg underline underline-offset-4" href={`?profile=credentials&returnTo=${encodeURIComponent(current)}`}>{t("agents.pendingCapability.manageCredentials")}</a>
      : undefined
    return <CapabilityVersionDialog
      key={`${capability.id}:${agent.id}`}
      mode="enable"
      agent={agent}
      capability={{ ...capability, from_marketplace: true }}
      workspaceID={workspaceID}
      credentials={credentialsQ.data?.credentials ?? []}
      sharedSecrets={sharedSecrets}
      open
      onOpenChange={(open) => { if (!open) onDismiss() }}
      onBack={() => onSelectAgent(null)}
      onInstalled={() => onInstalled(agent.id)}
      trigger={() => null}
      credentialHelp={credentialHelp}
      onToast={toast.show}
    />
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onDismiss() }}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("agents.pendingCapability.title", { cap: capability?.name ?? t("agents.pendingCapability.capability") })}</DialogTitle>
          <DialogDescription>{t("agents.pendingCapability.description")}</DialogDescription>
        </DialogHeader>
        {loading ? <Skeleton className="h-24 w-full" /> : !allowed ? (
          <p className="text-sm text-fg-muted">{t("agents.pendingCapability.notAllowed")}</p>
        ) : error ? (
          <ErrorState title={t("agents.pendingCapability.loadError")} detail={error instanceof Error ? error.message : undefined} onRetry={() => {
            void marketplaceQ.refetch()
            void agentsQ.refetch()
            if (agent) { void bindingsQ.refetch(); void credentialsQ.refetch(); void secretsQ.refetch() }
          }} />
        ) : !capability ? (
          <p className="text-sm text-fg-muted">{t("agents.pendingCapability.notFound")}</p>
        ) : installed && agent ? (
          <p className="text-sm text-fg">{t("agents.pendingCapability.alreadyAdded", { agent: agent.name })}</p>
        ) : agents.length === 0 ? (
          <EmptyState size="compact" title={t("agents.pendingCapability.empty")} description={t("agents.pendingCapability.emptyDescription")} />
        ) : (
          <Field label={t("agents.pendingCapability.chooseAgent")}>
            <Select aria-label={t("agents.pendingCapability.chooseAgent")} value={choice} onValueChange={setChoice}>
              <SelectOption value="">{t("agents.pendingCapability.chooseAgent")}</SelectOption>
              {agents.map((item) => <SelectOption key={item.id} value={item.id}>{item.name}</SelectOption>)}
            </Select>
          </Field>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onDismiss}>{t("common:actions.cancel")}</Button>
          {allowed && !loading && !error && capability && (installed && agent ? (
            <Button onClick={() => onInstalled(agent.id)}>{t("agents.pendingCapability.viewConfig")}</Button>
          ) : (
            <Button disabled={!agents.some((item) => item.id === choice)} onClick={() => onSelectAgent(choice)}>{t("agents.pendingCapability.continue")}</Button>
          ))}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
