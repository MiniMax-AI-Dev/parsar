import type { MCPDirectoryItem } from "../../../../lib/api-marketplace"

export function formatCommandPart(value: string): string {
  return /^[A-Za-z0-9_@%+=:,./-]+$/.test(value) ? value : JSON.stringify(value)
}

export function isConnectorConnectionActive(item: MCPDirectoryItem): boolean {
  return (
    item.connected &&
    item.connection_status !== "reconnect_required" &&
    item.connection_status !== "unavailable"
  )
}
