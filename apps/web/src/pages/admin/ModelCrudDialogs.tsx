/**
 * Create/Edit dialogs for the shared model catalog.
 *
 * - CreateModelDialog: provider_type + adapter + base_url + model_key
 *   + credential_mode radio. inline_secret chains a POST /secrets via
 *   useCreateModel; credential_ref carries only the kind code.
 * - EditModelDialog: name / model_key / base_url / headers / credential
 *   binding. credential_mode is LOCKED; switching mode requires a new
 *   Model.
 */
import { useEffect, useMemo, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { ApiError } from "../../lib/api-client"
import type { Model, ModelCredentialMode, Secret } from "../../lib/api-types"
import {
  useDetectModelEndpoints,
  type InlineCreateModelInput,
  type InlineUpdateModelInput,
} from "../../lib/api-models"
import { Button } from "../../components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../components/ui/dialog"
import { Input } from "../../components/ui/input"
import { CredentialKindCombobox } from "./capabilities/CredentialKindCombobox"
import { ModelKeyCombobox } from "./ModelKeyCombobox"
import { ProviderTypeCombobox } from "./ProviderTypeCombobox"
import {
  getProviderCatalogSnapshot,
  loadProviderCatalog,
} from "../../lib/model-presets"
import {
  FALLBACK_PROVIDER_TYPES,
  defaultModelFor,
  endpointBaseURLsForProvider,
  endpointTypeForProtocol,
  findProviderModel,
  isKnownProviderURL,
  providerTypesFromCatalog,
  shouldReplaceModelName,
  shouldReplaceProviderModelKey,
} from "../../lib/model-provider-options"

function extractErrorMessage(err: unknown): string | null {
  if (!err) return null
  if (err instanceof ApiError) {
    return err.envelope.message || err.message
  }
  if (err instanceof Error) return err.message
  return String(err)
}

/* --- HeadersEditor ------------------------------------------------------
 *
 * Row-based editor shared by Create + Edit. Internally tracks
 * `{id, key, value}` rows (not indexes) because React keys can't be
 * array indices (deleting row N reuses the same key for N+1 and diffs
 * the wrong inputs), and to preserve half-typed rows during onChange
 * round-trips.
 *
 * Rows seed from `value` once per `seedKey` token — pass model id for
 * edit, bump an int for create. Reseeding on every `value` change would
 * fight typing.
 */
interface HeadersEditorProps {
  value: Record<string, string>
  onChange: (next: Record<string, string>) => void
  label: string
  addLabel: string
  removeLabel: string
  seedKey: string
}

function HeadersEditor({
  value,
  onChange,
  label,
  addLabel,
  removeLabel,
  seedKey,
}: HeadersEditorProps) {
  const counterRef = useRef(0)
  const seedKeyRef = useRef<string | null>(null)
  const [rows, setRows] = useState<Array<{ id: number; key: string; value: string }>>([])

  // Seed once per seedKey change. Effect not useMemo because we mutate
  // a ref counter and React state in sequence.
  useEffect(() => {
    if (seedKeyRef.current === seedKey) return
    seedKeyRef.current = seedKey
    const seeded = Object.entries(value).map(([k, v]) => ({
      id: counterRef.current++,
      key: k,
      value: v,
    }))
    setRows(seeded)
  }, [seedKey, value])

  function commit(next: Array<{ id: number; key: string; value: string }>) {
    setRows(next)
    const map: Record<string, string> = {}
    for (const r of next) {
      const k = r.key.trim()
      const v = r.value.trim()
      if (k && v) map[k] = v
    }
    onChange(map)
  }

  function addRow() {
    commit([...rows, { id: counterRef.current++, key: "", value: "" }])
  }
  function updateRow(id: number, field: "key" | "value", v: string) {
    commit(rows.map((r) => (r.id === id ? { ...r, [field]: v } : r)))
  }
  function removeRow(id: number) {
    commit(rows.filter((r) => r.id !== id))
  }

  return (
    <div className="grid gap-1.5">
      <label className="text-sm font-medium text-fg-muted">{label}</label>
      {rows.map((row) => (
        <div key={row.id} className="flex gap-2">
          <Input
            value={row.key}
            onChange={(e) => updateRow(row.id, "key", e.target.value)}
            placeholder="Header"
            className="flex-1 font-mono text-sm"
          />
          <Input
            value={row.value}
            onChange={(e) => updateRow(row.id, "value", e.target.value)}
            placeholder="value"
            className="flex-1 font-mono text-sm"
          />
          <Button type="button" variant="outline" size="sm" onClick={() => removeRow(row.id)}>
            {removeLabel}
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={addRow}>
        {addLabel}
      </Button>
    </div>
  )
}

/* --- Tiny shared form bits ---------------------------------------------- */

interface FieldProps {
  id: string
  label: string
  value: string
  onChange: (v: string) => void
  placeholder?: string
  required?: boolean
  autoFocus?: boolean
  mono?: boolean
  hint?: string
  type?: string
}

function Field({
  id,
  label,
  value,
  onChange,
  placeholder,
  required,
  autoFocus,
  mono,
  hint,
  type,
}: FieldProps) {
  return (
    <div className="grid gap-1.5">
      <label className="text-sm font-medium text-fg-muted" htmlFor={id}>
        {label}
        {required && <span className="ml-0.5 text-danger">*</span>}
      </label>
      <Input
        id={id}
        type={type}
        value={value}
        autoFocus={autoFocus}
        required={required}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className={mono ? "font-mono text-sm" : undefined}
      />
      {hint && <span className="text-xs text-fg-faint">{hint}</span>}
    </div>
  )
}

/* --- Create Model -------------------------------------------------------- */

interface CreateModelDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Active secrets the user could reuse instead of pasting a fresh key.
   *  Filtered to kind="model_provider" only — runtime / inline_secret
   *  secrets are irrelevant here. */
  secrets: Secret[]
  /** Workspace the dialog lives in. Passed down to the credential-kind
   *  combobox so it can list / inline-create kinds in the right scope.
   *  null while the page hasn't picked a workspace yet — the dialog is
   *  effectively disabled in that window. */
  workspaceID: string | null
  pending: boolean
  error: unknown
  /** Optional seed values for the dialog fields. Used by the "duplicate"
   *  action on ModelsPage to pre-fill the form from an existing model.
   *  When omitted, the dialog resets to empty constants on open. */
  initialValues?: InlineCreateModelInput | null
  onSubmit: (values: InlineCreateModelInput) => void
}

export function CreateModelDialog({
  open,
  onOpenChange,
  secrets,
  workspaceID,
  pending,
  error,
  initialValues,
  onSubmit,
}: CreateModelDialogProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")

  const [providerCatalog, setProviderCatalog] = useState(getProviderCatalogSnapshot)
  useEffect(() => {
    let cancelled = false
    loadProviderCatalog().then((catalog) => {
      if (!cancelled) setProviderCatalog(catalog)
    })
    return () => {
      cancelled = true
    }
  }, [])

  const providerTypes = useMemo(() => providerTypesFromCatalog(providerCatalog), [providerCatalog])
  const defaultProviderType = providerTypes[0] ?? FALLBACK_PROVIDER_TYPES[0]
  const defaultModel = defaultModelFor(defaultProviderType)

  const [name, setName] = useState(() => defaultModel?.name ?? "")
  const [providerType, setProviderType] = useState<string>(() => defaultProviderType.key)
  const [baseURL, setBaseURL] = useState<string>(() => defaultProviderType.defaultBaseURL)
  const [modelKey, setModelKey] = useState(() => defaultModel?.id ?? "")
  const [credentialMode, setCredentialMode] = useState<ModelCredentialMode>("inline_secret")
  const [apiKey, setApiKey] = useState("")
  const [existingSecretID, setExistingSecretID] = useState<string>("")
  const [credentialKindCode, setCredentialKindCode] = useState<string>("")
  const [headers, setHeaders] = useState<Record<string, string>>({})
  // Bumped on every dialog reopen so HeadersEditor reseeds its row list
  // back to empty (matches the cleared `headers` state above).
  const [headersSeed, setHeadersSeed] = useState(0)
  const [authScheme, setAuthScheme] = useState<"api-key" | "bearer">("api-key")
  // Non-UI config keys carried verbatim from the duplicate source so
  // capabilities / limits / modalities / etc. survive the copy. Cleared
  // when the dialog opens fresh ("+ New model" path) so a vanilla create
  // doesn't inherit anything.
  const [baseConfig, setBaseConfig] = useState<Record<string, unknown>>({})
  const detectEndpointsMut = useDetectModelEndpoints(workspaceID)

  const wasOpenRef = useRef(false)
  useEffect(() => {
    if (open && !wasOpenRef.current) {
      // Seed every field. If `initialValues` was provided (duplicate
      // flow), pull from it; otherwise reset to the same empty defaults
      // we shipped before. Both branches bump headersSeed in the same
      // effect tick so HeadersEditor reseeds atomically with the parent.
      const seed = initialValues
      if (seed) {
        const providerCfg = providerTypes.find((p) => p.key === seed.provider_type)
        setName(seed.name)
        setProviderType(seed.provider_type)
        setBaseURL(seed.base_url)
        setModelKey(seed.model_key)
        setCredentialMode(seed.credential_mode)
        // API keys are never readable back from the server; the duplicate
        // flow reuses the source model's secret_id (or credential_kind_code)
        // so the user doesn't have to re-paste their key.
        setApiKey("")
        setExistingSecretID(seed.existing_secret_id ?? "")
        setCredentialKindCode(seed.credential_kind_code ?? "")
        const cfg = (seed.config ?? {}) as {
          headers?: Record<string, string>
          auth_scheme?: "api-key" | "bearer"
        }
        setHeaders(providerCfg?.customHeaders ? (cfg.headers ?? {}) : {})
        setAuthScheme(providerCfg?.authSchemeSelector ? (cfg.auth_scheme ?? "api-key") : "api-key")
        // Stash every config key the form does NOT have a dedicated field
        // for — capabilities / limits / modalities / etc. — so submit can
        // merge them back in and the duplicate behaves identically to its
        // source. The form-owned keys (headers / auth_scheme) are managed
        // by their own state and re-applied at submit time.
        const {
          headers: _h,
          auth_scheme: _a,
          ...rest
        } = (seed.config ?? {}) as Record<string, unknown>
        void _h
        void _a
        setBaseConfig(rest)
      } else {
        const nextDefaultModel = defaultModelFor(defaultProviderType)
        setName(nextDefaultModel?.name ?? "")
        setProviderType(defaultProviderType.key)
        setBaseURL(defaultProviderType.defaultBaseURL)
        setModelKey(nextDefaultModel?.id ?? "")
        setCredentialMode("inline_secret")
        setApiKey("")
        setExistingSecretID("")
        setCredentialKindCode("")
        setHeaders({})
        setAuthScheme("api-key")
        setBaseConfig({})
      }
      setHeadersSeed((n) => n + 1)
    }
    wasOpenRef.current = open
  }, [open, initialValues, providerTypes, defaultProviderType])

  function handleProviderTypeChange(next: string) {
    const cfg = providerTypes.find((p) => p.key === next)
    if (!cfg) return
    const previousCfg = providerTypes.find((p) => p.key === providerType)
    const previousModel = findProviderModel(previousCfg, modelKey)
    const nextDefaultModel = defaultModelFor(cfg)
    // Overwrite the base URL unless the user hand-typed a custom endpoint —
    // i.e. the current value still matches ANY of the previous provider's
    // known protocol URLs.
    if (baseURL === "" || isKnownProviderURL(previousCfg, baseURL)) {
      setBaseURL(cfg.defaultBaseURL)
    }
    if (shouldReplaceProviderModelKey(modelKey, previousCfg)) {
      setModelKey(nextDefaultModel?.id ?? "")
    }
    if (shouldReplaceModelName(name, previousModel)) {
      setName(nextDefaultModel?.name ?? "")
    }
    setProviderType(next)
  }

  const cfg = providerTypes.find((p) => p.key === providerType)
  const adapter = cfg?.adapter ?? "@ai-sdk/openai-compatible"
  const protocolChoices = cfg?.protocols ?? []
  const showHeadersEditor = !!cfg?.customHeaders
  const showAuthSchemeSelector = !!cfg?.authSchemeSelector
  const providerModels = cfg?.models ?? []
  const errMsg = extractErrorMessage(error)

  // Resolve each provider option's display label once (literal brand name, or
  // translated key for the generic gateways) for the searchable picker.
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

  // Picking a catalog model id fills the key; if the display name is still
  // catalog-managed, keep it in sync with the model's friendly name.
  function handleModelKeyChange(next: string) {
    const previousModel = providerModels.find((m) => m.id === modelKey.trim())
    const hit = providerModels.find((m) => m.id === next)
    const shouldReplaceName = shouldReplaceModelName(name, previousModel)
    setModelKey(next)
    if (shouldReplaceName) {
      setName(hit?.name ?? next.trim())
    }
  }

  const activeSecrets = secrets.filter((s) => s.status === "active" && s.kind === "model_provider")

  // Duplicate flow seeds existingSecretID from the source model's
  // secret_id. If the caller can't read that Secret (cross-workspace
  // scope, RBAC, deleted), it won't show up in activeSecrets — and the
  // user would see a blank-looking dropdown with the form silently
  // refusing to submit. Surface it as a disabled phantom option +
  // inline warning so the user knows what to do (pick another or paste
  // a fresh key).
  const sourceSecretMissing =
    credentialMode === "inline_secret" &&
    existingSecretID !== "" &&
    !activeSecrets.some((s) => s.id === existingSecretID)

  const inlineSecretBranchValid =
    credentialMode !== "inline_secret" ||
    apiKey.trim() !== "" ||
    (existingSecretID !== "" && !sourceSecretMissing)

  const credentialRefBranchValid =
    credentialMode !== "credential_ref" || credentialKindCode.trim() !== ""

  const canSubmit =
    name.trim() !== "" &&
    baseURL.trim() !== "" &&
    modelKey.trim() !== "" &&
    inlineSecretBranchValid &&
    credentialRefBranchValid &&
    !pending &&
    !detectEndpointsMut.isPending

  function fallbackEndpointTypes(): string[] {
    const out: string[] = []
    const seen = new Set<string>()
    for (const protocol of protocolChoices) {
      const endpointType = endpointTypeForProtocol(protocol.id)
      if (!endpointType || seen.has(endpointType)) continue
      seen.add(endpointType)
      out.push(endpointType)
    }
    return out
  }

  async function detectedEndpointTypes(config: Record<string, unknown>): Promise<string[]> {
    if (credentialMode !== "inline_secret") return fallbackEndpointTypes()
    const secretID = existingSecretID || undefined
    const key = apiKey.trim()
    if (!secretID && !key) return fallbackEndpointTypes()
    try {
      const result = await detectEndpointsMut.mutateAsync({
        base_url: baseURL.trim(),
        model_key: modelKey.trim(),
        api_key: key || undefined,
        secret_id: secretID,
        config,
      })
      return result.supported_endpoint_types.length > 0
        ? result.supported_endpoint_types
        : fallbackEndpointTypes()
    } catch {
      return fallbackEndpointTypes()
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    // Start from baseConfig so duplicate-flow values (capabilities,
    // limits, modalities, …) survive the round-trip. For a vanilla
    // create baseConfig is {} and we land on the original empty-object
    // behavior. Then layer the form-owned keys (headers / auth_scheme)
    // on top — they always reflect what's currently in the dialog
    // because the form re-seeds them on open.
    const config: Record<string, unknown> = { ...baseConfig }
    if (showHeadersEditor) {
      if (Object.keys(headers).length > 0) {
        config.headers = headers
      } else {
        delete config.headers
      }
    }
    if (showAuthSchemeSelector) {
      config.auth_scheme = authScheme
    }
    const endpointBaseURLs = endpointBaseURLsForProvider(cfg, baseURL)
    if (Object.keys(endpointBaseURLs).length > 0) {
      config.endpoint_base_urls = endpointBaseURLs
    }
    config.supported_endpoint_types = await detectedEndpointTypes(config)

    const payload: InlineCreateModelInput = {
      name: name.trim(),
      provider_type: providerType,
      adapter,
      base_url: baseURL.trim(),
      model_key: modelKey.trim(),
      credential_mode: credentialMode,
      config: Object.keys(config).length > 0 ? config : undefined,
    }
    if (credentialMode === "inline_secret") {
      if (existingSecretID) payload.existing_secret_id = existingSecretID
      else if (apiKey.trim()) payload.api_key = apiKey.trim()
    } else {
      payload.credential_kind_code = credentialKindCode.trim()
    }
    onSubmit(payload)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("models.createModel.title")}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={handleSubmit}>
          {/* --- Identity --- */}
          <Field
            id="model-name"
            label={t("models.createModel.fields.name")}
            value={name}
            onChange={setName}
            placeholder={t("models.createModel.fields.namePlaceholder")}
            required
            autoFocus
          />

          {/* --- Provider type + endpoint --- */}
          <div className="grid gap-1.5">
            <label className="text-sm font-medium text-fg-muted" htmlFor="model-provider-type">
              {t("models.createProvider.fields.providerType")}
              <span className="ml-0.5 text-danger">*</span>
            </label>
            <ProviderTypeCombobox
              id="model-provider-type"
              value={providerType}
              onChange={handleProviderTypeChange}
              options={providerChoices}
            />
            <span className="text-xs text-fg-faint">
              {t("models.createProvider.fields.adapterHint", { adapter })}
            </span>
          </div>

          {/* --- Protocol row ---
          <Field
            id="model-base-url"
            label={t("models.createProvider.fields.baseURL")}
            value={baseURL}
            onChange={setBaseURL}
            placeholder="https://api.example.com/v1"
            required
            mono
          />

          <div className="grid gap-1.5">
            <label className="text-sm font-medium text-fg-muted" htmlFor="model-key">
              {t("models.createModel.fields.modelKey")}
              <span className="ml-0.5 text-danger">*</span>
            </label>
            <ModelKeyCombobox
              id="model-key"
              value={modelKey}
              onChange={handleModelKeyChange}
              models={providerModels}
              placeholder={t("models.createModel.fields.modelKeyPlaceholder")}
            />
            {providerModels.length > 0 && (
              <span className="text-xs text-fg-faint">
                {t("models.createModel.fields.modelKeyCatalogHint")}
              </span>
            )}
          </div>

          {/* --- Custom headers (only for *-compatible gateways) --- */}
          {showHeadersEditor && (
            <HeadersEditor
              value={headers}
              onChange={setHeaders}
              label={t("models.createProvider.fields.customHeaders")}
              addLabel={t("models.createProvider.actions.addHeader")}
              removeLabel={tc("actions.delete")}
              seedKey={`create-${headersSeed}`}
            />
          )}

          {/* --- Auth scheme (only for anthropic-compatible) --- */}
          {showAuthSchemeSelector && (
            <div className="grid gap-1.5">
              <label className="text-sm font-medium text-fg-muted" htmlFor="model-auth-scheme">
                {t("models.createProvider.fields.authScheme")}
              </label>
              <select
                id="model-auth-scheme"
                value={authScheme}
                onChange={(e) => setAuthScheme(e.target.value as "api-key" | "bearer")}
                className="flex h-9 w-full rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong"
              >
                <option value="api-key">x-api-key</option>
                <option value="bearer">Authorization: Bearer</option>
              </select>
            </div>
          )}

          {/* --- Credential mode --- */}
          <fieldset className="rounded-md border border-line p-3">
            <legend className="px-1 text-sm font-medium text-fg-muted">
              {t("models.createModel.fields.credentialMode")}
              <span className="ml-0.5 text-danger">*</span>
            </legend>
            <div className="mt-2 grid gap-2">
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="radio"
                  name="credential-mode"
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
                  name="credential-mode"
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

          {/* --- inline_secret branch fields --- */}
          {credentialMode === "inline_secret" && (
            <div className="grid gap-3 rounded-md bg-surface-subtle/60 p-3">
              <Field
                id="model-api-key"
                label={t("models.createProvider.fields.apiKey")}
                value={apiKey}
                onChange={(v) => {
                  setApiKey(v)
                  if (v.trim() !== "") setExistingSecretID("")
                }}
                placeholder="sk-..."
                hint={t("models.createModel.credentialMode.inlineSecret.apiKeyHint")}
                type="password"
              />
              {(activeSecrets.length > 0 || sourceSecretMissing) && (
                <div className="grid gap-1.5">
                  <label
                    className="text-sm font-medium text-fg-muted"
                    htmlFor="model-existing-secret"
                  >
                    {t("models.createModel.credentialMode.inlineSecret.reuseSecret")}
                  </label>
                  <select
                    id="model-existing-secret"
                    value={existingSecretID}
                    onChange={(e) => {
                      setExistingSecretID(e.target.value)
                      if (e.target.value !== "") setApiKey("")
                    }}
                    className="flex h-9 w-full rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong"
                  >
                    <option value="">
                      {t("models.createModel.credentialMode.inlineSecret.reuseNone")}
                    </option>
                    {sourceSecretMissing && (
                      // Phantom option for a duplicate whose source Secret
                      // is not visible to the caller. Marked disabled so
                      // submit can't proceed via this path — the user has
                      // to either pick another visible Secret or paste a
                      // fresh key in the field above.
                      <option value={existingSecretID} disabled>
                        {`✗ ${existingSecretID.slice(0, 8)}… (${t("models.copy.secretInaccessible")})`}
                      </option>
                    )}
                    {activeSecrets.map((s) => (
                      <option key={s.id} value={s.id}>
                        {s.name} ({s.masked})
                      </option>
                    ))}
                  </select>
                  {sourceSecretMissing && (
                    <span className="text-xs text-danger">
                      {t("models.copy.secretInaccessible")}
                    </span>
                  )}
                </div>
              )}
            </div>
          )}

          {/* --- credential_ref branch fields --- */}
          {credentialMode === "credential_ref" && (
            <div className="grid gap-3 rounded-md bg-surface-subtle/60 p-3">
              <div className="grid gap-1.5">
                <label
                  className="text-sm font-medium text-fg-muted"
                  htmlFor="model-credential-kind"
                >
                  {t("models.createModel.credentialMode.credentialRef.kindLabel")}
                  <span className="ml-0.5 text-danger">*</span>
                </label>
                <CredentialKindCombobox
                  workspaceID={workspaceID}
                  value={credentialKindCode}
                  onChange={setCredentialKindCode}
                  className="w-full"
                />
                <span className="text-xs text-fg-faint">
                  {t("models.createModel.credentialMode.credentialRef.kindHint")}
                </span>
              </div>
            </div>
          )}

          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={pending || detectEndpointsMut.isPending}
            >
              {tc("actions.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={!canSubmit}>
              {pending || detectEndpointsMut.isPending ? tc("states.loading") : t("models.createModel.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

/* --- Edit Model --------------------------------------------------------- */

interface EditModelDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  model: Model | null
  secrets: Secret[]
  /** Workspace the dialog lives in. Passed to the credential-kind combobox
   *  for credential_ref-mode models. */
  workspaceID: string | null
  pending: boolean
  error: unknown
  onSubmit: (values: InlineUpdateModelInput) => void
}

/**
 * Edit dialog for a shared Model. credential_mode / provider_type /
 * adapter are LOCKED (read-only display) — to change them, recreate
 * the model. Inside the model's current mode the user can still
 * rotate / re-point the credential.
 */
export function EditModelDialog({
  open,
  onOpenChange,
  model,
  secrets,
  workspaceID,
  pending,
  error,
  onSubmit,
}: EditModelDialogProps) {
  const { t } = useTranslation("admin")
  const { t: tc } = useTranslation("common")

  const providerTypeMeta = useMemo(() => {
    if (!model) return undefined
    return FALLBACK_PROVIDER_TYPES.find((p) => p.key === model.provider_type)
  }, [model])
  const supportsCustomHeaders = providerTypeMeta?.customHeaders ?? false

  const [name, setName] = useState("")
  const [modelKey, setModelKey] = useState("")
  const [baseURL, setBaseURL] = useState("")
  const [headers, setHeaders] = useState<Record<string, string>>({})
  const [newAPIKey, setNewAPIKey] = useState("")
  const [secretID, setSecretID] = useState<string>("")
  const [credentialKindCode, setCredentialKindCode] = useState<string>("")

  useEffect(() => {
    if (!open || !model) return
    setName(model.name)
    setModelKey(model.model_key)
    setBaseURL(model.base_url)
    setHeaders((model.config as { headers?: Record<string, string> })?.headers ?? {})
    setNewAPIKey("")
    setSecretID(model.secret_id ?? "")
    setCredentialKindCode(model.credential_kind_code ?? "")
  }, [open, model])

  if (!model) return null
  const errMsg = extractErrorMessage(error)
  const activeSecrets = secrets.filter((s) => s.status === "active" && s.kind === "model_provider")
  const isInline = model.credential_mode === "inline_secret"
  const isCredentialRef = model.credential_mode === "credential_ref"

  const canSubmit =
    name.trim() !== "" &&
    modelKey.trim() !== "" &&
    !pending &&
    (isInline || credentialKindCode.trim() !== "")

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    if (!model) return

    // Merge headers back into model.config. When the provider type
    // doesn't support custom headers we pass model.config through
    // verbatim (nothing on this form changed it); when it does, we
    // overwrite the headers key — empty map drops the key entirely
    // so we don't persist `{headers: {}}`.
    let config: Record<string, unknown>
    if (supportsCustomHeaders) {
      const rest = Object.fromEntries(Object.entries(model.config).filter(([k]) => k !== "headers"))
      config = Object.keys(headers).length > 0 ? { ...rest, headers } : rest
    } else {
      config = model.config as Record<string, unknown>
    }

    const values: InlineUpdateModelInput = {
      name: name.trim(),
      model_key: modelKey.trim(),
      base_url: baseURL.trim(),
      provider_type: model.provider_type,
      credential_mode: model.credential_mode,
      config,
    }
    if (isInline) {
      // empty newAPIKey + same existing secret_id ⇒ keep current binding
      if (newAPIKey.trim()) {
        values.api_key = newAPIKey.trim()
      } else if (secretID) {
        values.existing_secret_id = secretID
      }
    } else {
      values.credential_kind_code = credentialKindCode.trim()
    }
    onSubmit(values)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("models.editModel.title", { name: model.name })}</DialogTitle>
        </DialogHeader>
        <form className="grid gap-4" onSubmit={handleSubmit}>
          {/* --- Locked identity --- */}
          <div className="grid grid-cols-2 gap-3 rounded-md bg-surface-subtle/60 p-3 text-sm">
            <div>
              <div className="text-fg-subtle">{t("models.editModel.locked.providerType")}</div>
              <div className="mt-0.5 font-medium text-fg-muted">{model.provider_type}</div>
            </div>
            <div>
              <div className="text-fg-subtle">{t("models.editModel.locked.adapter")}</div>
              <div className="mt-0.5 font-mono text-fg-muted">{model.adapter}</div>
            </div>
            <div>
              <div className="text-fg-subtle">{t("models.editModel.locked.credentialMode")}</div>
              <div className="mt-0.5 font-medium text-fg-muted">
                {isInline
                  ? t("models.createModel.credentialMode.inlineSecret.title")
                  : t("models.createModel.credentialMode.credentialRef.title")}
              </div>
            </div>
            <div>
              <div className="text-fg-subtle">{t("models.editModel.locked.slug")}</div>
              <div className="mt-0.5 font-mono text-fg-muted">{model.slug}</div>
            </div>
          </div>

          <Field
            id="edit-model-name"
            label={t("models.editModel.fields.name")}
            value={name}
            onChange={setName}
            required
            autoFocus
          />

          <Field
            id="edit-model-key"
            label={t("models.editModel.fields.modelKey")}
            value={modelKey}
            onChange={setModelKey}
            required
            mono
          />

          <Field
            id="edit-model-base-url"
            label={t("models.editModel.fields.baseURL")}
            value={baseURL}
            onChange={setBaseURL}
            mono
          />

          {supportsCustomHeaders && (
            <HeadersEditor
              value={headers}
              onChange={setHeaders}
              label={t("models.editModel.fields.customHeaders")}
              addLabel={t("models.createProvider.actions.addHeader")}
              removeLabel={tc("actions.delete")}
              seedKey={`edit-${model.id}`}
            />
          )}

          {/* --- Credential binding (mode-specific) --- */}
          {isInline && (
            <div className="grid gap-3 rounded-md bg-surface-subtle/60 p-3">
              <div className="grid gap-1.5">
                <label className="text-sm font-medium text-fg-muted" htmlFor="edit-model-secret">
                  {t("models.editModel.credentialBinding.boundSecret")}
                </label>
                <select
                  id="edit-model-secret"
                  value={secretID}
                  onChange={(e) => setSecretID(e.target.value)}
                  className="flex h-9 w-full rounded-md border border-line bg-surface px-3 py-1.5 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong"
                  disabled={newAPIKey.trim() !== ""}
                >
                  <option value="">
                    {t("models.editModel.credentialBinding.boundSecretNone")}
                  </option>
                  {activeSecrets.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name} ({s.masked})
                    </option>
                  ))}
                </select>
              </div>
              <Field
                id="edit-model-new-key"
                label={t("models.editModel.credentialBinding.rotateNewKey")}
                value={newAPIKey}
                onChange={setNewAPIKey}
                placeholder={t("models.editModel.credentialBinding.rotateHint")}
                hint={t("models.editModel.credentialBinding.rotateExplain")}
                type="password"
              />
            </div>
          )}

          {isCredentialRef && (
            <div className="grid gap-3 rounded-md bg-surface-subtle/60 p-3">
              <div className="grid gap-1.5">
                <label
                  className="text-sm font-medium text-fg-muted"
                  htmlFor="edit-model-credential-kind"
                >
                  {t("models.editModel.credentialBinding.kindCode")}
                  <span className="ml-0.5 text-danger">*</span>
                </label>
                <CredentialKindCombobox
                  workspaceID={workspaceID}
                  value={credentialKindCode}
                  onChange={setCredentialKindCode}
                  className="w-full"
                />
                <span className="text-xs text-fg-faint">
                  {t("models.editModel.credentialBinding.kindCodeHint")}
                </span>
              </div>
            </div>
          )}

          {errMsg && (
            <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">
              {errMsg}
            </p>
          )}

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => onOpenChange(false)}
              disabled={pending}
            >
              {tc("actions.cancel")}
            </Button>
            <Button type="submit" size="sm" disabled={!canSubmit}>
              {pending ? tc("states.loading") : t("models.editModel.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
