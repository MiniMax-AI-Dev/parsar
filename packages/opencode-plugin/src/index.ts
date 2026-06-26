// @parsar/opencode-plugin
//
// Runs inside the `opencode serve` subprocess spawned by the Parsar
// OpenCode connector. Receives `permission.ask` events from opencode
// and long-polls Parsar's /_plugin/permission for an approve/deny
// verdict, then mutates `output.status` so the in-flight tool call
// proceeds or is blocked.
//
// HTTP (not opencode's event bus or a local socket) because RemotePool
// deployments may put the opencode subprocess on a different node from
// the Parsar server; HTTP composes cleanly with service meshes and
// telemetry. The held request per permission.ask is bounded by
// concurrent in-flight prompts that hit a permission boundary.

// Minimal local mirror of opencode's plugin type surface. We don't
// depend on `@opencode-ai/plugin` because that would pin us to a
// specific opencode SDK version; the real types come from opencode at
// runtime. The Permission shape mirrors opencode's
// `sdk/js/src/gen/types.gen.ts:423` — only the fields the callback
// consumes are typed.
export interface Permission {
  id: string
  type: string
  pattern?: string
  sessionID: string
  messageID?: string
  callID?: string
  title: string
  metadata?: Record<string, unknown>
  time?: { created?: number }
}

export interface PermissionAskOutput {
  status: "ask" | "deny" | "allow"
}

// Wire shape returned by Parsar's /_plugin/permission endpoint.
// Mirrors server/internal/connector/opencode/connector.go's
// PermissionVerdictWire.
interface VerdictResponse {
  status?: "allow" | "deny"
  reason?: string
}

// Strict subset of opencode's Hooks (only keys we implement) so a
// future hook addition cannot silently drop permission.ask.
export interface Hooks {
  "permission.ask"?: (input: Permission, output: PermissionAskOutput) => Promise<void>
}

export type Plugin = (input: unknown, options?: Record<string, unknown>) => Promise<Hooks>

/**
 * Env vars the plugin reads at load time.
 *
 * Routing intentionally uses `input.sessionID` from permission.ask
 * (indexed by the connector's inflight stream) rather than
 * PARSAR_RUN_ID / PARSAR_CONVERSATION_ID env values, because
 * LocalPool reuses an `opencode serve` subprocess across runs and
 * those env values would go stale after the second prompt.
 */
export const ENV_VARS = {
  /**
   * Parsar server URL (full path) where the plugin POSTs
   * permission asks. Example: `http://127.0.0.1:8080/dev/_plugin/permission`.
   */
  callbackUrl: "PARSAR_PLUGIN_CALLBACK_URL",
} as const

export interface ParsarPluginEnv {
  callbackUrl: string | undefined
}

export function readEnv(env: NodeJS.ProcessEnv = process.env): ParsarPluginEnv {
  return {
    callbackUrl: env[ENV_VARS.callbackUrl],
  }
}

/**
 * postVerdict sends the Permission payload to Parsar's callback URL
 * and returns the parsed verdict. Exposed for unit testing — production
 * callers go through the permission.ask hook below.
 *
 * On any failure (missing URL / fetch reject / non-200 / malformed
 * JSON) the verdict defaults to "deny" — fail-closed so a
 * misconfigured plugin can never silently approve a tool call.
 */
export async function postVerdict(
  callbackUrl: string | undefined,
  payload: PluginPermissionRequest,
  // Allow tests to inject a mock fetch without mutating globalThis.
  fetchImpl: typeof fetch = fetch,
): Promise<{ status: "allow" | "deny"; reason: string }> {
  if (!callbackUrl) {
    return { status: "deny", reason: "PARSAR_PLUGIN_CALLBACK_URL is unset" }
  }
  let res: Response
  try {
    res = await fetchImpl(callbackUrl, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(payload),
    })
  } catch (err) {
    return {
      status: "deny",
      reason: `permission callback fetch failed: ${(err as Error).message ?? String(err)}`,
    }
  }
  if (res.status !== 200) {
    let body = ""
    try {
      body = await res.text()
    } catch {
      body = "(unreadable)"
    }
    return {
      status: "deny",
      reason: `permission callback returned HTTP ${res.status}: ${body.slice(0, 200)}`,
    }
  }
  let parsed: VerdictResponse
  try {
    parsed = (await res.json()) as VerdictResponse
  } catch (err) {
    return {
      status: "deny",
      reason: `permission callback body not JSON: ${(err as Error).message ?? String(err)}`,
    }
  }
  if (parsed.status === "allow") {
    return { status: "allow", reason: parsed.reason ?? "" }
  }
  if (parsed.status === "deny") {
    return { status: "deny", reason: parsed.reason ?? "" }
  }
  return {
    status: "deny",
    reason: `permission callback returned unknown status ${JSON.stringify(parsed.status)}`,
  }
}

/**
 * Wire-format payload the plugin POSTs to Parsar. Mirrors
 * server/internal/connector/opencode/connector.go's PermissionAsk
 * (lower_snake_case json tags).
 */
export interface PluginPermissionRequest {
  id: string
  type: string
  pattern?: string
  session_id: string
  message_id?: string
  call_id?: string
  title: string
  metadata?: Record<string, unknown>
}

function toWireRequest(input: Permission): PluginPermissionRequest {
  return {
    id: input.id,
    type: input.type,
    pattern: input.pattern,
    session_id: input.sessionID,
    message_id: input.messageID,
    call_id: input.callID,
    title: input.title,
    metadata: input.metadata,
  }
}

/**
 * Default plugin export — entry point opencode loads when the
 * connector's opencode.json references this package. Registers the
 * permission.ask hook that long-polls Parsar for a human verdict.
 *
 * Fail-closed: any of missing env / fetch reject / non-200 /
 * unparseable body / unknown status maps to `output.status = "deny"`.
 *
 * Export shape: opencode's plugin loader requires an object with
 * `id` + `server: function` — exporting the function directly fails
 * with "Plugin export is not a function" and silently bypasses the
 * permission chain.
 */
const ParsarPlugin: Plugin = async (_input, _options) => {
  const env = readEnv()
  const callback = env.callbackUrl ?? "(unset)"
  process.stderr.write(
    `[parsar-plugin] loaded; callback=${callback}\n`,
  )
  const hooks: Hooks = {
    "permission.ask": async (input, output) => {
      const verdict = await postVerdict(env.callbackUrl, toWireRequest(input))
      output.status = verdict.status
      if (verdict.reason) {
        process.stderr.write(
          `[parsar-plugin] permission ${input.id} → ${verdict.status} (${verdict.reason})\n`,
        )
      }
    },
  }
  return hooks
}

export default {
  /**
   * Plugin id — required by opencode's loader for file-path plugins
   * (resolvePluginId throws "Path plugin <spec> must export id"
   * otherwise).
   */
  id: "@parsar/opencode-plugin",
  /**
   * Server-side entry point. opencode treats the default export as a
   * v1 PluginModule { id, server } and invokes server() to obtain the
   * Hooks object.
   */
  server: ParsarPlugin,
}
