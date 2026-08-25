/**
 * SlotRenderer — renders plugin-registered components at a named slot.
 *
 * Usage:
 *   <ToolCardSlot presentation={toolResult.presentation} fallback={<DefaultCard />} />
 *   <ListSlot slotId="conversation.composer.right" />
 */

import { Component, type ReactNode, useSyncExternalStore } from "react"
import { slotRegistry, type SlotRegistration } from "../../lib/plugin-slots"

// ─── Hook: subscribe to slot registry ───────────────────────────────────────

const subscribe = (cb: () => void) => slotRegistry.subscribe(cb)
const getSnapshot = () => slotRegistry.getVersion()

function useSlotRegistrations(slotId: string): SlotRegistration[] {
  // Subscribe to version changes; derive the list from the stable cache.
  useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
  return slotRegistry.getRegistrations(slotId)
}

// ─── ErrorBoundary ──────────────────────────────────────────────────────────

interface ErrorBoundaryProps {
  pluginName: string
  children: ReactNode
}
interface ErrorBoundaryState {
  error: Error | null
}

class PluginErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  render() {
    if (this.state.error) {
      return (
        <div className="rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-xs text-danger-emphasis">
          Plugin "{this.props.pluginName}" crashed: {this.state.error.message}
        </div>
      )
    }
    return this.props.children
  }
}

// ─── ToolCardSlot (chain type) ──────────────────────────────────────────────

interface ToolCardSlotProps {
  /** The presentation metadata from the tool result. */
  presentation?: { kind?: string; data?: unknown } | null
  /** Raw tool result content (fallback text). */
  content?: string
  /** Rendered when no plugin claims the presentation. */
  fallback?: ReactNode
}

/**
 * Renders a plugin-registered tool card if a plugin's match() claims the
 * presentation. Falls back to the default rendering otherwise.
 */
export function ToolCardSlot({ presentation, content, fallback }: ToolCardSlotProps) {
  if (!presentation?.kind) return <>{fallback}</>

  const match = slotRegistry.matchChain("conversation.tool-card", { presentation, content })
  if (!match) return <>{fallback}</>

  const { registration, data } = match
  const PluginComponent = registration.component

  return (
    <PluginErrorBoundary pluginName={registration.pluginName}>
      <PluginComponent data={data} presentation={presentation} content={content} />
    </PluginErrorBoundary>
  )
}

// ─── ListSlot ───────────────────────────────────────────────────────────────

interface ListSlotProps {
  slotId: string
  /** Extra props passed to every registered component. */
  context?: Record<string, unknown>
}

/**
 * Renders all plugin-registered components for a "list" slot, in order.
 */
export function ListSlot({ slotId, context }: ListSlotProps) {
  const registrations = useSlotRegistrations(slotId)
  if (registrations.length === 0) return null

  return (
    <>
      {registrations.map((reg) => {
        const PluginComponent = reg.component
        return (
          <PluginErrorBoundary key={reg.key} pluginName={reg.pluginName}>
            <PluginComponent {...(context ?? {})} />
          </PluginErrorBoundary>
        )
      })}
    </>
  )
}

// ─── SingleSlot ─────────────────────────────────────────────────────────────

interface SingleSlotProps {
  slotId: string
  /** Extra props passed to the registered component. */
  context?: Record<string, unknown>
  /** Rendered when no plugin has registered for this slot. */
  fallback?: ReactNode
}

/**
 * Renders the last-registered plugin component for a "single" slot.
 * Falls back to children when no registration exists.
 */
export function SingleSlot({ slotId, context, fallback }: SingleSlotProps) {
  const registrations = useSlotRegistrations(slotId)
  if (registrations.length === 0) return <>{fallback}</>

  // Single slot: last registration wins.
  const reg = registrations[registrations.length - 1]
  const PluginComponent = reg.component

  return (
    <PluginErrorBoundary pluginName={reg.pluginName}>
      <PluginComponent {...(context ?? {})} />
    </PluginErrorBoundary>
  )
}
