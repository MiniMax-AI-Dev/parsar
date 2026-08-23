/**
 * React hook for loading plugin client bundles for the current workspace.
 * Fetches the plugin list from the capabilities API and loads any that
 * have client_entry defined.
 *
 * Plugins are loaded once on page load. After binding or unbinding a
 * capability, refresh the page to pick up the change (same as DSH).
 */

import { useEffect, useRef } from "react"
import { useQuery, useQueryClient } from "@tanstack/react-query"
import { apiRequest, noUnreachableRetry } from "./api-client"
import { loadAllPluginClients, unloadPlugin, isPluginLoaded } from "./plugin-loader"

interface PluginCapability {
  id: string
  name: string
  type: string
}

interface PluginListResponse {
  capabilities: PluginCapability[]
}

async function fetchBundleCapabilities(wsId: string): Promise<PluginCapability[]> {
  const resp = await apiRequest<PluginListResponse>(
    `/api/v1/workspaces/${encodeURIComponent(wsId)}/capabilities`,
    { query: { type: "bundle" } }
  )
  return resp.capabilities ?? []
}

export function usePluginClients(workspaceId: string | null) {
  const prevPluginsRef = useRef<Set<string>>(new Set())

  const { data: capabilities } = useQuery({
    queryKey: ["plugins", "bundles", workspaceId ?? "_none"],
    queryFn: () => {
      if (!workspaceId) return []
      return fetchBundleCapabilities(workspaceId)
    },
    enabled: !!workspaceId,
    retry: noUnreachableRetry,
    staleTime: Infinity,
  })

  useEffect(() => {
    if (!capabilities) return

    const currentNames = new Set(
      capabilities.filter((c) => c.type === "bundle").map((c) => c.name)
    )

    // Unload plugins that were previously loaded but are no longer in the list.
    for (const name of prevPluginsRef.current) {
      if (!currentNames.has(name)) {
        unloadPlugin(name)
      }
    }

    // Load new plugins that aren't loaded yet.
    const toLoad = [...currentNames].filter((name) => !isPluginLoaded(name))
    if (toLoad.length > 0) {
      void loadAllPluginClients(toLoad.map((name) => ({ name, client_entry: "yes" })))
    }

    prevPluginsRef.current = currentNames
  }, [capabilities])
}

/** Invalidate the plugin bundles query to trigger reload/unload. */
export function useInvalidatePlugins() {
  const qc = useQueryClient()
  return (workspaceId: string) => {
    void qc.invalidateQueries({ queryKey: ["plugins", "bundles", workspaceId] })
  }
}
