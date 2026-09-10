export function httpAgentConfig(config?: Record<string, unknown> | null) {
  const nested = config?.http as Record<string, unknown> | undefined
  return {
    endpoint: String(config?.endpoint ?? nested?.endpoint ?? ""),
    secretID: String(config?.secret_id ?? nested?.secret_id ?? ""),
  }
}

export function validHTTPAgentEndpoint(value: string) {
  try {
    const url = new URL(value.trim())
    return ["http:", "https:"].includes(url.protocol) && Boolean(url.hostname) && !url.username && !url.password && !url.hash
  } catch { return false }
}
