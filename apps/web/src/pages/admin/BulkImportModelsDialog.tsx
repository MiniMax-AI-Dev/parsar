import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { CheckCircle2, Loader2, Search } from "lucide-react"

import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { ApiError } from "../../lib/api-client"
import type { ModelCredentialMode, Secret } from "../../lib/api-types"
import {
  useImportProviderModels,
  type ImportProviderModelPreview,
  type ImportProviderModelsInput,
  type ImportProviderModelsResponse,
} from "../../lib/api-models"
import {
  FALLBACK_PROVIDER_TYPES,
  isKnownProviderURL,
  providerTypesFromCatalog,
} from "../../lib/model-provider-options"
import { getProviderCatalogSnapshot, loadProviderCatalog } from "../../lib/model-presets"
import { ProviderTypeCombobox } from "./ProviderTypeCombobox"
import { CredentialKindCombobox } from "./capabilities/CredentialKindCombobox"

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
}

export function BulkImportModelsDialog({
  open,
  onOpenChange,
  secrets,
  workspaceID,
}: BulkImportModelsDialogProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")
  const [providerCatalog, setProviderCatalog] = useState(getProviderCatalogSnapshot)
  const providerTypes = useMemo(() => providerTypesFromCatalog(providerCatalog), [providerCatalog])
  const defaultProviderType = providerTypes[0] ?? FALLBACK_PROVIDER_TYPES[0]

  const [providerType, setProviderType] = useState(defaultProviderType.key)
  const [baseURL, setBaseURL] = useState(defaultProviderType.defaultBaseURL)
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
    let cancelled = false
    loadProviderCatalog().then((catalog) => {
      if (!cancelled) setProviderCatalog(catalog)
    })
    return () => {
      cancelled = true
    }
  }, [])

  useEffect(() => {
    if (open && !wasOpenRef.current) {
      setProviderType(defaultProviderType.key)
      setBaseURL(defaultProviderType.defaultBaseURL)
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
  }, [open, defaultProviderType, previewMut, importMut])

  const cfg = providerTypes.find((p) => p.key === providerType)
  const adapter = cfg?.adapter ?? "@ai-sdk/openai-compatible"
  const activeSecrets = secrets.filter((s) => s.status === "active" && s.kind === "model_provider")
  const pending = previewMut.isPending || importMut.isPending
  const errMsg = errorMessage(previewMut.error) ?? errorMessage(importMut.error)
  const count = selectedCount(previewModels, selected)

  const providerChoices = useMemo(
    () =>
      providerTypes.map((p) => ({
        key: p.key,
        label: p.label ?? (p.labelKey ? t(p.labelKey as never) : p.key),
        adapter: p.adapter,
        modelCount: p.models?.length ?? 0,
        protocols: p.protocols.map((proto) => proto.id),
      })),
    [providerTypes, t],
  )

  const visibleModels = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return previewModels
    return previewModels.filter((model) => model.id.toLowerCase().includes(q))
  }, [previewModels, search])

  function handleProviderTypeChange(next: string) {
    const nextCfg = providerTypes.find((p) => p.key === next)
    if (!nextCfg) return
    const previousCfg = providerTypes.find((p) => p.key === providerType)
    setProviderType(next)
    if (baseURL === "" || isKnownProviderURL(previousCfg, baseURL)) {
      setBaseURL(nextCfg.defaultBaseURL)
    }
    setPreviewModels([])
    setSelected(new Set())
    setImportResult(null)
  }

  function payload(dryRun: boolean): ImportProviderModelsInput {
    const config: Record<string, unknown> = {}
    if (cfg?.authSchemeSelector) {
      config.auth_scheme = "api-key"
    }
    const body: ImportProviderModelsInput = {
      provider_type: providerType,
      adapter,
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

  function discover() {
    previewMut.mutate(payload(true), {
      onSuccess: (data) => {
        setPreviewModels(data.models ?? [])
        setSelected(new Set((data.models ?? []).filter((m) => !m.exists).map((m) => m.id)))
        setImportResult(null)
      },
    })
  }

  function importSelected() {
    importMut.mutate(payload(false), {
      onSuccess: (data) => {
        setImportResult(data)
        setPreviewModels(data.models ?? [])
        setSelected(new Set())
      },
    })
  }

  const canDiscover = !!workspaceID && baseURL.trim() !== "" && providerType !== "" && adapter !== "" && !pending
  const canImport =
    count > 0 &&
    !pending &&
    (credentialMode === "credential_ref"
      ? credentialKindCode.trim() !== ""
      : apiKey.trim() !== "" || existingSecretID !== "")

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("models.bulkImport.title")}</DialogTitle>
          <DialogDescription>{t("models.bulkImport.description")}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4">
          <div className="grid gap-3 sm:grid-cols-2">
            <div className="grid gap-1.5 min-w-0">
              <label className="text-sm font-medium text-fg-muted" htmlFor="bulk-model-provider">
                {t("models.createProvider.fields.providerType")}
              </label>
              <ProviderTypeCombobox
                id="bulk-model-provider"
                value={providerType}
                onChange={handleProviderTypeChange}
                options={providerChoices}
              />
            </div>
            <div className="grid gap-1.5 min-w-0">
              <label className="text-sm font-medium text-fg-muted" htmlFor="bulk-model-base-url">
                {t("models.createProvider.fields.baseURL")}
              </label>
              <Input
                id="bulk-model-base-url"
                value={baseURL}
                onChange={(event) => setBaseURL(event.target.value)}
                placeholder="https://api.example.com/v1"
                className="font-mono text-sm"
              />
            </div>
          </div>

          <fieldset className="rounded-md border border-line p-3">
            <legend className="px-1 text-sm font-medium text-fg-muted">
              {t("models.createModel.fields.credentialMode")}
            </legend>
            <div className="mt-2 grid gap-2 sm:grid-cols-2">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="bulk-credential-mode"
                  value="inline_secret"
                  checked={credentialMode === "inline_secret"}
                  onChange={() => setCredentialMode("inline_secret")}
                />
                <span className="font-medium text-fg-emphasis">
                  {t("models.createModel.credentialMode.inlineSecret.title")}
                </span>
              </label>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="bulk-credential-mode"
                  value="credential_ref"
                  checked={credentialMode === "credential_ref"}
                  onChange={() => setCredentialMode("credential_ref")}
                />
                <span className="font-medium text-fg-emphasis">
                  {t("models.createModel.credentialMode.credentialRef.title")}
                </span>
              </label>
            </div>
          </fieldset>

          <div className="grid gap-3 rounded-md bg-surface-subtle/60 p-3">
            <div className="grid gap-1.5">
              <label className="text-sm font-medium text-fg-muted" htmlFor="bulk-model-api-key">
                {t("models.createProvider.fields.apiKey")}
              </label>
              <Input
                id="bulk-model-api-key"
                type="password"
                value={apiKey}
                onChange={(event) => {
                  setApiKey(event.target.value)
                  if (event.target.value.trim() !== "") setExistingSecretID("")
                }}
                placeholder="sk-..."
              />
              <span className="text-xs text-fg-faint">
                {credentialMode === "inline_secret"
                  ? t("models.bulkImport.apiKeyHintInline")
                  : t("models.bulkImport.apiKeyHintPersonal")}
              </span>
            </div>

            {credentialMode === "inline_secret" && activeSecrets.length > 0 && (
              <div className="grid gap-1.5">
                <label className="text-sm font-medium text-fg-muted" htmlFor="bulk-model-secret">
                  {t("models.createModel.credentialMode.inlineSecret.reuseSecret")}
                </label>
                <select
                  id="bulk-model-secret"
                  value={existingSecretID}
                  onChange={(event) => {
                    setExistingSecretID(event.target.value)
                    if (event.target.value !== "") setApiKey("")
                  }}
                  className="flex h-9 w-full rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong"
                >
                  <option value="">
                    {t("models.createModel.credentialMode.inlineSecret.reuseNone")}
                  </option>
                  {activeSecrets.map((secret) => (
                    <option key={secret.id} value={secret.id}>
                      {secret.name} ({secret.masked})
                    </option>
                  ))}
                </select>
              </div>
            )}

            {credentialMode === "credential_ref" && (
              <div className="grid gap-1.5">
                <label className="text-sm font-medium text-fg-muted" htmlFor="bulk-model-kind">
                  {t("models.createModel.credentialMode.credentialRef.kindLabel")}
                </label>
                <CredentialKindCombobox
                  workspaceID={workspaceID}
                  value={credentialKindCode}
                  onChange={setCredentialKindCode}
                  className="w-full"
                />
              </div>
            )}
          </div>

          <div className="flex items-center justify-between gap-3">
            <Button type="button" variant="outline" size="sm" onClick={discover} disabled={!canDiscover}>
              {previewMut.isPending ? (
                <Loader2 className="h-3.5 w-3.5 animate-spin" />
              ) : (
                <Search className="h-3.5 w-3.5" />
              )}
              {t("models.bulkImport.discover")}
            </Button>
            {previewModels.length > 0 && (
              <span className="text-xs text-fg-faint">
                {t("models.bulkImport.selectedCount", { count })}
              </span>
            )}
          </div>

          {previewModels.length > 0 && (
            <div className="rounded-md border border-line">
              <div className="border-b border-line-muted p-2">
                <div className="relative">
                  <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
                  <Input
                    value={search}
                    onChange={(event) => setSearch(event.target.value)}
                    placeholder={t("models.bulkImport.search")}
                    className="pl-8"
                  />
                </div>
              </div>
              <div className="max-h-64 overflow-y-auto">
                {visibleModels.map((model) => {
                  const checked = selected.has(model.id)
                  return (
                    <label
                      key={model.id}
                      className="flex min-w-0 items-center gap-3 border-b border-line-muted px-3 py-2 last:border-b-0"
                    >
                      <input
                        type="checkbox"
                        className="h-3.5 w-3.5"
                        disabled={model.exists}
                        checked={!model.exists && checked}
                        onChange={(event) => {
                          const next = new Set(selected)
                          if (event.target.checked) next.add(model.id)
                          else next.delete(model.id)
                          setSelected(next)
                        }}
                      />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm font-medium text-fg" title={model.id}>
                          {modelLabel(model.id)}
                        </span>
                        <span className="block truncate font-mono text-xs text-fg-faint" title={model.id}>
                          {model.id}
                        </span>
                        {model.supported_endpoint_types && model.supported_endpoint_types.length > 0 && (
                          <span className="mt-1 flex flex-wrap gap-1">
                            {model.supported_endpoint_types.map((endpointType) => (
                              <span
                                key={endpointType}
                                className="rounded border border-line-muted px-1.5 py-0.5 font-mono text-xs text-fg-subtle"
                              >
                                {endpointType}
                              </span>
                            ))}
                          </span>
                        )}
                      </span>
                      {model.exists && (
                        <span className="shrink-0 rounded border border-line-muted px-1.5 py-0.5 text-xs text-fg-subtle">
                          {t("models.bulkImport.exists")}
                        </span>
                      )}
                    </label>
                  )
                })}
              </div>
            </div>
          )}

          {importResult && (
            <div className="flex items-start gap-2 rounded-md bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
              <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0" />
              <span>
                {t("models.bulkImport.resultSummary", {
                  created: importResult.created?.length ?? 0,
                  skipped: importResult.skipped?.length ?? 0,
                  failed: importResult.failed?.length ?? 0,
                })}
              </span>
            </div>
          )}
          {importResult?.failed?.length ? (
            <div className="max-h-28 overflow-y-auto rounded-md bg-danger-subtle px-3 py-2 text-xs text-danger-emphasis">
              {importResult.failed.map((failure) => (
                <div key={failure.model_key} className="break-all">
                  {failure.model_key}: {failure.error}
                </div>
              ))}
            </div>
          ) : null}
          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis break-all">
              {errMsg}
            </p>
          )}
        </div>

        <DialogFooter>
          <Button type="button" variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={pending}>
            {tc("actions.cancel")}
          </Button>
          <Button type="button" size="sm" onClick={importSelected} disabled={!canImport}>
            {importMut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t("models.bulkImport.importSelected")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
