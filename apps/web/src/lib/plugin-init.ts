/**
 * Plugin system initialization — exposes the shared React instance and
 * plugin registration API on window so client bundles can access them.
 *
 * Must be imported early in main.tsx (before any plugin loading happens).
 */

import * as React from "react"
import { createPluginClientContext, type ParsarPluginAPI } from "./plugin-slots"

const api: ParsarPluginAPI = {
  React,
  createContext: createPluginClientContext,
  definePlugin(pluginName, setup) {
    const ctx = createPluginClientContext(pluginName)
    setup(ctx)
  },
}

window.__PARSAR_PLUGIN_API__ = api
