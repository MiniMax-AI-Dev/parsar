/**
 * Provider presets for the Add-Model dialog.
 *
 * The catalog is a deliberately small, hand-vetted whitelist. It is loaded as
 * JSON at frontend startup so product builds can refresh the list without
 * touching dialog code.
 */
import catalogFallback from "./model-presets.catalog.json"
import catalogUrl from "./model-presets.catalog.json?url"

export interface ModelPreset {
  id: string
  name: string
  /** Context window in tokens; null when unknown. */
  context: number | null
  /** USD per 1M tokens; null when unpriced. */
  costIn: number | null
  costOut: number | null
  tool: boolean
  reasoning: boolean
  vision: boolean
}

/** Wire protocol a provider endpoint speaks. Drives which agent engine
 * (claude_code=anthropic, codex=openai, pi=any) a model can attach to. */
export type ProtocolID = "anthropic" | "openai" | "google"

export interface ProtocolPreset {
  id: ProtocolID
  /** npm adapter package for this protocol (== Model.adapter). */
  adapter: string
  /** Endpoint base URL for this protocol. Empty when the first-party SDK
   * already knows the default. */
  baseURL: string
}

export interface ProviderPreset {
  /** Provider id, used as provider_type. */
  key: string
  name: string
  /** npm adapter package (== Model.adapter). Mirrors protocols[0].adapter. */
  adapter: string
  /** Empty when the first-party SDK already knows the default endpoint.
   * Mirrors protocols[0].baseURL. */
  defaultBaseURL: string
  docUrl: string
  /** Gateway-style adapter -> show the custom-headers editor. */
  customHeaders: boolean
  /** anthropic-compatible gateway -> show the auth-scheme selector. */
  authSchemeSelector: boolean
  /** One entry per wire protocol the provider serves. A provider with more
   * than one lets the Add-Model dialog toggle protocol (e.g. MiniMax serves
   * Anthropic at /anthropic and OpenAI at /v1). Always non-empty; a
   * single-protocol provider carries exactly one entry. */
  protocols: ProtocolPreset[]
  models: ModelPreset[]
}

export const PROVIDER_CATALOG: ProviderPreset[] = normalizeProviderCatalog(catalogFallback)

let providerCatalogSnapshot = PROVIDER_CATALOG
let providerCatalogPromise: Promise<ProviderPreset[]> | null = null

export function getProviderCatalogSnapshot(): ProviderPreset[] {
  return providerCatalogSnapshot
}

export function prefetchProviderCatalog(): void {
  void loadProviderCatalog()
}

export function loadProviderCatalog(): Promise<ProviderPreset[]> {
  if (providerCatalogPromise) return providerCatalogPromise
  if (typeof fetch !== "function") return Promise.resolve(providerCatalogSnapshot)

  providerCatalogPromise = fetch(catalogUrl, { cache: "no-store" })
    .then((res) => {
      if (!res.ok) throw new Error(`GET ${catalogUrl} failed: ${res.status}`)
      return res.json() as Promise<unknown>
    })
    .then((payload) => {
      const catalog = normalizeProviderCatalog(payload)
      if (catalog.length === 0) throw new Error("model preset catalog is empty")
      providerCatalogSnapshot = catalog
      return providerCatalogSnapshot
    })
    .catch((err) => {
      console.warn("Failed to load model preset catalog; using fallback presets", err)
      providerCatalogSnapshot = PROVIDER_CATALOG
      return providerCatalogSnapshot
    })

  return providerCatalogPromise
}

/** Short "200K · $0.14/$0.28" caption for a model, omitting null parts. */
export function modelCaption(m: ModelPreset): string {
  const parts: string[] = []
  if (m.context != null) {
    parts.push(m.context >= 1000 ? `${Math.round(m.context / 1000)}K` : String(m.context))
  }
  if (m.costIn != null && m.costOut != null) {
    parts.push(`$${m.costIn}/$${m.costOut}`)
  }
  return parts.join(" · ")
}

function normalizeProviderCatalog(payload: unknown): ProviderPreset[] {
  if (!Array.isArray(payload)) return []
  return payload
    .map((item) => (isRecord(item) ? normalizeProvider(item) : null))
    .filter((provider): provider is ProviderPreset => provider !== null)
}

function normalizeProvider(item: Record<string, unknown>): ProviderPreset | null {
  const key = stringOrNull(item.key)
  const name = stringOrNull(item.name)
  const adapter = stringOrNull(item.adapter)
  if (!key || !name || !adapter) return null

  const defaultBaseURL = stringOrNull(item.defaultBaseURL) ?? ""
  const protocols = normalizeProtocols(item.protocols, adapter, defaultBaseURL)

  return {
    key,
    name,
    // Keep top-level adapter/defaultBaseURL aligned with the default
    // (first) protocol so back-compat readers stay correct.
    adapter: protocols[0].adapter,
    defaultBaseURL: protocols[0].baseURL,
    docUrl: stringOrNull(item.docUrl) ?? "",
    customHeaders: item.customHeaders === true,
    authSchemeSelector: item.authSchemeSelector === true,
    protocols,
    models: Array.isArray(item.models) ? normalizeModels(item.models) : [],
  }
}

/** Normalize the protocols array, falling back to a single synthesized entry
 * from the legacy adapter + defaultBaseURL fields so a catalog without an
 * explicit `protocols` block still loads. */
function normalizeProtocols(
  payload: unknown,
  fallbackAdapter: string,
  fallbackBaseURL: string,
): ProtocolPreset[] {
  const out: ProtocolPreset[] = []
  if (Array.isArray(payload)) {
    for (const item of payload) {
      if (!isRecord(item)) continue
      const id = protocolIDOrNull(item.id)
      const adapter = stringOrNull(item.adapter)
      if (!id || !adapter) continue
      out.push({ id, adapter, baseURL: stringOrNull(item.baseURL) ?? "" })
    }
  }
  if (out.length > 0) return out
  return [
    {
      id: protocolIDForAdapter(fallbackAdapter),
      adapter: fallbackAdapter,
      baseURL: fallbackBaseURL,
    },
  ]
}

function protocolIDOrNull(value: unknown): ProtocolID | null {
  if (value === "anthropic" || value === "openai" || value === "google") return value
  return null
}

/** Derive a wire protocol from an adapter package name. Mirrors the
 * provider_type/adapter allow-lists in
 * server/internal/connector/agentdaemon/model_injection.go. */
function protocolIDForAdapter(adapter: string): ProtocolID {
  const a = adapter.toLowerCase()
  if (a.includes("anthropic")) return "anthropic"
  if (a.includes("google")) return "google"
  return "openai"
}

function normalizeModels(items: unknown[]): ModelPreset[] {
  return items
    .map((item) => (isRecord(item) ? normalizeModel(item) : null))
    .filter((model): model is ModelPreset => model !== null)
}

function normalizeModel(item: Record<string, unknown>): ModelPreset | null {
  const id = stringOrNull(item.id)
  const name = stringOrNull(item.name) ?? id
  if (!id || !name) return null

  return {
    id,
    name,
    context: numberOrNull(item.context),
    costIn: numberOrNull(item.costIn),
    costOut: numberOrNull(item.costOut),
    tool: item.tool === true,
    reasoning: item.reasoning === true,
    vision: item.vision === true,
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === "object" && !Array.isArray(value)
}

function stringOrNull(value: unknown): string | null {
  if (typeof value !== "string") return null
  const trimmed = value.trim()
  return trimmed === "" ? null : trimmed
}

function numberOrNull(value: unknown): number | null {
  if (typeof value === "number" && Number.isFinite(value)) return value
  return null
}
