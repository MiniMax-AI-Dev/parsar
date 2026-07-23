import { useState } from "react"
import { Loader2, Plus, Search } from "lucide-react"
import { useTranslation } from "react-i18next"

import { SandboxPanel } from "../../../components/admin/SandboxPanel"
import { Badge } from "../../../components/ui/badge"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
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
import { agentExecutionPlacement } from "../../../lib/agent-runtime"
import type { Agent, AgentCapability, AgentDetail, Capability, CapabilityVersion, UserCredential } from "../../../lib/api-types"
import { CapabilityTypeBadge } from "../CapabilitiesPage"
import { UpgradeCapabilityDialog } from "../capabilities/UpgradeCapabilityDialog"
import { credentialKindLabel } from "../capability-ui"
import { AgentConfigSummary } from "./AgentConfigSummary"

type CapabilityCardItem = { capability?: Capability; binding?: AgentCapability }

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

function hasCredentialKind(credentials: UserCredential[], kind: string) {
  return credentials.some((credential) => credential.kind === kind)
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
    <div className="rounded-md border border-line p-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability?.name ?? key}</span>
            {capability?.type && <CapabilityTypeBadge type={capability.type} />}
            <Badge variant="neutral">{t("agents.detail.capabilities.builtin.badge")}</Badge>
          </div>
          {capability?.description && <p className="mt-1 text-sm text-fg-subtle">{capability.description}</p>}
        </div>
        <label className={`flex shrink-0 items-center gap-2 text-sm ${isAdmin ? "cursor-pointer" : "cursor-not-allowed opacity-60"} text-fg-subtle`}>
          <input
            type="checkbox"
            className="h-4 w-4"
            checked={enabled}
            disabled={!isAdmin || mut.isPending}
            onChange={(e) => onToggle(e.target.checked)}
          />
          <span>{enabled ? t("agents.detail.capabilities.builtin.on") : t("agents.detail.capabilities.builtin.off")}</span>
        </label>
      </div>
    </div>
  )
}

function CapabilityCard({
  item,
  agent,
  workspaceID,
  credentials,
  mode,
  onToast,
}: {
  item: CapabilityCardItem
  agent: Agent
  workspaceID: string | null
  credentials: UserCredential[]
  mode: "enabled" | "available"
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const capability = item.capability
  const binding = item.binding
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, mode === "enabled")
  const boundVersion = versions.find((version) => version.id === binding?.capability_version_id) ?? (binding?.capability_version_id && capability?.pinned_version ? { id: binding.capability_version_id, capability_id: capability.id, version: capability.pinned_version, created_at: capability.latest_version_created_at ?? capability.created_at } as CapabilityVersion : undefined)
  const versionDeleted = !!binding && !versionsQ.isLoading && !boundVersion && !capability?.latest_version_id
  const missingCredential = capability ? requiredCredentialKinds(capability).some((rc) => !hasCredentialKind(credentials, rc.kind)) : false
  const fromMarketplace = !!capability?.from_marketplace || (!!capability?.source_workspace_id && capability.source_workspace_id !== workspaceID)
  const deprecated = !!capability?.deprecated_at
  const border = mode === "available" ? "border-dashed border-line-strong" : "border-line"

  if (!capability && binding) {
    return (
      <div className="rounded-md border border-warning-border bg-warning-subtle/60 p-3">
        <p className="text-sm font-medium text-warning-emphasis">{t("agents.detail.capabilities.deletedCapability.title")}</p>
        <p className="mt-1 text-sm text-warning-emphasis">{t("agents.detail.capabilities.deletedCapability.description")}</p>
        <RemoveCapabilityDialog
          agent={agent}
          binding={binding}
          capabilityName={t("agents.detail.capabilities.deletedCapability.fallbackName")}
          workspaceID={workspaceID}
          onToast={onToast}
        />
      </div>
    )
  }
  if (!capability) return null

  return (
    <div className={`rounded-md border ${border} p-3`}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-sm font-medium text-fg">{capability.name}</span>
            <CapabilityTypeBadge type={capability.type} />
            {fromMarketplace && <Badge variant="primary">{t("agents.detail.capabilities.marketplace.badge")}</Badge>}
            {missingCredential && <Badge variant="destructive">{t("agents.detail.capabilities.credential.missingBadge")}</Badge>}
            {versionDeleted && <Badge variant="destructive">{t("agents.detail.capabilities.bindings.versionDeleted.warning")}</Badge>}
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
          {capability.description && <p className="mt-1 text-sm text-fg-subtle">{capability.description}</p>}
          {fromMarketplace && <p className="mt-1 text-sm text-fg-subtle">{t("agents.detail.capabilities.marketplace.source", { source: capability.source_workspace_name ?? "—", version: boundVersion?.version ?? capability.pinned_version ?? latest?.version ?? "—" })}</p>}
        </div>
      </div>

      {mode === "enabled" && deprecated && (
        <div className="mt-3 rounded-md border border-danger-border bg-danger-subtle px-3 py-2 text-sm leading-5 text-danger-emphasis">
          {t("agents.detail.capabilities.marketplace.deprecatedBanner", { version: boundVersion?.version ?? capability.pinned_version ?? "—" })}
        </div>
      )}

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

      <CredentialStatus capability={capability} credentials={credentials} />

      <div className="mt-3 flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          {binding ? (
            <Badge variant="neutral" className="font-mono">{boundVersion ? `v${boundVersion.version}` : "v—"}</Badge>
          ) : latest ? (
            <Badge variant="neutral" className="font-mono">v{latest.version} · {t("agents.detail.capabilities.switchDialog.latest")}</Badge>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {mode === "available" ? (
            <CapabilityVersionDialog
              mode="enable"
              agent={agent}
              capability={capability}
              credentials={credentials}
              workspaceID={workspaceID}
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
    </div>
  )
}

function CredentialStatus({ capability, credentials }: { capability: Capability; credentials: UserCredential[] }) {
  const { t, i18n } = useTranslation("admin")
  const requiredCreds = capability.required_credentials ?? []
  if (requiredCreds.length === 0) {
    return <p className="mt-3 text-sm text-fg-subtle">{t("agents.detail.capabilities.credential.none")}</p>
  }
  return (
    <div className="mt-3 space-y-1.5">
      {requiredCreds.map((rc) => {
        const credential = credentials.find((cred) => cred.kind === rc.kind)
        const label = credentialKindLabel(rc.kind, i18n.language, rc.kind)
        return (
          <div key={rc.kind} className={`rounded-md border px-3 py-2 text-sm ${credential ? "border-success-border bg-success-subtle text-success-emphasis" : "border-danger-border bg-danger-subtle text-danger-emphasis"}`}>
            {credential ? (
              <span>{t("agents.detail.capabilities.credential.present", { kind: label, name: credential.display_name || t("agents.detail.capabilities.credential.defaultName") })}</span>
            ) : (
              <span>{t("agents.detail.capabilities.credential.missing", { kind: label })}</span>
            )}
            <CredentialLink kind={rc.kind} className="ml-2 font-medium underline underline-offset-2" />
          </div>
        )
      })}
    </div>
  )
}

function CredentialLink({ kind, className, children }: { kind?: string; className?: string; children?: React.ReactNode }) {
  const { t } = useTranslation("admin")
  if (!kind) return null
  return (
    <a className={className ?? "text-sm font-medium text-fg underline underline-offset-2"} href={credentialURL(kind)}>
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
  return message ? <p className="rounded-md bg-danger-subtle px-3 py-2 text-sm text-danger-emphasis">{message}</p> : null
}

function VersionSelect({ versions, value, onChange }: { versions: CapabilityVersion[]; value: string; onChange: (value: string) => void }) {
  const { t } = useTranslation("admin")
  return (
    <select
      value={value}
      onChange={(event) => onChange(event.target.value)}
      className="h-8 w-full rounded-md border border-line bg-surface px-2 text-sm text-fg focus:outline-none focus:ring-2 focus:ring-line-strong"
    >
      {versions.map((version, index) => (
        <option key={version.id} value={version.id}>v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}</option>
      ))}
    </select>
  )
}

function EnableCredentialStatusList({
  requiredKinds,
  credentials,
}: {
  requiredKinds: { kind: string }[]
  credentials: UserCredential[]
}) {
  const { t } = useTranslation("admin")
  return (
    <div className="space-y-1.5">
      {requiredKinds.map((rc) => {
        const has = hasCredentialKind(credentials, rc.kind)
        return (
          <div
            key={rc.kind}
            className={
              has
                ? "flex items-center gap-2 rounded-md border border-success-border bg-success-subtle px-3 py-1.5 text-sm text-success-emphasis"
                : "flex items-center gap-2 rounded-md border border-warning-border bg-warning-subtle px-3 py-1.5 text-sm text-warning-emphasis"
            }
          >
            <span>{has ? "✓" : "⚠"}</span>
            <span>{rc.kind}</span>
            {!has && <span className="ml-auto text-xs">{t("credentialCheck.personalYouMissing")}</span>}
          </div>
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
  workspaceID,
  binding,
  triggerLabel,
  triggerVariant = "ghost",
  onToast,
}: {
  mode: "enable" | "switch"
  agent: Agent
  capability: Capability
  credentials?: UserCredential[]
  workspaceID: string | null
  binding?: AgentCapability
  triggerLabel?: string
  triggerVariant?: "ghost" | "link"
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const mut = useEnableAgentCapabilityMutation(workspaceID, agent.id)
  const [selected, setSelected] = useState(binding?.capability_version_id ?? "")
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, open)
  const selectedVersion = selected
    ? versions.find((version) => version.id === selected) ?? (mode === "enable" ? latest : versions[0])
    : mode === "enable" ? latest : versions[0]
  const requiredKinds = mode === "enable" ? requiredCredentialKinds(capability) : []
  const missingRequiredCredential = requiredKinds.some((rc) => !hasCredentialKind(credentials, rc.kind))
  const canSubmit = !!selectedVersion
    && !mut.isPending
    && (mode === "enable" ? !missingRequiredCredential : selectedVersion.id !== binding?.capability_version_id)

  const submit = () => {
    if (!selectedVersion) return
    mut.mutate({ capabilityVersionID: selectedVersion.id }, {
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
        variant={isSwitch ? triggerVariant : "default"}
        size="sm"
        className={isSwitch && triggerVariant === "link" ? "h-auto px-1 py-0 text-sm text-danger-emphasis" : undefined}
        onClick={() => setOpen(true)}
      >
        {triggerLabel ?? t(isSwitch ? "agents.detail.capabilities.actions.switchVersion" : "agents.detail.capabilities.actions.enable")}
      </Button>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(isSwitch ? "agents.detail.capabilities.switchDialog.title" : "agents.detail.capabilities.enableDialog.title", { agent: agent.name, cap: capability.name })}</DialogTitle>
          <DialogDescription>{t(isSwitch ? "agents.detail.capabilities.switchDialog.description" : "agents.detail.capabilities.enableDialog.description")}</DialogDescription>
        </DialogHeader>
        <div className={isSwitch ? "space-y-2" : "space-y-4"}>
          {isSwitch ? (
            versionsQ.isLoading ? <Skeleton className="h-28 w-full" /> : versions.map((version, index) => (
              <label key={version.id} className={`flex cursor-pointer items-start gap-3 rounded-md border p-3 ${selected === version.id ? "border-line-strong bg-surface-subtle" : "border-line"}`}>
                <input type="radio" name="capability-version" className="mt-1" checked={selected === version.id} onChange={() => setSelected(version.id)} />
                <span className="flex-1 text-sm text-fg-emphasis">v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}{version.id === binding?.capability_version_id ? ` · ${t("agents.detail.capabilities.switchDialog.current")}` : ""}</span>
              </label>
            ))
          ) : (
            <>
              <div className="grid gap-1.5">
                <label className="text-sm font-medium text-fg-muted">{t("agents.detail.capabilities.enableDialog.version")}</label>
                {versionsQ.isLoading ? <Skeleton className="h-8 w-full" /> : <VersionSelect versions={versions} value={selectedVersion?.id ?? ""} onChange={setSelected} />}
              </div>
              {requiredKinds.length > 0 ? (
                <EnableCredentialStatusList requiredKinds={requiredKinds} credentials={credentials} />
              ) : (
                <div className="rounded-md border border-success-border bg-success-subtle px-3 py-2 text-sm text-success-emphasis">
                  {t("agents.detail.capabilities.enableDialog.noCredential")}
                </div>
              )}
            </>
          )}
          {isSwitch && <p className="rounded-md border border-info-border bg-info-subtle px-3 py-2 text-sm text-info-emphasis">{t("agents.detail.capabilities.switchDialog.notice", { agent: agent.name })}</p>}
          <MutationError error={mut.error} />
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => setOpen(false)} disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button>
          <Button size="sm" disabled={!canSubmit} onClick={submit}>{mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{confirmLabel}</Button>
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
        <ul className="space-y-1 rounded-md border border-line bg-surface-subtle px-3 py-2 text-sm text-fg-muted">
          <li>{t("agents.detail.capabilities.removeDialog.impactRun")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCapability")}</li>
          <li>{t("agents.detail.capabilities.removeDialog.impactCredential")}</li>
        </ul>
        <MutationError error={mut.error} />
        <AlertDialogFooter>
          <AlertDialogCancel asChild><Button variant="outline" size="sm" disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button></AlertDialogCancel>
          <Button variant="destructive" size="sm" disabled={mut.isPending} onClick={submit}>{mut.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}{t("agents.detail.capabilities.actions.removeConfirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}

function Card({ title, className, children }: { title: string; className?: string; children: React.ReactNode }) {
  return (
    <section className={`rounded-lg border border-line bg-surface p-4 ${className ?? ""}`}>
      <h3 className="mb-3 text-base font-semibold text-fg">{title}</h3>
      {children}
    </section>
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
  const credentials = credentialsQ.data?.credentials ?? []
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
    <div className="space-y-4">
      <AgentConfigSummary agent={agent} modelLabel={modelLabel} />

      {runtimeOf(agent) === "sandbox" && (
        <SandboxPanel workspaceID={workspaceID} agentID={agent.id} />
      )}

      <ConfigCapabilitiesSection
        agent={agent}
        workspaceID={workspaceID}
        isAdmin={canManageCapabilities}
        enabledCaps={enabledCaps}
        installable={installable}
        credentials={credentials}
        loading={agentCapabilitiesQ.isLoading || workspaceCapabilitiesQ.isLoading}
        error={agentCapabilitiesQ.error ?? workspaceCapabilitiesQ.error}
        onToast={onToast}
      />
    </div>
  )
}

function ConfigCapabilitiesSection({
  agent,
  workspaceID,
  isAdmin,
  enabledCaps,
  installable,
  credentials,
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
  loading: boolean
  error: unknown
  onToast: (message: string) => void
}) {
  const { t } = useTranslation("admin")
  const [addOpen, setAddOpen] = useState(false)

  if (loading) {
    return (
      <Card title={t("agents.detail.config.capabilities.title")}>
        <div className="space-y-2">
          {Array.from({ length: 3 }).map((_, i) => <Skeleton key={i} className="h-12 w-full" />)}
        </div>
      </Card>
    )
  }
  if (error) {
    return (
      <ErrorState
        title={t("agents.detail.config.capabilities.loadError")}
        description={error instanceof Error ? error.message : undefined}
      />
    )
  }

  return (
    <section className="rounded-lg border border-line bg-surface p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-base font-semibold text-fg">
          {t("agents.detail.config.capabilities.title")}
        </h3>
        {isAdmin && installable.length > 0 && (
          <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
            {t("agents.detail.config.capabilities.add")}
          </Button>
        )}
      </div>

      {enabledCaps.length === 0 ? (
        <EmptyState
          icon={Plus}
          title={t("agents.detail.config.capabilities.empty")}
          action={
            isAdmin && installable.length > 0 ? (
              <Button size="sm" onClick={() => setAddOpen(true)}>
                {t("agents.detail.config.capabilities.add")}
              </Button>
            ) : undefined
          }
        />
      ) : (
        <div className="space-y-2">
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
                mode="enabled"
                onToast={onToast}
              />
            )
          )}
        </div>
      )}

      <AddCapabilityDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        agent={agent}
        workspaceID={workspaceID}
        installable={installable}
        credentials={credentials}
        onToast={onToast}
      />
    </section>
  )
}

function AddCapabilityDialog({
  open,
  onOpenChange,
  agent,
  workspaceID,
  installable,
  credentials,
  onToast,
}: {
  open: boolean
  onOpenChange: (next: boolean) => void
  agent: Agent
  workspaceID: string | null
  installable: Capability[]
  credentials: UserCredential[]
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
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("agents.detail.config.capabilities.add")}</DialogTitle>
        </DialogHeader>
        <div className="space-y-3">
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-faint" />
            <Input
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("agents.detail.config.capabilities.add")}
              className="pl-8"
              autoFocus
            />
          </div>
          <div className="max-h-80 space-y-2 overflow-y-auto">
            {filtered.length === 0 ? (
              <p className="rounded-md border border-dashed border-line px-3 py-6 text-center text-sm text-fg-subtle">
                {t("agents.detail.capabilities.emptyAvailable")}
              </p>
            ) : (
              filtered.map((capability) => (
                <CapabilityCard
                  key={capability.id}
                  item={{ capability }}
                  agent={agent}
                  workspaceID={workspaceID}
                  credentials={credentials}
                  mode="available"
                  onToast={(msg) => {
                    onToast(msg)
                    onOpenChange(false)
                  }}
                />
              ))
            )}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" size="sm" onClick={() => onOpenChange(false)}>
            {t("agents.detail.capabilities.actions.cancel")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
