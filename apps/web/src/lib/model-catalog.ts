import { useQuery } from "@tanstack/react-query"
import { apiRequest } from "./api-client"
import { coreHarnessLabel, type CoreHarness } from "./core-api"
export interface ModelProvider { id: string; name: string; protocol: "anthropic" | "responses"; base_url: string; key_configured: boolean }
export interface CatalogModel { id: string; name: string; model_key: string; provider_id: string; provider_name: string; protocol: ModelProvider["protocol"]; context_window: number; max_output_tokens: number }
export const catalogKey = (workspace: string | null) => ["model-catalog", workspace] as const
export const catalogPath = (workspace: string, resource: "models" | "model-providers", id?: string) => `/api/v1/workspaces/${encodeURIComponent(workspace)}/${resource}${id ? `/${encodeURIComponent(id)}` : ""}`
export function useModelCatalog(workspace: string | null) {
  return useQuery({ queryKey: catalogKey(workspace), enabled: !!workspace, queryFn: async () => {
    const [providers, models] = await Promise.all([
      apiRequest<{ providers: ModelProvider[] }>(catalogPath(workspace!, "model-providers")),
      apiRequest<{ models: CatalogModel[] }>(catalogPath(workspace!, "models")),
    ])
    return { ...providers, ...models }
  } })
}
export function supportsCatalogModel(model: CatalogModel, harness: CoreHarness | ""): boolean {
  if (!harness) return true
  if (harness === "codex") return model.protocol === "responses"
  return model.protocol === "anthropic" && (harness !== "mcode" || (model.context_window > 0 && model.max_output_tokens > 0))
}

export function catalogHarnessLabels(model: CatalogModel): string {
  return (["codex", "claude_sdk", "mcode"] as const).filter(harness => supportsCatalogModel(model, harness)).map(coreHarnessLabel).join(" · ")
}
