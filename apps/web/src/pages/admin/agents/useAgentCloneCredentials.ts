import { useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { KEY_CAPABILITY_VERSIONS, listCapabilityVersions } from "../../../lib/api-capabilities"
import { noUnreachableRetry } from "../../../lib/api-client"
import type { AgentCapability, Capability, CapabilityVersion, Secret } from "../../../lib/api-types"
import { credentialBinding, sharedSecretsForKind } from "../../../lib/credential-bindings"
import { catalogIDFromVersion } from "../../../lib/capability-config"
import { withoutCredentialBindings } from "../../../lib/agent-clone"
import { useMarketplaceList } from "../../../lib/api-marketplace"

export function useAgentCloneCredentials(
  sourceID: string | null,
  workspaceID: string | null,
  sourceConfig: Record<string, unknown>,
  bindings: AgentCapability[],
  capabilities: Capability[],
  selectedIDs: string[],
  versions: Record<string, { pinningMode: "latest" | "pinned"; versionID: string }>,
  secrets: Secret[],
) {
  const [choices, setChoices] = useState<Record<string, Record<string, string>>>({})
  const [choiceSourceID, setChoiceSourceID] = useState(sourceID)
  if (sourceID !== choiceSourceID) {
    setChoiceSourceID(sourceID)
    setChoices({})
  }
  const selected = sourceID ? capabilities.filter((cap) => selectedIDs.includes(cap.id)) : []
  const hasMarketplace = selected.some((cap) => cap.from_marketplace)
  const marketplace = useMarketplaceList(hasMarketplace ? workspaceID : null)
  const queries = useQueries({ queries: selected.map((cap) => ({
    queryKey: KEY_CAPABILITY_VERSIONS(workspaceID ?? "_none", cap.id),
    queryFn: () => listCapabilityVersions(workspaceID as string, cap.id),
    enabled: !!workspaceID && !cap.from_marketplace,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })) })
  const rows = selected.map((cap, index) => {
    const query = queries[index]
    const choice = versions[cap.id]
    const versionID = choice?.pinningMode === "pinned" ? choice.versionID : cap.latest_version_id
    const source = bindings.find((binding) => binding.capability_id === cap.id)
    const published = marketplace.data?.find((item) => item.id === cap.id && item.latest_version_id === cap.latest_version_id)
    // Installed and published metadata is the authorized view for foreign capabilities.
    const foreignVersions = [
      ...(source ? [{ id: source.capability_version_id, version: source.version ?? source.capability?.pinned_version ?? "?",
        required_credentials: source.required_credentials ?? source.capability?.required_credentials }] : []),
      ...(published?.latest_version_id && published.latest_version_id !== source?.capability_version_id
        ? [{ id: published.latest_version_id, version: published.latest_version ?? "?", required_credentials: published.required_credentials }] : []),
    ]
    const availableVersions: Pick<CapabilityVersion, "id" | "version" | "required_credentials" | "source_payload">[] =
      cap.from_marketplace ? foreignVersions : query.data?.versions ?? []
    const version = availableVersions.find((item) => item.id === versionID)
    const fields = (version?.required_credentials ?? []).map((rc) => {
      const available = sharedSecretsForKind(secrets, rc.kind, catalogIDFromVersion(version as CapabilityVersion | undefined))
      // A per-capability personal binding overrides an Agent-wide shared one.
      const original = credentialBinding(source?.configuration, rc.kind) ?? credentialBinding(sourceConfig, rc.kind)
      const requested = choices[cap.id]?.[rc.kind] ?? original?.secretID ?? ""
      const value = available.some((secret) => secret.id === requested) ? requested : ""
      return { ...rc, available, value }
    })
    const configuration = withoutCredentialBindings(source?.configuration ?? {})
    if (fields.length) configuration.credential_bindings = Object.fromEntries(fields
      .filter((field) => field.value)
      .map((field) => [field.kind, { source: "shared", secret_id: field.value }]))
    return { capabilityID: cap.id, name: cap.name, versionID, fields, configuration,
      versions: availableVersions, latestVersionID: cap.latest_version_id ?? "", latestVersion: cap.latest_version ?? "",
      ready: !!version,
      failed: cap.from_marketplace ? !version && (marketplace.isError || marketplace.isSuccess) : query.isError || (query.isSuccess && !version),
    }
  })
  return {
    rows,
    ready: rows.every((row) => row.ready),
    failed: rows.some((row) => row.failed),
    valid: rows.every((row) => row.ready && row.fields.every((field) => !field.required || field.value)),
    needsCredentials: rows.some((row) => row.fields.length > 0),
    retry: () => {
      if (hasMarketplace) void marketplace.refetch()
      for (const [index, query] of queries.entries()) if (!selected[index].from_marketplace) void query.refetch()
    },
    choose: (capabilityID: string, kind: string, secretID: string) => setChoices((prev) => ({
      ...prev, [capabilityID]: { ...prev[capabilityID], [kind]: secretID },
    })),
  }
}
