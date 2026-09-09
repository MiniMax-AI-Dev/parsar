/** Only explicit tool failure signals determine presentation, not output text. */
export function isFailedToolResult(result: unknown): boolean {
  if (!result || typeof result !== "object" || Array.isArray(result)) return false
  const value = result as Record<string, unknown>
  return value.status === "failed" || value.is_error === true || value.isError === true
}
