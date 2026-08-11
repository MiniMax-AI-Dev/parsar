import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import { KEY_CAPABILITIES_WORKSPACE, KEY_CAPABILITY_VERSIONS } from "./api-capabilities"
import type { Capability, CapabilityVersion } from "./api-types"

// Browser-safe equivalent of:
// curl -L https://agent-skill-index.vercel.app/api/skills
// The API endpoint redirects here, but the redirect response itself does not
// include CORS headers, so browser fetch fails before reaching this JSON.
const SKILLS_CATALOG_URL = "https://agent-skill-index.vercel.app/data/latest/skills.json"
const SKILLS_REGISTRY = "skills.sh"
const SAFE_SKILL_REF_PART = /^[A-Za-z0-9._-]+$/

export interface SkillsCatalogItem {
  rank?: number
  id: string
  slug: string
  source: string
  name: string
  installs?: number
  sourceType?: string
  source_type?: string
  installUrl?: string | null
  install_url?: string | null
  url?: string | null
}

export interface SkillsCatalogResponse {
  items: SkillsCatalogItem[]
  generatedAt?: string
  count?: number
}

export interface InstallSkillResponse {
  capability: Capability
  capability_version: CapabilityVersion
  created_secret_ids: string[]
}

export const KEY_SKILLS_CATALOG = ["admin", "skillsCatalog", "v2"] as const

async function listSkillsCatalog(): Promise<SkillsCatalogResponse> {
  const res = await fetch(SKILLS_CATALOG_URL, { headers: { Accept: "application/json" } })
  if (!res.ok) throw new Error(`Skills catalog request failed: HTTP ${res.status}`)
  const payload = await res.json()
  const rawItems: unknown[] = Array.isArray(payload)
    ? payload
    : Array.isArray(payload?.data)
      ? payload.data
      : Array.isArray(payload?.items)
        ? payload.items
        : []
  return {
    generatedAt: typeof payload?.generatedAt === "string" ? payload.generatedAt : undefined,
    count: typeof payload?.count === "number" ? payload.count : rawItems.length,
    items: rawItems.map(normalizeSkillItem).filter(isInstallableCatalogItem),
  }
}

function normalizeSkillItem(raw: unknown): SkillsCatalogItem {
  const item = raw && typeof raw === "object" ? (raw as Record<string, unknown>) : {}
  const source = stringField(item.source)
  const slug = stringField(item.slug)
  const id = stringField(item.id) || [source, slug].filter(Boolean).join("/")
  const installUrl = nullableStringField(item.installUrl) ?? nullableStringField(item.install_url)
  return {
    rank: numberField(item.rank),
    id,
    slug,
    source,
    name: stringField(item.name) || slug || id,
    installs: numberField(item.installs),
    sourceType: stringField(item.sourceType) || stringField(item.source_type),
    installUrl,
    url: nullableStringField(item.url),
  }
}

function isInstallableCatalogItem(item: SkillsCatalogItem): boolean {
  if (!item.source || !item.slug) return false
  const sourceParts = item.source.split("/")
  if (sourceParts.length !== 2 || !sourceParts.every(isSafeSkillRefPart)) return false
  if (!isSafeSkillRefPart(item.slug)) return false
  return true
}

function isSafeSkillRefPart(value: string): boolean {
  return value !== "." && value !== ".." && SAFE_SKILL_REF_PART.test(value)
}

function stringField(value: unknown): string {
  return typeof value === "string" ? value.trim() : ""
}

function nullableStringField(value: unknown): string | null {
  const text = stringField(value)
  return text || null
}

function numberField(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

async function installSkill(
  workspaceID: string,
  skill: SkillsCatalogItem,
): Promise<InstallSkillResponse> {
  return apiRequest<InstallSkillResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/skills/install`,
    {
      method: "POST",
      body: {
        source: skill.source,
        slug: skill.slug,
        registry_id: skill.id || `${skill.source}/${skill.slug}`,
        registry: SKILLS_REGISTRY,
      },
    },
  )
}

export function useSkillsCatalog() {
  return useQuery({
    queryKey: KEY_SKILLS_CATALOG,
    queryFn: listSkillsCatalog,
    retry: noUnreachableRetry,
    staleTime: 60_000,
  })
}

export function useInstallSkill(workspaceID: string | null) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (skill: SkillsCatalogItem) => {
      if (!workspaceID) throw new Error("workspace is required")
      return installSkill(workspaceID, skill)
    },
    retry: noUnreachableRetry,
    onSuccess: (result) => {
      if (!workspaceID) return
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(workspaceID) })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
      void qc.invalidateQueries({
        queryKey: KEY_CAPABILITY_VERSIONS(workspaceID, result.capability.id),
      })
    },
  })
}
