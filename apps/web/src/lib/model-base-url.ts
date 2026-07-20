export function hostFromBaseURL(baseURL: string): string {
  const trimmed = baseURL.trim()
  if (!trimmed) return ""
  try {
    return new URL(trimmed).host
  } catch {
    return trimmed.replace(/^https?:\/\//, "").split("/")[0] ?? trimmed
  }
}
