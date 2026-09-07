import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { Loader2, Search } from "lucide-react"

import { Badge } from "../../components/ui/badge"
import { Button } from "../../components/ui/button"
import { RailSection } from "../../components/ui/detail-rail"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { ErrorState } from "../../components/ui/error-state"
import { Field } from "../../components/ui/label"
import { Input } from "../../components/ui/input"
import { Select } from "../../components/ui/select"
import { StatusIcon } from "../../components/ui/status-icon"
import { ApiError } from "../../lib/api-client"
import type { Model, ModelCredentialMode, Secret } from "../../lib/api-types"
import {
  useImportProviderModels,
  type ImportProviderModelPreview,
  type ImportProviderModelsInput,
  type ImportProviderModelsResponse,
} from "../../lib/api-models"
import { endpointBaseURLsForProvider, type ProviderTypeOption } from "../../lib/model-provider-options"
import { CredentialKindCombobox } from "./capabilities/CredentialKindCombobox"

const IMPORT_PROVIDER: ProviderTypeOption = {
  key: "openai-compatible",
  adapter: "@ai-sdk/openai-compatible",
  defaultBaseURL: "",
  customHeaders: true,
  authSchemeSelector: false,
  labelKey: "models.createProvider.providerTypeLabel.openaiCompatible",
  protocols: [{ id: "openai", adapter: "@ai-sdk/openai-compatible", baseURL: "" }],
}

function errorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) return err.envelope.message || err.message
  if (err instanceof Error) return err.message
  return String(err)
}

function modelLabel(id: string): string {
  return id
    .split(/[-_:./]+/)
    .filter(Boolean)
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ")
}

function selectedCount(models: ImportProviderModelPreview[], selected: Set<string>): number {
  let count = 0
  for (const model of models) {
    if (!model.exists && selected.has(model.id)) count += 1
  }
  return count
}

interface BulkImportModelsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  secrets: Secret[]
  workspaceID: string | null
  onImported?: (models: Model[]) => void
}

export function BulkImportModelsDialog({
  open,
  onOpenChange,
  secrets,
  workspaceID,
  onImported,
}: BulkImportModelsDialogProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")

  const [baseURL, setBaseURL] = useState("")
  const [credentialMode, setCredentialMode] = useState<ModelCredentialMode>("inline_secret")
  const [apiKey, setApiKey] = useState("")
  const [existingSecretID, setExistingSecretID] = useState("")
  const [credentialKindCode, setCredentialKindCode] = useState("")
  const [search, setSearch] = useState("")
  const [previewModels, setPreviewModels] = useState<ImportProviderModelPreview[]>([])
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [importResult, setImportResult] = useState<ImportProviderModelsResponse | null>(null)
  const wasOpenRef = useRef(false)

  const previewMut = useImportProviderModels(workspaceID)
  const importMut = useImportProviderModels(workspaceID)

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      setBaseURL("")
      setCredentialMode("inline_secret")
      setApiKey("")
      setExistingSecretID("")
      setCredentialKindCode("")
      setSearch("")
      setPreviewModels([])
      setSelected(new Set())
      setImportResult(null)
      previewMut.reset()
      importMut.reset()
    }
    wasOpenRef.current = open
  }, [open, previewMut, importMut])

  const activeSecrets = secrets.filter((s) => s.status === "active" && s.kind === "model_provider")
  const pending = previewMut.isPending || importMut.isPending
  const errMsg = errorMessage(previewMut.error) ?? errorMessage(importMut.error)
  const count = selectedCount(previewModels, selected)

  const visibleModels = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return previewModels
    return previewModels.filter((model) => model.id.toLowerCase().includes(q))
  }, [previewModels, search])

  function resetDiscovery() {
    setPreviewModels([])
    setSelected(new Set())
    setImportResult(null)
    previewMut.reset()
    importMut.reset()
  }

  function payload(dryRun: boolean): ImportProviderModelsInput {
    const config: Record<string, unknown> = {}
    const endpointBaseURLs = endpointBaseURLsForProvider(IMPORT_PROVIDER, baseURL)
    if (Object.keys(endpointBaseURLs).length > 0) {
      config.endpoint_base_urls = endpointBaseURLs
    }
    const body: ImportProviderModelsInput = {
      provider_type: IMPORT_PROVIDER.key,
      adapter: IMPORT_PROVIDER.adapter,
      base_url: baseURL.trim(),
      credential_mode: credentialMode,
      dry_run: dryRun,
      skip_existing: true,
      config: Object.keys(config).length > 0 ? config : undefined,
    }
    if (apiKey.trim()) body.api_key = apiKey.trim()
    if (credentialMode === "inline_secret") {
      if (existingSecretID) body.secret_id = existingSecretID
    } else {
      body.credential_kind_code = credentialKindCode.trim()
    }
    if (!dryRun) {
      body.model_ids = previewModels
        .filter((model) => !model.exists && selected.has(model.id))
        .map((model) => model.id)
    }
    return body
  }

  const canDiscover =
    !!workspaceID &&
    baseURL.trim() !== "" &&
    !pending &&
    (apiKey.trim() !== "" || existingSecretID !== "")

  function discover() {
    if (!canDiscover) return
    previewMut.mutate(payload(true), {
      onSuccess: (data) => {
        setPreviewModels(data.models ?? [])
        setSelected(new Set((data.models ?? []).filter((m) => !m.exists).map((m) => m.id)))
        setImportResult(null)
      },
    })
  }

  useEffect(() => {
    if (!open || !canDiscover) return
    const timer = window.setTimeout(() => {
      discover()
    }, 650)
    return () => window.clearTimeout(timer)
  }, [open, baseURL, apiKey, existingSecretID])

  function importSelected() {
    importMut.mutate(payload(false), {
      onSuccess: (data) => {
        setImportResult(data)
        setPreviewModels(data.models ?? [])
        setSelected(new Set())
        if ((data.created?.length ?? 0) > 0) {
          onImported?.(data.created)
          onOpenChange(false)
        }
      },
    })
  }

  const canImport =
    count > 0 &&
    !pending &&
    (credentialMode === "credential_ref"
      ? credentialKindCode.trim() !== ""
      : apiKey.trim() !== "" || existingSecretID !== "")

  const failed = importResult?.failed ?? []

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[calc(100vh-2rem)] max-w-lg overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("models.bulkImport.title")}</DialogTitle>
        </DialogHeader>

        <div className="flex flex-col">
          <Field label={t("models.createProvider.fields.baseURL")} htmlFor="bulk-model-base-url">
            <Input
              id="bulk-model-base-url"
              value={baseURL}
              onChange={(event) => {
                setBaseURL(event.target.value)
                resetDiscovery()
              }}
              placeholder="https://api.example.com/v1"
              className="font-mono text-xs"
            />
          </Field>

          <RailSection title={t("models.createModel.fields.credentialMode")}>
            <div className="flex flex-col">
              {(["inline_secret", "credential_ref"] as ModelCredentialMode[]).map((mode) => (
                <label key={mode} className="flex h-7 items-center gap-2 text-sm text-fg">
                  <input
                    type="radio"
                    name="bulk-credential-mode"
                    value={mode}
                    className="h-3.5 w-3.5 accent-accent"
                    checked={credentialMode === mode}
                    onChange={() => setCredentialMode(mode)}
                  />
                  {mode === "inline_secret"
                    ? t("models.createModel.credentialMode.inlineSecret.title")
                    : t("models.createModel.credentialMode.credentialRef.title")}
                </label>
              ))}
            </div>

            <div className="mt-2 grid gap-3">
              <Field label={t("models.createProvider.fields.apiKey")} htmlFor="bulk-model-api-key">
                <Input
                  id="bulk-model-api-key"
                  type="password"
                  value={apiKey}
                  onChange={(event) => {
                    setApiKey(event.target.value)
                    if (event.target.value.trim() !== "") setExistingSecretID("")
                    resetDiscovery()
                  }}
                  placeholder="sk-..."
                />
              </Field>

              {credentialMode === "inline_secret" && activeSecrets.length > 0 && (
                <Field
                  label={t("models.createModel.credentialMode.inlineSecret.reuseSecret")}
                  htmlFor="bulk-model-secret"
                >
                  <Select
                    id="bulk-model-secret"
                    value={existingSecretID}
                    onChange={(event) => {
                      setExistingSecretID(event.target.value)
                      if (event.target.value !== "") setApiKey("")
                      resetDiscovery()
                    }}
                  >
                    <option value="">
                      {t("models.createModel.credentialMode.inlineSecret.reuseNone")}
                    </option>
                    {activeSecrets.map((secret) => (
                      <option key={secret.id} value={secret.id}>
                        {secret.name} ({secret.masked})
                      </option>
                    ))}
                  </Select>
                </Field>
              )}

              {credentialMode === "credential_ref" && (
                <Field
                  label={t("models.createModel.credentialMode.credentialRef.kindLabel")}
                  htmlFor="bulk-model-kind"
                >
                  <CredentialKindCombobox
                    workspaceID={workspaceID}
                    value={credentialKindCode}
                    onChange={setCredentialKindCode}
                    className="w-full"
                  />
                </Field>
              )}
            </div>
          </RailSection>

          <div className="mt-5 flex items-center gap-3">
            <Button type="button" variant="outline" onClick={discover} disabled={!canDiscover}>
              {previewMut.isPending ? (
                <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />
              ) : (
                <Search strokeWidth={1.5} aria-hidden="true" />
              )}
              {previewMut.isPending ? t("models.bulkImport.discovering") : t("models.bulkImport.discover")}
            </Button>
            {previewModels.length > 0 && (
              <span className="text-xs tabular-nums text-fg-muted">
                {t("models.bulkImport.selectedCount", { count })}
              </span>
            )}
          </div>

          {previewModels.length > 0 && (
            <div className="mt-3">
              <div className="relative">
                <Search
                  className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted"
                  strokeWidth={1.5}
                  aria-hidden="true"
                />
                <Input
                  type="search"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                  placeholder={t("models.bulkImport.search")}
                  aria-label={t("models.bulkImport.search")}
                  className="pl-7"
                />
              </div>
              <ul className="m-0 mt-2 max-h-64 list-none overflow-y-auto border-t border-line p-0">
                {visibleModels.map((model) => {
                  const checked = selected.has(model.id)
                  return (
                    <li key={model.id}>
                      <label className="flex h-9 items-center gap-2.5 border-b border-line text-sm text-fg hover:app-hover">
                        <input
                          type="checkbox"
                          className="h-3.5 w-3.5 accent-accent"
                          disabled={model.exists}
                          checked={!model.exists && checked}
                          onChange={(event) => {
                            const next = new Set(selected)
                            if (event.target.checked) next.add(model.id)
                            else next.delete(model.id)
                            setSelected(next)
                          }}
                        />
                        <span className="min-w-0 flex-1 truncate font-medium" title={model.id}>
                          {modelLabel(model.id)}
                        </span>
                        <span className="min-w-0 flex-1 truncate font-mono text-xs text-fg-muted" title={model.id}>
                          {model.id}
                        </span>
                        {model.supported_endpoint_types && model.supported_endpoint_types.length > 0 && (
                          <span className="shrink-0 truncate font-mono text-xs text-fg-muted">
                            {model.supported_endpoint_types.join(" · ")}
                          </span>
                        )}
                        {model.exists && <Badge variant="neutral">{t("models.bulkImport.exists")}</Badge>}
                      </label>
                    </li>
                  )
                })}
              </ul>
            </div>
          )}

          {importResult && (
            <p className="mt-3 flex items-center gap-2 text-sm text-fg">
              <StatusIcon status={failed.length === 0 ? "completed" : "failed"} />
              <span>
                {t("models.bulkImport.resultSummary", {
                  created: importResult.created?.length ?? 0,
                  skipped: importResult.skipped?.length ?? 0,
                  failed: failed.length,
                })}
              </span>
            </p>
          )}
          {failed.length > 0 && (
            <ul className="m-0 mt-1 max-h-28 list-none overflow-y-auto p-0 pl-5 font-mono text-xs text-fg">
              {failed.map((failure) => (
                <li key={failure.model_key} className="break-all">
                  {failure.model_key}: {failure.error}
                </li>
              ))}
            </ul>
          )}
          {errMsg && <ErrorState title={errMsg} className="pb-0" />}
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>
            {tc("actions.cancel")}
          </Button>
          <Button type="button" onClick={importSelected} disabled={!canImport}>
            {importMut.isPending && <Loader2 className="animate-spin" />}
            {t("models.bulkImport.importSelected")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
