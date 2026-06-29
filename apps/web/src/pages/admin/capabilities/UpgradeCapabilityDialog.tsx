import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"
import { useUpgrade } from "../../../lib/api-marketplace"
import type { AgentCapability, Capability, CapabilityVersion, ProjectAgent } from "../../../lib/api-types"

interface UpgradeCapabilityDialogProps {
  agent: ProjectAgent
  capability: Capability
  binding: AgentCapability
  latestVersion?: CapabilityVersion
  projectID: string | null
  disabled?: boolean
  onToast: (message: string) => void
}

export function UpgradeCapabilityDialog({ agent, capability, binding, latestVersion, projectID, disabled, onToast }: UpgradeCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const upgradeMut = useUpgrade(projectID, agent.project_agent_id)
  const errMsg = upgradeMut.error instanceof ApiError ? upgradeMut.error.envelope.message : upgradeMut.error instanceof Error ? upgradeMut.error.message : null
  const canUpgrade = !!latestVersion && latestVersion.id !== binding.capability_version_id && !disabled && !upgradeMut.isPending
  return (
    <div className="mt-3 rounded-md border border-blue-200 bg-blue-50 px-3 py-2 text-[13px] text-blue-800">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span>{disabled ? t("agents.detail.capabilities.marketplace.upgradeBlocked") : t("agents.detail.capabilities.marketplace.upgradeAvailable", { version: latestVersion?.version ?? "—" })}</span>
        <Button size="sm" variant="outline" disabled={!canUpgrade} onClick={() => {
          if (!latestVersion) return
          upgradeMut.mutate({ capabilityID: capability.id, versionID: latestVersion.id }, {
            onSuccess: () => onToast(t("agents.detail.capabilities.toast.upgraded", { cap: capability.name, version: latestVersion.version })),
          })
        }}>{upgradeMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("agents.detail.capabilities.actions.upgrade")}</Button>
      </div>
      {errMsg && <p className="mt-2 rounded-md bg-red-50 px-2 py-1 text-red-700">{errMsg}</p>}
    </div>
  )
}
