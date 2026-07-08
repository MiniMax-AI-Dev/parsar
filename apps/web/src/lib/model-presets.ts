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

export interface ProviderPreset {
  /** Provider id, used as provider_type. */
  key: string
  name: string
  /** npm adapter package (== Model.adapter). */
  adapter: string
  /** Empty when the first-party SDK already knows the default endpoint. */
  defaultBaseURL: string
  docUrl: string
  /** Gateway-style adapter -> show the custom-headers editor. */
  customHeaders: boolean
  /** anthropic-compatible gateway -> show the auth-scheme selector. */
  authSchemeSelector: boolean
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

export function findProvider(key: string): ProviderPreset | undefined {
  return providerCatalogSnapshot.find((p) => p.key === key)
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

  return {
    key,
    name,
    adapter,
    defaultBaseURL: stringOrNull(item.defaultBaseURL) ?? "",
    docUrl: stringOrNull(item.docUrl) ?? "",
    customHeaders: item.customHeaders === true,
    authSchemeSelector: item.authSchemeSelector === true,
    models: Array.isArray(item.models) ? normalizeModels(item.models) : [],
  }
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
