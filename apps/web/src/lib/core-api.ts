import { useInfiniteQuery } from "@tanstack/react-query"
import { apiRequest } from "./api-client"

export type CoreEnvironment =
  | { type: "openai_hosted"; environment_template_id?: string }
  | { type: "self_hosted"; workspace_directory: string; capability_directories?: string[] }
  | { type: "none" }
export interface CoreAgentConfig {
  model: string
  service_tier?: "auto" | "default" | "flex" | "priority" | "fast" | null
  tools?: Record<string, unknown>[] | null
  multi_agent?: Record<string, unknown> | null
  reasoning?: Record<string, unknown> | null
  text?: Record<string, unknown> | null
}
export interface CoreTemplate {
  id: string
  name?: string | null
  created_at: number
  network?: { access: "enabled" | "restricted" | "disabled"; allowed_domains?: string[] }
  packages?: { python?: string[]; npm?: string[]; system?: string[] }
}
export interface CoreTemplateInput extends Record<string, unknown> {
  name?: string | null
  network?: CoreTemplate["network"]
  packages?: CoreTemplate["packages"]
  setup_commands?: { command: string; cwd?: string }[]
  env?: Record<string, string>
}
interface TemplatePage { data: CoreTemplate[]; has_more: boolean; last_id?: string }
const path = (workspaceID: string) => `/api/v1/workspaces/${encodeURIComponent(workspaceID)}/core/environments/templates`
export const coreTemplateKey = (workspaceID: string | null) => ["core", "templates", workspaceID] as const
export function useCoreTemplates(workspaceID: string | null, enabled = true) {
  return useInfiniteQuery({
    queryKey: coreTemplateKey(workspaceID), enabled: !!workspaceID && enabled, retry: false,
    initialPageParam: "",
    queryFn: ({ pageParam }) => apiRequest<TemplatePage>(path(workspaceID!), { query: { after: pageParam || undefined } }),
    getNextPageParam: (page) => page.has_more ? page.last_id ?? page.data.at(-1)?.id : undefined,
  })
}
export function saveCoreTemplate(workspaceID: string, body: CoreTemplateInput, id?: string) {
  return apiRequest<CoreTemplate>(`${path(workspaceID)}${id ? `/${encodeURIComponent(id)}` : ""}`, {
    method: "POST", body,
  })
}
export function deleteCoreTemplate(workspaceID: string, id: string) {
  return apiRequest(`${path(workspaceID)}/${encodeURIComponent(id)}`, { method: "DELETE" })
}
export function requestStartSession(agentID?: string) {
  window.dispatchEvent(new CustomEvent("core:start-session", { detail: agentID }))
}
export function jsonObject(text: string): Record<string, unknown> {
  const value: unknown = JSON.parse(text || "{}")
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Expected a JSON object")
  return value as Record<string, unknown>
}
