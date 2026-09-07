import { useTranslation } from "react-i18next"
import { ArrowUp, Loader2 } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"
import { useUpgrade } from "../../../lib/api-marketplace"
import type { AgentCapability, Capability, CapabilityVersion, Agent } from "../../../lib/api-types"
import { InlineNotice } from "./notices"

interface UpgradeCapabilityDialogProps {
  agent: Agent
  capability: Capability
  binding: AgentCapability
  latestVersion?: CapabilityVersion
  workspaceID: string | null
  disabled?: boolean
  onToast: (message: string) => void
}

/** Inline "a newer version exists" row with its one upgrade action. */
export function UpgradeCapabilityDialog({ agent, capability, binding, latestVersion, workspaceID, disabled, onToast }: UpgradeCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const upgradeMut = useUpgrade(workspaceID, agent.id)
  const errMsg = upgradeMut.error instanceof ApiError ? upgradeMut.error.envelope.message : upgradeMut.error instanceof Error ? upgradeMut.error.message : null
  const canUpgrade = !!latestVersion && latestVersion.id !== binding.capability_version_id && !disabled && !upgradeMut.isPending
  return (
    <div className="mt-3 border-t border-line pt-2">
      <div className="flex flex-wrap items-center justify-between gap-2 text-sm text-fg">
        <span className="inline-flex items-center gap-2">
          <ArrowUp className="h-3.5 w-3.5 shrink-0 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
          {disabled ? t("agents.detail.capabilities.marketplace.upgradeBlocked") : t("agents.detail.capabilities.marketplace.upgradeAvailable", { version: latestVersion?.version ?? "—" })}
        </span>
        <Button size="sm" variant="outline" disabled={!canUpgrade} onClick={() => {
          if (!latestVersion) return
          upgradeMut.mutate({ capabilityID: capability.id, versionID: latestVersion.id }, {
            onSuccess: () => onToast(t("agents.detail.capabilities.toast.upgraded", { cap: capability.name, version: latestVersion.version })),
          })
        }}>
          {upgradeMut.isPending && <Loader2 className="animate-spin" />}
          {t("agents.detail.capabilities.actions.upgrade")}
        </Button>
      </div>
      {errMsg && <InlineNotice tone="error" className="mt-2">{errMsg}</InlineNotice>}
    </div>
  )
}
