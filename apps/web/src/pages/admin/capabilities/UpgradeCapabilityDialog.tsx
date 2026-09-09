import { useTranslation } from "react-i18next"
import { Loader2 } from "lucide-react"

import { Button } from "../../../components/ui/button"
import { ApiError } from "../../../lib/api-client"
import { useUpgrade } from "../../../lib/api-marketplace"
import type { AgentCapability, Capability, CapabilityVersion, Agent } from "../../../lib/api-types"
import { InlineNotice } from "./notices"
import type { ShowToast } from "../../../components/ui/toast"

interface UpgradeCapabilityDialogProps {
  agent: Agent
  capability: Capability
  binding: AgentCapability
  latestVersion?: CapabilityVersion
  workspaceID: string | null
  disabled?: boolean
  onToast: ShowToast
}

/**
 * The upgrade verb on its own. The sentence that used to sit beside it in a
 * bordered banner is the row's note now, so this is only the control.
 */
export function UpgradeCapabilityDialog({ agent, capability, binding, latestVersion, workspaceID, disabled, onToast }: UpgradeCapabilityDialogProps) {
  const { t } = useTranslation("admin")
  const upgradeMut = useUpgrade(workspaceID, agent.id)
  const errMsg = upgradeMut.error instanceof ApiError ? upgradeMut.error.envelope.message : upgradeMut.error instanceof Error ? upgradeMut.error.message : null
  const canUpgrade = !!latestVersion && latestVersion.id !== binding.capability_version_id && !disabled && !upgradeMut.isPending
  return (
    <>
      <Button size="sm" variant="link" className="shrink-0 px-0" disabled={!canUpgrade} onClick={() => {
        if (!latestVersion) return
        upgradeMut.mutate({ capabilityID: capability.id, versionID: latestVersion.id }, {
          onSuccess: () => onToast(t("agents.detail.capabilities.toast.upgraded", { cap: capability.name, version: latestVersion.version })),
        })
      }}>
        {upgradeMut.isPending && <Loader2 className="animate-spin" />}
        {t("agents.detail.capabilities.actions.upgrade")}
      </Button>
      {errMsg && <InlineNotice tone="error" className="mt-1">{errMsg}</InlineNotice>}
    </>
  )
}
