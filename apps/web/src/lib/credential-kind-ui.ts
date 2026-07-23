/**
 * credential-kind-ui — label / option helpers for credential kinds.
 *
 * The source of truth is the `credential_kinds` table. The constants below
 * are a fallback dictionary (covers seed rows, lets callers render labels
 * offline); `useCredentialKindOptions` pulls the live list.
 *
 * Pickers should use the hook so newly-created kinds appear immediately.
 * Read-only labels can omit the kinds argument — fallback covers the seed
 * rows; unknown codes fall through to `fallback`.
 */
import { useMemo } from "react"
import { useQuery } from "@tanstack/react-query"

import { apiRequest, noUnreachableRetry } from "./api-client"
import type { RequiredCredential } from "./api-types"

/* ---------- fallback dictionary (matches the migration seed rows) ------- */

export const CREDENTIAL_KIND_LABELS = {
  github_pat: {
    zh: "GitHub 访问令牌",
    en: "GitHub Access Token",
  },
  slack_bot_token: {
    zh: "Slack Bot Token",
    en: "Slack Bot Token",
  },
  postgres_dsn: {
    zh: "Postgres 连接串",
    en: "Postgres DSN",
  },
  notion_integration: {
    zh: "Notion 集成 token",
    en: "Notion Integration Token",
  },
  mcp_oauth: {
    zh: "MCP OAuth",
    en: "MCP OAuth",
  },
  jira_api_token: {
    zh: "Jira API Token",
    en: "Jira API Token",
  },
} as const

export type KnownCredentialKind = keyof typeof CREDENTIAL_KIND_LABELS

export const CREDENTIAL_KIND_OPTIONS: KnownCredentialKind[] = [
  "github_pat",
  "slack_bot_token",
  "postgres_dsn",
  "notion_integration",
  "mcp_oauth",
  "jira_api_token",
]

export const CREDENTIAL_KIND_META: Record<KnownCredentialKind, { placeholder: { zh: string; en: string }; getUrl?: string }> = {
  github_pat: {
    placeholder: { zh: "ghp_ / github_pat_ / gho_", en: "ghp_ / github_pat_ / gho_" },
    getUrl: "https://github.com/settings/tokens",
  },
  slack_bot_token: {
    placeholder: { zh: "xoxb-…", en: "xoxb-…" },
    getUrl: "https://api.slack.com/apps",
  },
  postgres_dsn: {
    placeholder: { zh: "postgres://user:pass@host:5432/db", en: "postgres://user:pass@host:5432/db" },
  },
  notion_integration: {
    placeholder: { zh: "secret_…", en: "secret_…" },
    getUrl: "https://www.notion.so/profile/integrations",
  },
  mcp_oauth: {
    placeholder: { zh: "通过连接器目录授权", en: "Authorize from the connector directory" },
  },
  jira_api_token: {
    placeholder: { zh: "ATATT…", en: "ATATT…" },
    getUrl: "https://id.atlassian.com/manage-profile/security/api-tokens",
  },
}

/* ---------- live (API) credential_kinds --------------------------------- */

/**
 * Minimal CredentialKindRead — duplicated from capabilities/types.ts to
 * keep this lib file free of UI-package imports.
 */
export interface CredentialKind {
  id: string
  code: string
  display_name: string
  description: string
  built_in: boolean
  source?: CredentialKindSource
}

export type CredentialKindSource = "platform_oauth" | "platform_model" | "user_defined"

interface ListCredentialKindsResponse {
  items: CredentialKind[]
}

const KEY_CREDENTIAL_KINDS_GLOBAL = (workspaceID: string) =>
  ["credentialKinds", workspaceID] as const

async function getCredentialKinds(workspaceID: string): Promise<ListCredentialKindsResponse> {
  return apiRequest<ListCredentialKindsResponse>(
    `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/credential-kinds`,
  )
}

/**
 * Hook returning the live credential_kinds list merged with the hardcoded
 * fallback so the picker isn't empty on cold load.
 */
export function useCredentialKindOptions(workspaceID: string | null) {
  const q = useQuery({
    queryKey: KEY_CREDENTIAL_KINDS_GLOBAL(workspaceID ?? "_none"),
    queryFn: () => getCredentialKinds(workspaceID as string),
    enabled: !!workspaceID,
    retry: noUnreachableRetry,
    staleTime: 60_000,
  })

  return useMemo(() => {
    const live = q.data?.items ?? []
    // Live entries win on duplicate code so an admin override of a kind's
    // display name takes effect immediately.
    const codes = new Set<string>()
    for (const k of live) codes.add(k.code)
    const merged: CredentialKind[] = [...live]
    for (const code of CREDENTIAL_KIND_OPTIONS) {
      if (codes.has(code)) continue
      const meta = CREDENTIAL_KIND_LABELS[code]
      merged.push({
        id: `seed-${code}`,
        code,
        display_name: meta.en, // language-resolution happens at render time
        description: "",
        built_in: true,
      })
    }
    return {
      kinds: merged,
      options: merged.map((k) => k.code),
      byCode: new Map(merged.map((k) => [k.code, k])),
      isLoading: q.isLoading,
      error: q.error,
    }
  }, [q.data, q.isLoading, q.error])
}

/* ---------- label helpers ----------------------------------------------- */

/**
 * Resolve a credential kind code to a human label.
 *
 * Resolution order:
 *   1. A live kind in `kinds` matching by code (display_name)
 *   2. The hardcoded fallback dictionary (zh/en split)
 *   3. The provided `fallback` string
 */
export function credentialKindLabel(
  kind: string | undefined,
  language: string,
  fallback: string,
  kinds?: CredentialKind[],
): string {
  if (!kind) return fallback
  if (kinds) {
    const hit = kinds.find((k) => k.code === kind)
    if (hit && hit.display_name) return hit.display_name
  }
  const meta = CREDENTIAL_KIND_LABELS[kind as keyof typeof CREDENTIAL_KIND_LABELS]
  if (meta) return language.toLowerCase().startsWith("zh") ? meta.zh : meta.en
  return fallback
}

export function requiredCredentialsLabel(
  creds: RequiredCredential[] | undefined,
  language: string,
  noneLabel: string,
  kinds?: CredentialKind[],
): string {
  if (!creds || creds.length === 0) return noneLabel
  return creds.map((rc) => credentialKindLabel(rc.kind, language, rc.kind, kinds)).join("、")
}
