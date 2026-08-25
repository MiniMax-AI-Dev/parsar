/**
 * Plugin Slot Registry — the core client-side plugin system.
 *
 * Plugins register React components to named "slots" in the UI. The main
 * app renders SlotRenderer at each slot position, which queries this registry
 * and renders the appropriate plugin component.
 *
 * Slot types:
 *   single  — only the last registration wins (replace entire area)
 *   list    — all registrations render in order
 *   chain   — first registration whose `match` returns truthy wins
 *
 * Standard slot IDs:
 *   workspace.main              — replace entire workspace (single)
 *   agent.workspace             — replace agent right panel (single)
 *   conversation.tool-card      — custom tool result card (chain)
 *   conversation.header.actions — header action buttons (list)
 *   conversation.input.dock     — above-input panel (list)
 *   conversation.composer.left  — left of input (list)
 *   conversation.composer.right — right of input (list)
 *   agent.settings.section      — agent settings extensions (list)
 */

import type { ComponentType } from "react"

// ─── Types ──────────────────────────────────────────────────────────────────

export type SlotType = "single" | "list" | "chain"

export interface SlotRegistration {
  /** Unique key for this registration (plugin dedup). */
  key: string
  /** The owning plugin name. */
  pluginName: string
  /** The React component to render. */
  component: ComponentType<any>
  /** For "chain" slots: return truthy data to claim rendering. */
  match?: (props: any) => any
  /** Sort order for "list" slots. Lower = earlier. Default 0. */
  order?: number
}

export interface SlotDefinition {
  id: string
  type: SlotType
}

// ─── Registry ───────────────────────────────────────────────────────────────

/** Predefined slot definitions. */
export const SLOT_DEFINITIONS: Record<string, SlotDefinition> = {
  // Full-page slots
  "workspace.main": { id: "workspace.main", type: "single" },
  "workspace.content": { id: "workspace.content", type: "single" },
  "agent.workspace": { id: "agent.workspace", type: "single" },
  // Layout extension slots
  "layout.header.actions": { id: "layout.header.actions", type: "list" },
  "layout.nav.bottom": { id: "layout.nav.bottom", type: "list" },
  // Conversation slots
  "conversation.tool-card": { id: "conversation.tool-card", type: "chain" },
  "conversation.header.actions": { id: "conversation.header.actions", type: "list" },
  "conversation.input.dock": { id: "conversation.input.dock", type: "list" },
  "conversation.composer.left": { id: "conversation.composer.left", type: "list" },
  "conversation.composer.right": { id: "conversation.composer.right", type: "list" },
  // Agent extension slots
  "agent.settings.section": { id: "agent.settings.section", type: "list" },
}

class PluginSlotRegistry {
  private slots = new Map<string, SlotRegistration[]>()
  private sortedCache = new Map<string, SlotRegistration[]>()
  private listeners = new Set<() => void>()
  private version = 0

  /** Register a component to a slot. */
  register(slotId: string, reg: Omit<SlotRegistration, "pluginName">, pluginName: string): void {
    const full: SlotRegistration = { ...reg, pluginName }
    const list = this.slots.get(slotId) ?? []

    // Dedup by key: replace if same key exists.
    const idx = list.findIndex((r) => r.key === full.key)
    if (idx >= 0) {
      list[idx] = full
    } else {
      list.push(full)
    }

    this.slots.set(slotId, list)
    this.sortedCache.delete(slotId)
    this.notify()
  }

  /** Remove all registrations from a specific plugin. */
  unregisterPlugin(pluginName: string): void {
    let changed = false
    for (const [slotId, list] of this.slots) {
      const filtered = list.filter((r) => r.pluginName !== pluginName)
      if (filtered.length !== list.length) {
        this.slots.set(slotId, filtered)
        this.sortedCache.delete(slotId)
        changed = true
      }
    }
    if (changed) this.notify()
  }

  /** Get all registrations for a slot, sorted by order. Cached for React stability. */
  getRegistrations(slotId: string): SlotRegistration[] {
    const cached = this.sortedCache.get(slotId)
    if (cached) return cached

    const list = this.slots.get(slotId) ?? []
    const sorted = [...list].sort((a, b) => (a.order ?? 0) - (b.order ?? 0))
    this.sortedCache.set(slotId, sorted)
    return sorted
  }

  /** For chain slots: find the first registration whose match() returns truthy. */
  matchChain(slotId: string, props: any): { registration: SlotRegistration; data: any } | null {
    const regs = this.getRegistrations(slotId)
    for (const reg of regs) {
      if (!reg.match) continue
      const data = reg.match(props)
      if (data) return { registration: reg, data }
    }
    return null
  }

  /** Subscribe to registry changes (for React re-renders). */
  subscribe(listener: () => void): () => void {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }

  /** Get a version number for useSyncExternalStore snapshot comparison. */
  getVersion(): number {
    return this.version
  }

  private notify(): void {
    this.version++
    for (const fn of this.listeners) fn()
  }
}

/** The singleton slot registry instance. */
export const slotRegistry = new PluginSlotRegistry()

// ─── Plugin API (exposed on window) ────────────────────────────────────────

/**
 * The PluginClientContext provided to each plugin's client entry.
 * Plugin code calls ctx.slots.register(...) to inject UI.
 */
export interface PluginClientContext {
  slots: {
    register(
      slotId: string,
      registration: {
        key: string
        component: ComponentType<any>
        match?: (props: any) => any
        order?: number
      }
    ): void
  }
}

/**
 * Creates a PluginClientContext for a specific plugin.
 * Called by the plugin loader before executing each plugin's client.js.
 */
export function createPluginClientContext(pluginName: string): PluginClientContext {
  return {
    slots: {
      register(slotId, registration) {
        slotRegistry.register(slotId, registration, pluginName)
      },
    },
  }
}

// ─── Window global API ──────────────────────────────────────────────────────

export interface ParsarPluginAPI {
  /** React library — shared with plugins so they don't bundle their own. */
  React: typeof import("react")
  /** Create a plugin context (used internally by the loader). */
  createContext: (pluginName: string) => PluginClientContext
  /** Convenience: directly register a plugin's default export. */
  definePlugin: (
    pluginName: string,
    setup: (ctx: PluginClientContext) => void
  ) => void
}

declare global {
  interface Window {
    __PARSAR_PLUGIN_API__?: ParsarPluginAPI
  }
}
