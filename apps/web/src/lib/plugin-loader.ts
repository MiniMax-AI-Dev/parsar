/**
 * Plugin Client Loader — fetches plugin client.js bundles and executes them.
 *
 * On page load (or when the agent's enabled plugins change), this module:
 * 1. Fetches the plugin list for the current agent/workspace
 * 2. For each plugin with a client_entry, fetches GET /api/v1/plugins/{name}/client.js
 * 3. Executes the bundle in a function scope with access to window.__PARSAR_PLUGIN_API__
 *
 * Each plugin bundle is expected to call:
 *   const { React, definePlugin } = window.__PARSAR_PLUGIN_API__
 *   definePlugin("@internal/my-plugin", (ctx) => { ctx.slots.register(...) })
 */

import { slotRegistry } from "./plugin-slots"

interface PluginManifest {
  name: string
  client_entry?: string
}

const loadedPlugins = new Set<string>()

/**
 * Load a single plugin's client bundle by name.
 * Idempotent: skips if already loaded.
 */
export async function loadPluginClient(pluginName: string): Promise<void> {
  if (loadedPlugins.has(pluginName)) return

  // Derive URL-safe name (strip @scope/ prefix for the path segment).
  const dirName = pluginName.includes("/")
    ? pluginName.slice(pluginName.lastIndexOf("/") + 1)
    : pluginName

  const url = `/api/v1/plugins/${encodeURIComponent(dirName)}/client.js`

  try {
    const resp = await fetch(url)
    if (!resp.ok) {
      console.warn(`[plugin-loader] failed to fetch client for "${pluginName}": ${resp.status}`)
      return
    }
    const code = await resp.text()

    // Execute the plugin code. It should call window.__PARSAR_PLUGIN_API__.definePlugin()
    // or access the API directly.
    const fn = new Function(code)
    fn()

    loadedPlugins.add(pluginName)
    console.info(`[plugin-loader] loaded client for "${pluginName}"`)
  } catch (err) {
    console.error(`[plugin-loader] error loading "${pluginName}":`, err)
  }
}

/**
 * Load all plugin clients that have a client_entry.
 * Called from the conversation view when plugins are resolved.
 */
export async function loadAllPluginClients(plugins: PluginManifest[]): Promise<void> {
  const withClient = plugins.filter((p) => p.client_entry)
  await Promise.allSettled(withClient.map((p) => loadPluginClient(p.name)))
}

/**
 * Unload a plugin (remove its slot registrations).
 */
export function unloadPlugin(pluginName: string): void {
  slotRegistry.unregisterPlugin(pluginName)
  loadedPlugins.delete(pluginName)
}

/**
 * Check if a plugin client is already loaded.
 */
export function isPluginLoaded(pluginName: string): boolean {
  return loadedPlugins.has(pluginName)
}
