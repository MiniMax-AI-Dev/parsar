export function isRuntimeCapabilityError(kind?: string, metadata?: Record<string, unknown>): boolean {
  return kind === "runtime_error" || (kind === "error" && metadata?.kind === "runtime_error")
}
