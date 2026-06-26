import type { ApiErrorEnvelope } from "./api-types"

/**
 * Minimal typed fetch wrapper for the Parsar admin API. On network failures
 * (server down / CORS / DNS) surfaces an `ApiError` with `unreachable: true`
 * so UI can switch to a friendlier "server offline" affordance.
 */

export class ApiError extends Error {
  readonly envelope: ApiErrorEnvelope
  constructor(env: ApiErrorEnvelope) {
    super(env.message)
    this.envelope = env
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "DELETE" | "PUT" | "PATCH"
  body?: unknown
  query?: Record<string, string | number | boolean | undefined>
  /** Extra request headers; merged on top of defaults (Content-Type when
   *  body present, Accept always). */
  headers?: Record<string, string>
  /** Abort signal */
  signal?: AbortSignal
}

function buildURL(path: string, query?: RequestOptions["query"]): string {
  if (!query) return path
  const parts: string[] = []
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined) continue
    parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`)
  }
  if (parts.length === 0) return path
  const sep = path.includes("?") ? "&" : "?"
  return `${path}${sep}${parts.join("&")}`
}

export async function apiRequest<T>(
  path: string,
  opts: RequestOptions = {}
): Promise<T> {
  const url = buildURL(path, opts.query)
  let res: Response
  try {
    res = await fetch(url, {
      method: opts.method ?? "GET",
      signal: opts.signal,
      headers: {
        ...(opts.body
          ? { "Content-Type": "application/json", Accept: "application/json" }
          : { Accept: "application/json" }),
        ...(opts.headers ?? {}),
      },
      body: opts.body ? JSON.stringify(opts.body) : undefined,
    })
  } catch (err) {
    // network / CORS / proxy down
    throw new ApiError({
      status: 0,
      code: "network_error",
      message: err instanceof Error ? err.message : "fetch failed",
      unreachable: true,
    })
  }

  if (!res.ok) {
    let bodyMsg = res.statusText
    let bodyCode = `http_${res.status}`
    let bodyText: string | null = null
    try {
      const ct = res.headers.get("content-type") ?? ""
      if (ct.includes("application/json")) {
        const j = await res.json()
        if (j && typeof j === "object" && "error" in j) {
          bodyCode = String(j.error)
          // Server envelope is { error: <code>, message: <human text> }.
          // Prefer `message` so the UI shows the human text instead of
          // the bare error code.
          const rawMsg = (j as { message?: unknown }).message
          if (typeof rawMsg === "string" && rawMsg.trim() !== "") {
            bodyMsg = rawMsg
          } else {
            bodyMsg = bodyCode
          }
        }
      } else {
        bodyText = await res.text()
      }
    } catch {
      /* ignore body parse error */
    }

    // Vite proxy returns a plain-text "Not found" 404 when the upstream Go
    // server is offline (ECONNREFUSED). Treat that as unreachable.
    const looksLikeViteProxyMiss =
      res.status === 404 && bodyText?.toLowerCase().trim() === "not found"

    if (res.status === 401 && path !== "/api/v1/me" && typeof window !== "undefined") {
      window.location.assign("/login")
    }

    throw new ApiError({
      status: res.status,
      code: bodyCode,
      message: bodyMsg,
      unreachable:
        res.status === 503 ||
        bodyCode === "server_unreachable" ||
        looksLikeViteProxyMiss,
    })
  }

  // 204 / empty
  if (res.status === 204) return undefined as T
  const contentType = res.headers.get("content-type") ?? ""
  if (!contentType.includes("application/json")) {
    return undefined as T
  }
  return (await res.json()) as T
}

/**
 * Shared React Query `retry` strategy: skip retries when the server is
 * unreachable, otherwise allow one retry for transient errors.
 */
export function noUnreachableRetry(failureCount: number, error: unknown): boolean {
  if (error instanceof ApiError && error.envelope.unreachable) return false
  return failureCount < 1
}
