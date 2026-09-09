import { useEffect, useState } from "react"
import { useQueries } from "@tanstack/react-query"
import { KEY_CAPABILITY_VERSIONS, listCapabilityVersions } from "../../../lib/api-capabilities"
import { noUnreachableRetry } from "../../../lib/api-client"
import type { AgentCapability, Capability, Secret } from "../../../lib/api-types"
import { catalogIDFromVersion, credentialBinding, sharedSecretsForKind } from "../../../lib/credential-bindings"
import { withoutCredentialBindings } from "../../../lib/agent-clone"

export function useAgentCloneCredentials(
  sourceID: string | null,
  workspaceID: string | null,
  sourceConfig: Record<string, unknown>,
  bindings: AgentCapability[],
  capabilities: Capability[],
  selectedIDs: string[],
  versions: Record<string, { versionID: string }>,
  secrets: Secret[],
) {
  const [choices, setChoices] = useState<Record<string, Record<string, string>>>({})
  useEffect(() => setChoices({}), [sourceID])
  const selected = sourceID ? capabilities.filter((cap) => selectedIDs.includes(cap.id)) : []
  const queries = useQueries({ queries: selected.map((cap) => ({
    queryKey: KEY_CAPABILITY_VERSIONS(workspaceID ?? "_none", cap.id),
    queryFn: () => listCapabilityVersions(workspaceID as string, cap.id),
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    staleTime: 30_000,
  })) })
  const rows = selected.map((cap, index) => {
    const query = queries[index]
    const versionID = versions[cap.id]?.versionID || cap.latest_version_id
    const version = query.data?.versions.find((item) => item.id === versionID)
    const source = bindings.find((binding) => binding.capability_id === cap.id)
    const fields = (version?.required_credentials ?? []).filter((rc) => rc.required).map((rc) => {
      const available = sharedSecretsForKind(secrets, rc.kind, catalogIDFromVersion(version))
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
      ready: !!version,
      failed: query.isError || (query.isSuccess && !version),
    }
  })
  return {
    rows,
    ready: rows.every((row) => row.ready),
    failed: rows.some((row) => row.failed),
    valid: rows.every((row) => row.ready && row.fields.every((field) => field.value)),
    needsCredentials: rows.some((row) => row.fields.length > 0),
    retry: () => { for (const query of queries) void query.refetch() },
    choose: (capabilityID: string, kind: string, secretID: string) => setChoices((prev) => ({
      ...prev, [capabilityID]: { ...prev[capabilityID], [kind]: secretID },
    })),
  }
}
