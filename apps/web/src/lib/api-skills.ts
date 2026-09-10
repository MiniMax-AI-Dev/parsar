import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import { KEY_CAPABILITIES_WORKSPACE, KEY_CAPABILITY_VERSIONS } from "./api-capabilities"
import type { Capability, CapabilityVersion } from "./api-types"
import type { ImportPreviewResponse } from "../pages/admin/capabilities/types"

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
const KEY_INSTALLED_SKILLS = (workspaceID: string) => [...KEY_CAPABILITIES_WORKSPACE(workspaceID), "skillsInstalled"] as const

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

export function useSkillPreview(workspaceID: string | null, skill: SkillsCatalogItem, enabled: boolean) {
  return useQuery({
    queryKey: ["admin", "skillPreview", workspaceID, skill.source, skill.slug, skill.id],
    queryFn: ({ signal }) => apiRequest<ImportPreviewResponse & { entry_markdown: string }>(
      `/api/v1/workspaces/${encodeURIComponent(workspaceID!)}/skills/preview`,
      {
        method: "POST",
        signal,
        body: { source: skill.source, slug: skill.slug, registry_id: skill.id, registry: SKILLS_REGISTRY },
      },
    ),
    enabled: !!workspaceID && enabled,
    retry: false,
    staleTime: 60_000,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })
}

export function useInstalledSkills(workspaceID: string | null) {
  return useQuery({
    queryKey: KEY_INSTALLED_SKILLS(workspaceID ?? "_none"),
    queryFn: () => workspaceID
      ? apiRequest<Record<string, string>>(`/api/v1/workspaces/${encodeURIComponent(workspaceID)}/skills/installed`)
      : Promise.resolve({} as Record<string, string>),
    retry: noUnreachableRetry,
    // Recheck on entry, including changes made from another page or session.
    staleTime: 0,
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
    onSuccess: async (result) => {
      const installedWorkspaceID = result.capability.workspace_id
      await qc.cancelQueries({ queryKey: KEY_INSTALLED_SKILLS(installedWorkspaceID) })
      void qc.invalidateQueries({ queryKey: KEY_CAPABILITIES_WORKSPACE(installedWorkspaceID) })
      void qc.invalidateQueries({ queryKey: ["admin", "capability"] })
      void qc.invalidateQueries({
        queryKey: KEY_CAPABILITY_VERSIONS(installedWorkspaceID, result.capability.id),
      })
    },
  })
}
