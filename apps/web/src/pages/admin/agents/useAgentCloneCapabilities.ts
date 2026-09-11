import { useState, type Dispatch, type SetStateAction } from "react"
import { cloneableAgentCapabilities } from "../../../lib/agent-clone"
import type { AgentCapability } from "../../../lib/api-types"

type VersionChoice = { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }

export function useAgentCloneCapabilities(
  open: boolean,
  sourceID: string | null,
  bindings: AgentCapability[] | undefined,
  setSelectedIDs: Dispatch<SetStateAction<string[]>>,
  setVersionChoices: Dispatch<SetStateAction<Record<string, VersionChoice>>>,
) {
  const [snapshot, setSnapshot] = useState<{ sourceID: string; bindings: AgentCapability[] } | null>(null)
  if (!open || !sourceID) {
    if (snapshot !== null) setSnapshot(null)
  } else if (snapshot?.sourceID !== sourceID && bindings) {
    const selected = cloneableAgentCapabilities(bindings)
    setSelectedIDs(selected.map((binding) => binding.capability_id))
    setVersionChoices(Object.fromEntries(selected.map((binding) => [binding.capability_id, {
      pinningMode: binding.pinning_mode === "latest" ? "latest" : "pinned",
      versionID: binding.capability_version_id,
      pinnedVersion: binding.version ?? binding.capability?.pinned_version,
    }])))
    setSnapshot({ sourceID, bindings: selected })
  }
  return {
    ready: !sourceID || snapshot?.sourceID === sourceID,
    bindings: snapshot?.sourceID === sourceID ? snapshot.bindings : [],
  }
}
