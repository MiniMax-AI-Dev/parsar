import { useMemo, useState } from "react"
import { Check, Loader2, Search } from "lucide-react"
import { useTranslation } from "react-i18next"

import { SandboxPanel } from "../../../components/admin/SandboxPanel"
import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Field } from "../../../components/ui/label"
import { Select } from "../../../components/ui/select"
import { Skeleton } from "../../../components/ui/skeleton"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../components/ui/dialog"
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "../../../components/ui/alert-dialog"
import { ApiError } from "../../../lib/api-client"
import {
  useAgentCapabilitiesQuery,
  useCapabilitiesQuery,
  useCapabilityVersionsQuery,
  useDeleteAgentCapabilityMutation,
  useEnableAgentCapabilityMutation,
  useToggleBuiltinCapabilityMutation,
} from "../../../lib/api-capabilities"
import { useMyCredentials } from "../../../lib/api-credentials"
import { useSecrets } from "../../../lib/api-secrets"
import { agentExecutionPlacement } from "../../../lib/agent-runtime"
import { agentEngineLabel, agentEngineOf, agentEngineSupportsCapability, agentEnginesSupportingCapability } from "../../../lib/agent-view-model"
import { credentialBinding, hasCredentialKind, sharedSecretsForKind } from "../../../lib/credential-bindings"
import type { Agent, AgentCapability, AgentDetail, Capability, CapabilityVersion, Secret, UserCredential } from "../../../lib/api-types"
import { cn } from "../../../lib/utils"
import { CredentialBindingSelect } from "../../../components/admin/CredentialBindingSelect"
import { CapabilityTypeBadge } from "../CapabilitiesPage"
import { UpgradeCapabilityDialog } from "../capabilities/UpgradeCapabilityDialog"
import { credentialKindLabel } from "../capability-ui"
import { AgentConfigSummary } from "./AgentConfigSummary"
import { DetailSection, InlineError } from "./DetailSection"

type CapabilityCardItem = { capability?: Capability; binding?: AgentCapability }

/* Native <select> inside the shared CredentialBindingSelect, dressed as ui/Select. */
const SELECT_CLASS = "app-shadow-control h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-fg focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent"

function runtimeOf(agent: Agent): "local" | "sandbox" {
  const placement = agentExecutionPlacement(agent)
  return placement === "local" ? "local" : "sandbox"
}

function capabilityFromBinding(binding: AgentCapability, workspaceID: string | null): Capability | undefined {
  if (!binding.capability_id || !binding.capability_version_id) return undefined
  return {
    id: binding.capability_id,
    workspace_id: binding.workspace_id ?? workspaceID ?? "",
    type: binding.type ?? "mcp",
    name: binding.name ?? tCapabilityFallback(binding.capability_id),
    description: binding.description ?? "",
    visibility: binding.visibility,
    status: binding.status ?? "active",
    required_credentials: binding.required_credentials,
    deprecated_at: binding.deprecated_at,
    from_marketplace: !!binding.workspace_id && binding.workspace_id !== workspaceID,
    source_workspace_id: binding.workspace_id,
    source_workspace_name: binding.source_workspace_name,
    latest_version_id: binding.latest_version_id,
    latest_version: binding.latest_version,
    latest_version_created_at: binding.latest_version_created_at,
    pinned_version_id: binding.capability_version_id,
    pinned_version: binding.version,
    creator_id: "",
    created_at: binding.latest_version_created_at ?? new Date().toISOString(),
    updated_at: binding.latest_version_created_at ?? new Date().toISOString(),
  }
}

function tCapabilityFallback(capabilityID: string) {
  return `Capability ${capabilityID.slice(0, 8)}`
}

function latestCapabilityVersion(capability: Capability): CapabilityVersion | undefined {
  return capability.latest_version_id
    ? {
        id: capability.latest_version_id,
        capability_id: capability.id,
        version: capability.latest_version ?? capability.latest_published_version ?? "—",
        created_at: capability.latest_version_created_at ?? capability.created_at ?? new Date().toISOString(),
      } as CapabilityVersion
    : undefined
}

function requiredCredentialKinds(capability: Capability) {
  return (capability.required_credentials ?? []).filter((rc) => rc.required)
}

function boundSharedSecretID(agent: Agent, binding: AgentCapability | undefined, kind: string) {
  const capabilityBinding = credentialBinding(binding?.configuration, kind)
  if (capabilityBinding) return capabilityBinding.secretID
  return credentialBinding(agent.config, kind)?.secretID ?? ""
}

function hasUsableCredential(agent: Agent, binding: AgentCapability | undefined, credentials: UserCredential[], sharedSecrets: Secret[], kind: string, catalogID: string) {
  const sharedID = boundSharedSecretID(agent, binding, kind)
  if (sharedID && sharedSecretsForKind(sharedSecrets, kind, catalogID).some((secret) => secret.id === sharedID)) return true
  return agent.visibility !== "public" && hasCredentialKind(credentials, kind)
}

function catalogIDFromVersion(version: CapabilityVersion | undefined) {
  const payload = version?.source_payload
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) return ""
  const catalogID = (payload as Record<string, unknown>).catalog_id
  return typeof catalogID === "string" ? catalogID.trim() : ""
}

function useCapabilityVersions(
  workspaceID: string | null,
  capability: Capability | undefined,
  enabled: boolean,
) {
  const versionsQ = useCapabilityVersionsQuery(workspaceID, enabled ? capability?.id ?? null : null)
  const versions = versionsQ.data?.versions ?? []
  const latest = versions[0] ?? (capability ? latestCapabilityVersion(capability) : undefined)
  return { latest, versions, versionsQ }
}

/** One hairline-separated capability row: name and flags left, controls right. */
function CapabilityRow({ children, className }: { children: React.ReactNode; className?: string }) {
  return <li className={cn("border-b border-line py-3 last:border-b-0", className)}>{children}</li>
}

function BuiltinCapabilityCard({
  binding,
  agent,
  workspaceID,
  isAdmin,
  onToast,
}: {
  binding: AgentCapability
  agent: Agent
  workspaceID: string | null
  isAdmin: boolean
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const capability = binding.capability
  const key = binding.builtin_key ?? capability?.builtin_key ?? ""
  const mut = useToggleBuiltinCapabilityMutation(workspaceID, agent.id)
  const enabled = binding.enabled
  const onToggle = (next: boolean) => {
    if (!key || mut.isPending) return
    mut.mutate(
      { key, enabled: next },
      { onError: (e) => onToast(t("agents.detail.capabilities.builtin.toggleError", { message: e instanceof Error ? e.message : String(e) })) },
    )
  }
  return (
    <CapabilityRow>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability?.name ?? key}</span>
            {capability?.type && <CapabilityTypeBadge type={capability.type} />}
            <Badge variant="neutral">{t("agents.detail.capabilities.builtin.badge")}</Badge>
          </div>
          {capability?.description && <p className="mt-0.5 text-xs text-fg-muted">{capability.description}</p>}
        </div>
        <label className={cn("flex h-7 shrink-0 items-center gap-2 text-sm text-fg", isAdmin ? "cursor-pointer" : "cursor-not-allowed opacity-50")}>
          <input
            type="checkbox"
            className="h-3.5 w-3.5 accent-accent"
            checked={enabled}
            disabled={!isAdmin || mut.isPending}
            onChange={(e) => onToggle(e.target.checked)}
          />
          <span>{enabled ? t("agents.detail.capabilities.builtin.on") : t("agents.detail.capabilities.builtin.off")}</span>
        </label>
      </div>
    </CapabilityRow>
  )
}

function CapabilityCard({
  item,
  agent,
  workspaceID,
  credentials,
  sharedSecrets,
  mode,
  onToast,
}: {
  item: CapabilityCardItem
  agent: Agent
  workspaceID: string | null
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  mode: "enabled" | "available"
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const capability = item.capability
  const binding = item.binding
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, mode === "enabled")
  const boundVersion = versions.find((version) => version.id === binding?.capability_version_id) ?? (binding?.capability_version_id && capability?.pinned_version ? { id: binding.capability_version_id, capability_id: capability.id, version: capability.pinned_version, created_at: capability.latest_version_created_at ?? capability.created_at } as CapabilityVersion : undefined)
  const catalogID = catalogIDFromVersion(boundVersion ?? latest)
  const versionDeleted = !!binding && !versionsQ.isLoading && !boundVersion && !capability?.latest_version_id
  const missingCredential = capability
    ? requiredCredentialKinds(capability).some((rc) => !hasUsableCredential(agent, binding, credentials, sharedSecrets, rc.kind, catalogID))
    : false
  const fromMarketplace = !!capability?.from_marketplace || (!!capability?.source_workspace_id && capability.source_workspace_id !== workspaceID)
  const deprecated = !!capability?.deprecated_at

  if (!capability && binding) {
    return (
      <CapabilityRow>
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <InlineError className="font-medium">{t("agents.detail.capabilities.deletedCapability.title")}</InlineError>
            <p className="mt-0.5 pl-5 text-xs text-fg-muted">{t("agents.detail.capabilities.deletedCapability.description")}</p>
          </div>
          <RemoveCapabilityDialog
            agent={agent}
            binding={binding}
            capabilityName={t("agents.detail.capabilities.deletedCapability.fallbackName")}
            workspaceID={workspaceID}
            onToast={onToast}
          />
        </div>
      </CapabilityRow>
    )
  }
  if (!capability) return null

  const agentEngine = agentEngineOf(agent)
  const incompatible = !agentEngineSupportsCapability(agentEngine, capability.type)
  const compatibilityMessage = incompatible
    ? t("agents.detail.capabilities.compatibility.unsupported", {
        engine: t(agentEngineLabel(agentEngine)),
        type: t(`agents.detail.capabilities.compatibility.types.${capability.type}`),
        engines: agentEnginesSupportingCapability(capability.type).map((engine) => t(agentEngineLabel(engine))).join(", "),
      })
    : ""
  const versionLabel = binding
    ? (boundVersion ? `v${boundVersion.version}` : "v—")
    : latest
      ? `v${latest.version} · ${t("agents.detail.capabilities.switchDialog.latest")}`
      : null

  return (
    <CapabilityRow>
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability.name}</span>
            <CapabilityTypeBadge type={capability.type} />
            {fromMarketplace && <Badge variant="primary">{t("agents.detail.capabilities.marketplace.badge")}</Badge>}
            {incompatible && <Badge variant="destructive" dot>{t("agents.detail.capabilities.compatibility.badge")}</Badge>}
            {missingCredential && <Badge variant="destructive" dot>{t("agents.detail.capabilities.credential.missingBadge")}</Badge>}
            {versionDeleted && <Badge variant="destructive" dot>{t("agents.detail.capabilities.bindings.versionDeleted.warning")}</Badge>}
            {versionDeleted && versions.length > 0 && binding && (
              <CapabilityVersionDialog
                mode="switch"
                agent={agent}
                capability={capability}
                binding={binding}
                workspaceID={workspaceID}
                triggerLabel={t("agents.detail.capabilities.bindings.versionDeleted.switchAction")}
                triggerVariant="link"
                onToast={onToast}
              />
            )}
          </div>
          {capability.description && <p className="mt-0.5 text-xs text-fg-muted">{capability.description}</p>}
          {fromMarketplace && <p className="mt-0.5 text-xs text-fg-muted">{t("agents.detail.capabilities.marketplace.source", { source: capability.source_workspace_name ?? "—", version: boundVersion?.version ?? capability.pinned_version ?? latest?.version ?? "—" })}</p>}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          {versionLabel && <span className="font-mono text-xs text-fg-muted">{versionLabel}</span>}
          {mode === "available" ? (
            <CapabilityVersionDialog
              mode="enable"
              agent={agent}
              capability={capability}
              credentials={credentials}
              sharedSecrets={sharedSecrets}
              workspaceID={workspaceID}
              disabled={incompatible}
              onToast={onToast}
            />
          ) : binding ? (
            <>
              {versions.length > 1 && !versionDeleted && !fromMarketplace && (
                <CapabilityVersionDialog
                  mode="switch"
                  agent={agent}
                  capability={capability}
                  binding={binding}
                  workspaceID={workspaceID}
                  onToast={onToast}
                />
              )}
              <RemoveCapabilityDialog
                agent={agent}
                binding={binding}
                capabilityName={capability.name}
                workspaceID={workspaceID}
                onToast={onToast}
              />
            </>
          ) : null}
        </div>
      </div>

      {mode === "enabled" && deprecated && (
        <InlineError className="mt-2">
          {t("agents.detail.capabilities.marketplace.deprecatedBanner", { version: boundVersion?.version ?? capability.pinned_version ?? "—" })}
        </InlineError>
      )}

      {incompatible && <InlineError className="mt-2">{compatibilityMessage}</InlineError>}

      {mode === "enabled" && fromMarketplace && binding && latest && latest.id !== binding.capability_version_id && (
        <UpgradeCapabilityDialog
          agent={agent}
          capability={capability}
          binding={binding}
          latestVersion={latest}
          workspaceID={workspaceID}
          disabled={deprecated}
          onToast={onToast}
        />
      )}

      <CredentialStatus capability={capability} binding={binding} agent={agent} credentials={credentials} sharedSecrets={sharedSecrets} catalogID={catalogID} />
    </CapabilityRow>
  )
}

function CredentialStatus({
  capability,
  binding,
  agent,
  credentials,
  sharedSecrets,
  catalogID,
}: {
  capability: Capability
  binding?: AgentCapability
  agent: Agent
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  catalogID: string
}) {
  const { t, i18n } = useTranslation("admin")
  const requiredCreds = capability.required_credentials ?? []
  if (requiredCreds.length === 0) {
    return <p className="mt-2 text-xs text-fg-muted">{t("agents.detail.capabilities.credential.none")}</p>
  }
  return (
    <ul className="m-0 mt-2 flex list-none flex-col gap-1 p-0">
      {requiredCreds.map((rc) => {
        const sharedID = boundSharedSecretID(agent, binding, rc.kind)
        const sharedSecret = sharedSecretsForKind(sharedSecrets, rc.kind, catalogID).find((secret) => secret.id === sharedID)
        const credential = agent.visibility === "public" ? undefined : credentials.find((cred) => cred.kind === rc.kind)
        const available = sharedSecret ?? credential
        const label = credentialKindLabel(rc.kind, i18n.language, rc.kind)
        return (
          <li key={rc.kind} className="flex items-center gap-1.5 text-xs text-fg">
            {available ? (
              <Check className="h-3.5 w-3.5 shrink-0 text-status-completed" strokeWidth={1.5} aria-hidden="true" />
            ) : (
              <span className="inline-flex h-3.5 w-3.5 shrink-0 items-center justify-center" aria-hidden="true">
                <span className="h-1.5 w-1.5 rounded-full bg-status-failed" />
              </span>
            )}
            <span className="min-w-0 truncate">
              {available
                ? t("agents.detail.capabilities.credential.present", { kind: label, name: sharedSecret?.name || credential?.display_name || t("agents.detail.capabilities.credential.defaultName") })
                : t("agents.detail.capabilities.credential.missing", { kind: label })}
            </span>
            {!sharedSecret && <CredentialLink kind={rc.kind} className="shrink-0 text-xs text-fg underline underline-offset-4" />}
          </li>
        )
      })}
    </ul>
  )
}

function CredentialLink({ kind, className, children }: { kind?: string; className?: string; children?: React.ReactNode }) {
  const { t } = useTranslation("admin")
  if (!kind) return null
  return (
    <a className={className ?? "text-sm text-fg underline underline-offset-4"} href={credentialURL(kind)}>
      {children ?? t("agents.detail.capabilities.credential.addCta")}
    </a>
  )
}

function credentialURL(kind: string) {
  const current = window.location.pathname + window.location.search
  return `?profile=credentials&kind=${encodeURIComponent(kind)}&returnTo=${encodeURIComponent(current)}`
}

function mutationError(error: unknown) {
  return error instanceof ApiError ? error.envelope.message : error instanceof Error ? error.message : null
}

function MutationError({ error }: { error: unknown }) {
  const message = mutationError(error)
  return message ? <InlineError>{message}</InlineError> : null
}

function VersionSelect({ versions, value, onChange }: { versions: CapabilityVersion[]; value: string; onChange: (value: string) => void }) {
  const { t } = useTranslation("admin")
  return (
    <Select value={value} onChange={(event) => onChange(event.target.value)}>
      {versions.map((version, index) => (
        <option key={version.id} value={version.id}>v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}</option>
      ))}
    </Select>
  )
}

function EnableCredentialBindingList({
  requiredKinds,
  credentials,
  sharedSecrets,
  catalogID,
  publicAgent,
  bindings,
  onChange,
}: {
  requiredKinds: { kind: string }[]
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  catalogID: string
  publicAgent: boolean
  bindings: Record<string, string>
  onChange: (kind: string, secretID: string) => void
}) {
  const { t, i18n } = useTranslation("admin")
  return (
    <div className="flex flex-col gap-3">
      {requiredKinds.map((rc) => {
        const kindSecrets = sharedSecretsForKind(sharedSecrets, rc.kind, catalogID)
        const selectedSecretID = bindings[rc.kind] ?? ""
        const hasPersonal = !publicAgent && hasCredentialKind(credentials, rc.kind)
        const ready = !!selectedSecretID || hasPersonal
        return (
          <Field key={rc.kind} label={credentialKindLabel(rc.kind, i18n.language, rc.kind)}>
            <CredentialBindingSelect
              value={selectedSecretID}
              secrets={kindSecrets}
              allowPersonal={!publicAgent}
              personalLabel={t("credentialCheck.sourcePersonal")}
              personalPlaceholder={t("credentialCheck.sharedPlaceholder")}
              sharedLabel={t("credentialCheck.sourceShared")}
              onChange={(value) => onChange(rc.kind, value)}
              className={SELECT_CLASS}
            />
            {!ready && (
              <InlineError className="mt-1 text-xs">
                {publicAgent ? t("credentialCheck.sharedNoneAvailable") : t("credentialCheck.personalYouMissing")}
              </InlineError>
            )}
          </Field>
        )
      })}
    </div>
  )
}

function CapabilityVersionDialog({
  mode,
  agent,
  capability,
  credentials = [],
  sharedSecrets = [],
  workspaceID,
  binding,
  triggerLabel,
  triggerVariant = "ghost",
  disabled = false,
  onToast,
}: {
  mode: "enable" | "switch"
  agent: Agent
  capability: Capability
  credentials?: UserCredential[]
  sharedSecrets?: Secret[]
  workspaceID: string | null
  binding?: AgentCapability
  triggerLabel?: string
  triggerVariant?: "ghost" | "link"
  disabled?: boolean
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const mut = useEnableAgentCapabilityMutation(workspaceID, agent.id)
  const [selected, setSelected] = useState(binding?.capability_version_id ?? "")
  const [credentialBindingChoices, setCredentialBindingChoices] = useState<Record<string, string>>({})
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, open)
  const selectedVersion = selected
    ? versions.find((version) => version.id === selected) ?? (mode === "enable" ? latest : versions[0])
    : mode === "enable" ? latest : versions[0]
  const requiredKinds = useMemo(
    () => mode === "enable" ? requiredCredentialKinds(capability) : [],
    [capability, mode],
  )
  const catalogID = catalogIDFromVersion(selectedVersion)
  const defaultCredentialBindings = useMemo(() => {
    const defaults: Record<string, string> = {}
    for (const rc of requiredKinds) {
      const kindSecrets = sharedSecretsForKind(sharedSecrets, rc.kind, catalogID)
      const oauthSecret = kindSecrets.find(() => rc.kind === "mcp_oauth")
      if (oauthSecret) defaults[rc.kind] = oauthSecret.id
      else if (agent.visibility === "public" && kindSecrets[0]) defaults[rc.kind] = kindSecrets[0].id
    }
    return defaults
  }, [agent.visibility, catalogID, requiredKinds, sharedSecrets])
  const credentialBindings = { ...defaultCredentialBindings, ...credentialBindingChoices }
  const missingRequiredCredential = requiredKinds.some((rc) => {
    const selectedSecretID = credentialBindings[rc.kind]
    if (selectedSecretID && sharedSecretsForKind(sharedSecrets, rc.kind, catalogID).some((secret) => secret.id === selectedSecretID)) {
      return false
    }
    return agent.visibility === "public" || !hasCredentialKind(credentials, rc.kind)
  })
  const canSubmit = !!selectedVersion
    && !mut.isPending
    && !disabled
    && (mode === "enable" ? !missingRequiredCredential : selectedVersion.id !== binding?.capability_version_id)

  const submit = () => {
    if (!selectedVersion) return
    const capabilityBindings = Object.fromEntries(
      requiredKinds.map(({ kind }) => {
        const secretID = credentialBindings[kind]
        return [kind, secretID
          ? { source: "shared", secret_id: secretID }
          : { source: "personal" }]
      }),
    )
    mut.mutate({
      capabilityVersionID: selectedVersion.id,
      configuration: mode === "enable"
        ? { credential_bindings: capabilityBindings }
        : binding?.configuration,
    }, {
      onSuccess: () => {
        setOpen(false)
        onToast(mode === "enable"
          ? t("agents.detail.capabilities.toast.enabled", { cap: capability.name, agent: agent.name, version: selectedVersion.version })
          : t("agents.detail.capabilities.toast.switched", { cap: capability.name, version: selectedVersion.version }))
      },
    })
  }
  const isSwitch = mode === "switch"
  const confirmLabel = isSwitch
    ? selectedVersion
      ? t("agents.detail.capabilities.actions.switchConfirm", { version: selectedVersion.version })
      : t("agents.detail.capabilities.actions.switchVersion")
    : t("agents.detail.capabilities.actions.enableConfirm")

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button
        variant={isSwitch ? triggerVariant : "outline"}
        size="sm"
        disabled={disabled}
        onClick={() => setOpen(true)}
      >
        {triggerLabel ?? t(isSwitch ? "agents.detail.capabilities.actions.switchVersion" : "agents.detail.capabilities.actions.enable")}
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(isSwitch ? "agents.detail.capabilities.switchDialog.title" : "agents.detail.capabilities.enableDialog.title", { agent: agent.name, cap: capability.name })}</DialogTitle>
          <DialogDescription>{t(isSwitch ? "agents.detail.capabilities.switchDialog.description" : "agents.detail.capabilities.enableDialog.description")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          {isSwitch ? (
            versionsQ.isLoading ? <Skeleton className="h-28 w-full" /> : (
              <ul className="m-0 list-none p-0">
                {versions.map((version, index) => (
                  <li key={version.id}>
                    <label className="flex h-7 cursor-pointer items-center gap-2 text-sm text-fg">
                      <input type="radio" name="capability-version" className="h-3.5 w-3.5 accent-accent" checked={selected === version.id} onChange={() => setSelected(version.id)} />
                      <span className="font-mono text-xs">v{version.version}</span>
                      {index === 0 && <span className="text-xs text-fg-muted">· {t("agents.detail.capabilities.switchDialog.latest")}</span>}
                      {version.id === binding?.capability_version_id && <span className="text-xs text-fg-muted">· {t("agents.detail.capabilities.switchDialog.current")}</span>}
                    </label>
                  </li>
                ))}
              </ul>
            )
          ) : (
            <>
              <Field label={t("agents.detail.capabilities.enableDialog.version")}>
                {versionsQ.isLoading ? <Skeleton className="h-7 w-full" /> : <VersionSelect versions={versions} value={selectedVersion?.id ?? ""} onChange={setSelected} />}
              </Field>
              {requiredKinds.length > 0 ? (
                <EnableCredentialBindingList
                  requiredKinds={requiredKinds}
                  credentials={credentials}
                  sharedSecrets={sharedSecrets}
                  catalogID={catalogID}
                  publicAgent={agent.visibility === "public"}
                  bindings={credentialBindings}
                  onChange={(kind, secretID) => setCredentialBindingChoices((current) => ({ ...current, [kind]: secretID }))}
                />
              ) : (
                <p className="flex items-center gap-1.5 text-sm text-fg">
                  <Check className="h-3.5 w-3.5 shrink-0 text-status-completed" strokeWidth={1.5} aria-hidden="true" />
                  {t("agents.detail.capabilities.enableDialog.noCredential")}
                </p>
              )}
            </>
          )}
          {isSwitch && <p className="text-sm text-fg-muted">{t("agents.detail.capabilities.switchDialog.notice", { agent: agent.name })}</p>}
          <MutationError error={mut.error} />
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => setOpen(false)} disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button>
          <Button disabled={!canSubmit} onClick={submit}>{mut.isPending && <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />}{confirmLabel}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function RemoveCapabilityDialog({
  agent,
  binding,
  capabilityName,
  workspaceID,
  onToast,
}: {
  agent: Agent
  binding: AgentCapability
  capabilityName: string
  workspaceID: string | null
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const mut = useDeleteAgentCapabilityMutation(workspaceID, agent.id)
  const submit = () => {
    mut.mutate(binding.capability_version_id, {
      onSuccess: () => {
        setOpen(false)
        onToast(t("agents.detail.capabilities.toast.removed", { cap: capabilityName, agent: agent.name }))
      },
    })
  }

  return (
    <AlertDialog open={open} onOpenChange={setOpen}>
      <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>{t("agents.detail.capabilities.actions.remove")}</Button>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("agents.detail.capabilities.removeDialog.title", { agent: agent.name, cap: capabilityName })}</AlertDialogTitle>
          <AlertDialogDescription>{t("agents.detail.capabilities.removeDialog.description")}</AlertDialogDescription>
        </AlertDialogHeader>
        <ul className="m-0 flex list-disc flex-col gap-1 pl-4 text-sm text-fg-muted">
          <li>{t("agents.detail.capabilities.removeDialog.impactRun")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCapability")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCredential")}</li>
        </ul>
        <MutationError error={mut.error} />
        <AlertDialogFooter>
          <AlertDialogCancel asChild><Button variant="outline" disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button></AlertDialogCancel>
          <Button variant="destructive" disabled={mut.isPending} onClick={submit}>{mut.isPending && <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />}{t("agents.detail.capabilities.actions.removeConfirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

/* ------------------------------------------------------------------ */
/*  AgentConfigTab — "Config" tab.                                     */
/* ------------------------------------------------------------------ */

export function AgentConfigTab({
  agent,
  workspaceID,
  workspaceRole,
  modelLabel,
  onToast,
}: {
  agent: AgentDetail
  workspaceID: string | null
  workspaceRole?: string
  modelLabel: string
  onToast: (message: string) => void
}) {
  const agentCapabilitiesQ = useAgentCapabilitiesQuery(workspaceID, agent.id)
  const workspaceCapabilitiesQ = useCapabilitiesQuery(workspaceID)
  const credentialsQ = useMyCredentials()
  const secretsQ = useSecrets(workspaceID)
  const credentials = credentialsQ.data?.credentials ?? []
  const sharedSecrets = useMemo(
    () => (secretsQ.data?.secrets ?? []).filter((secret) => secret.kind === "capability_inline" && secret.status === "active"),
    [secretsQ.data?.secrets],
  )
  const installedCapabilities = agentCapabilitiesQ.data?.installed ?? []
  const availableCapabilities = agentCapabilitiesQ.data?.available ?? workspaceCapabilitiesQ.data?.capabilities ?? []
  const installedIDs = new Set(installedCapabilities.map((item) => item.capability_id))
  const enabledCaps = installedCapabilities
    .filter((item) => item.enabled || item.built_in)
    .map((item) => {
      const raw = item as AgentCapability & { capability?: Capability }
      return {
        binding: item,
        capability: raw.capability
          ?? availableCapabilities.find((cap) => cap.id === item.capability_id)
          ?? capabilityFromBinding(item, workspaceID),
      }
    })
  const installable = availableCapabilities.filter((cap) => !installedIDs.has(cap.id))
  const canManageCapabilities = workspaceRole === "owner"
    || workspaceRole === "admin"
    || workspaceRole === "member"

  return (
    <>
      <AgentConfigSummary agent={agent} modelLabel={modelLabel} />

      {runtimeOf(agent) === "sandbox" && (
        <div className="mt-6">
          <SandboxPanel workspaceID={workspaceID} agentID={agent.id} />
        </div>
      )}

      <ConfigCapabilitiesSection
        agent={agent}
        workspaceID={workspaceID}
        isAdmin={canManageCapabilities}
        enabledCaps={enabledCaps}
        installable={installable}
        credentials={credentials}
        sharedSecrets={sharedSecrets}
        loading={agentCapabilitiesQ.isLoading || workspaceCapabilitiesQ.isLoading}
        error={agentCapabilitiesQ.error ?? workspaceCapabilitiesQ.error}
        onToast={onToast}
      />
    </>
  )
}

function ConfigCapabilitiesSection({
  agent,
  workspaceID,
  isAdmin,
  enabledCaps,
  installable,
  credentials,
  sharedSecrets,
  loading,
  error,
  onToast,
}: {
  agent: Agent
  workspaceID: string | null
  isAdmin: boolean
  enabledCaps: Array<{ binding: AgentCapability; capability?: Capability }>
  installable: Capability[]
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  loading: boolean
  error: unknown
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [addOpen, setAddOpen] = useState(false)
  const title = t("agents.detail.config.capabilities.title")

  if (loading) {
    return (
      <DetailSection title={title}>
        <div className="flex flex-col gap-3 pt-1">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-3 w-2/3" />)}
        </div>
      </DetailSection>
    )
  }
  if (error) {
    return (
      <DetailSection title={title}>
        <ErrorState
          title={t("agents.detail.config.capabilities.loadError")}
          description={error instanceof Error ? error.message : undefined}
        />
      </DetailSection>
    )
  }

  return (
    <DetailSection
      title={title}
      meta={enabledCaps.length || undefined}
      action={
        isAdmin && installable.length > 0 ? (
          <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
            {t("agents.detail.config.capabilities.add")}
          </Button>
        ) : undefined
      }
    >
      {enabledCaps.length === 0 ? (
        <EmptyState title={t("agents.detail.config.capabilities.empty")} className="py-8" />
      ) : (
        <ul className="m-0 list-none border-t border-line p-0">
          {enabledCaps.map((item) =>
            item.binding.built_in ? (
              <BuiltinCapabilityCard
                key={item.binding.id ?? item.capability?.id}
                binding={item.binding}
                agent={agent}
                workspaceID={workspaceID}
                isAdmin={isAdmin}
                onToast={onToast}
              />
            ) : (
              <CapabilityCard
                key={item.binding.id ?? item.capability?.id}
                item={item}
                agent={agent}
                workspaceID={workspaceID}
                credentials={credentials}
                sharedSecrets={sharedSecrets}
                mode="enabled"
                onToast={onToast}
              />
            )
          )}
        </ul>
      )}

      <AddCapabilityDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        agent={agent}
        workspaceID={workspaceID}
        installable={installable}
        credentials={credentials}
        sharedSecrets={sharedSecrets}
        onToast={onToast}
      />
    </DetailSection>
  )
}

function AddCapabilityDialog({
  open,
  onOpenChange,
  agent,
  workspaceID,
  installable,
  credentials,
  sharedSecrets,
  onToast,
}: {
  open: boolean
  onOpenChange: (next: boolean) => void
  agent: Agent
  workspaceID: string | null
  installable: Capability[]
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [q, setQ] = useState("")
  const filtered = installable.filter((cap) => {
    if (!q.trim()) return true
    const needle = q.toLowerCase()
    return cap.name.toLowerCase().includes(needle)
      || (cap.description ?? "").toLowerCase().includes(needle)
  })

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("agents.detail.config.capabilities.add")}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            <Input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("agents.detail.config.capabilities.add")}
              aria-label={t("agents.detail.config.capabilities.add")}
              className="pl-7"
              autoFocus
            />
          </div>
          <div className="max-h-80 overflow-y-auto">
            {filtered.length === 0 ? (
              <p className="py-6 text-center text-sm text-fg-muted">
                {t("agents.detail.capabilities.emptyAvailable")}
              </p>
            ) : (
              <ul className="m-0 list-none border-t border-line p-0">
                {filtered.map((capability) => (
                  <CapabilityCard
                    key={capability.id}
                    item={{ capability }}
                    agent={agent}
                    workspaceID={workspaceID}
                    credentials={credentials}
                    sharedSecrets={sharedSecrets}
                    mode="available"
                    onToast={(msg) => {
                      onToast(msg)
                      onOpenChange(false)
                    }}
                  />
                ))}
              </ul>
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("agents.detail.capabilities.actions.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
