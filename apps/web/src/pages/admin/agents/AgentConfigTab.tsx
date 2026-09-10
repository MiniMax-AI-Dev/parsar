import { useMemo, useState, type ReactNode } from "react"
import { Loader2, Power, Search, Trash2 } from "lucide-react"
import { useTranslation } from "react-i18next"

import { SandboxPanel } from "../../../components/admin/SandboxPanel"
import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import { EmptyState } from "../../../components/ui/empty-state"
import { ErrorState } from "../../../components/ui/error-state"
import { Input } from "../../../components/ui/input"
import { Skeleton } from "../../../components/ui/skeleton"
import { StatusIcon, type StatusKind } from "../../../components/ui/status-icon"
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
  useDeleteAgentCapabilityMutation,
  useToggleBuiltinCapabilityMutation,
} from "../../../lib/api-capabilities"
import { useMyCredentials } from "../../../lib/api-credentials"
import { useSecrets } from "../../../lib/api-secrets"
import { agentCapabilityFollowsLatest, agentCapabilityVersion } from "../../../lib/agent-capability-version"
import { agentExecutionPlacement } from "../../../lib/agent-runtime"
import { agentEngineLabel, agentEngineOf, agentEngineSupportsCapability, agentEnginesSupportingCapability } from "../../../lib/agent-view-model"
import { credentialBinding, hasCredentialKind, sharedSecretsForKind } from "../../../lib/credential-bindings"
import type { Agent, AgentCapability, AgentDetail, Capability, Secret, UserCredential } from "../../../lib/api-types"
import { CapabilityTypeBadge } from "../CapabilitiesPage"
import { UpgradeCapabilityDialog } from "../capabilities/UpgradeCapabilityDialog"
import { credentialKindLabel } from "../capability-ui"
import { AgentConfigSummary } from "./AgentConfigSummary"
import { DetailSection, InlineError } from "./DetailSection"
import type { ShowToast } from "../../../components/ui/toast"

import { CapabilityVersionDialog } from "./CapabilityVersionDialog"
import { CapabilityCredentialsDialog } from "./CapabilityCredentialsDialog"
import { catalogIDFromVersion, requiredCredentialKinds, useCapabilityVersions } from "../../../lib/capability-config"

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

/** One hairline-separated capability row: name and flags left, controls right. */
/**
 * One capability, one shape — the whole point of this block.
 *
 * Every row reads the same left to right: a 14px glyph carrying the row's
 * worst state, the name, its kind, the pinned version right-aligned in mono,
 * and the verbs revealed on hover. Under it, the description, and then only
 * the problems worth acting on, one line each with one link.
 *
 * It used to be three different rows. A built-in rendered two lines with a
 * checkbox; an installed capability rendered three with two text buttons; one
 * from the market rendered five, and said its version three times (a mono
 * `v—`, a "current v2.0.0" in prose, a "published v2.0.0" in a banner), its
 * missing credential twice (a red badge and a sentence), and its origin twice
 * (a badge and a line). Nothing lined up, so the column could not be scanned.
 */
function CapabilityLine({
  status,
  statusLabel,
  name,
  kind,
  description,
  version,
  notes,
  actions,
  actionsAlways,
}: {
  status: StatusKind
  statusLabel: string
  name: ReactNode
  kind?: ReactNode
  description?: ReactNode
  version?: string
  /** At most one line each, and each with at most one thing to press. */
  notes?: { key: string; text: ReactNode; action?: ReactNode }[]
  actions?: ReactNode
  /** For a row whose action is its whole point — a toggle, or an "add" list. */
  actionsAlways?: boolean
}) {
  return (
    <li className="group border-b border-line py-2.5 last:border-b-0">
      {/* The identifying line: glyph, name, kind, then a fixed version track so
          versions line up down the list whatever the row's verbs are — the
          cluster is one icon wide on one row and a text button on another. */}
      <div className="flex items-center gap-2">
        <StatusIcon status={status} title={statusLabel} className="shrink-0" />
        <span className="min-w-0 truncate text-sm font-medium text-fg">{name}</span>
        {kind && <span className="min-w-0 shrink truncate text-xs text-fg-muted">{kind}</span>}
        <span className="ml-auto w-14 shrink-0 text-right font-mono text-xs tabular-nums text-fg-muted">
          {version}
        </span>
        {actions && <RowActions always={actionsAlways}>{actions}</RowActions>}
      </div>
      {/* Everything below runs the full width under the glyph: the version
          track belongs to the line above, not to the sentences. */}
      <div className="pl-5">
        {description && (
          <p className="mt-0.5 truncate text-xs text-fg-muted" title={typeof description === "string" ? description : undefined}>
            {description}
          </p>
        )}
        {/* A div, not a p: a note's action may itself render a block (the
            upgrade button's failure notice), which a paragraph cannot hold. */}
        {notes?.map((note) => (
          <div key={note.key} className="mt-1 flex min-w-0 items-center gap-2 text-xs text-fg">
            <span className="min-w-0 truncate">{note.text}</span>
            {note.action}
          </div>
        ))}
      </div>
    </li>
  )
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
  onToast: ShowToast
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
      {
        onError: (e) =>
          onToast(t("agents.detail.capabilities.builtin.toggleError"), {
            tone: "error",
            detail: e instanceof Error ? e.message : String(e),
          }),
      },
    )
  }
  return (
    <CapabilityLine
      status={enabled ? "completed" : "cancelled"}
      statusLabel={enabled ? t("agents.detail.capabilities.builtin.on") : t("agents.detail.capabilities.builtin.off")}
      name={capability?.name ?? key}
      kind={
        <>
          {capability?.type && <CapabilityTypeBadge type={capability.type} className="inline" />}
          {capability?.type ? " · " : ""}
          {t("agents.detail.capabilities.builtin.badge")}
        </>
      }
      description={capability?.description}
      actionsAlways
      actions={
        <ActionIconButton
          icon={Power}
          label={enabled ? t("agents.detail.capabilities.builtin.disable") : t("agents.detail.capabilities.builtin.enable")}
          busy={mut.isPending}
          disabled={!isAdmin || mut.isPending}
          aria-pressed={enabled}
          onClick={() => onToggle(!enabled)}
        />
      }
    />
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

/** `v1.4.0`, or nothing at all — never a placeholder dash. */
function versionOf(version?: string | null): string | undefined {
  return version ? `v${version}` : undefined
}

function CapabilityCard({
  item,
  agent,
  workspaceID,
  credentials,
  sharedSecrets,
  mode,
  onToast,
  canEditCredentials = false,
}: {
  item: CapabilityCardItem
  agent: Agent
  workspaceID: string | null
  credentials: UserCredential[]
  sharedSecrets: Secret[]
  mode: "enabled" | "available"
  onToast: ShowToast
  canEditCredentials?: boolean
}) {
  const { t, i18n } = useTranslation("admin")
  const capability = item.capability
  const binding = item.binding
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, mode === "enabled")
  const boundVersion = agentCapabilityVersion(binding, capability, versions)
  const catalogID = catalogIDFromVersion(boundVersion ?? latest)
  const versionDeleted = !!binding && !versionsQ.isLoading && !boundVersion && !capability?.latest_version_id
  const fromMarketplace = !!capability?.from_marketplace || (!!capability?.source_workspace_id && capability.source_workspace_id !== workspaceID)
  const deprecated = !!capability?.deprecated_at

  if (!capability && binding) {
    return (
      <CapabilityLine
        status="failed"
        statusLabel={t("agents.detail.capabilities.deletedCapability.title")}
        name={t("agents.detail.capabilities.deletedCapability.title")}
        description={t("agents.detail.capabilities.deletedCapability.description")}
        actions={
          <RemoveCapabilityDialog
            agent={agent}
            binding={binding}
            capabilityName={t("agents.detail.capabilities.deletedCapability.fallbackName")}
            workspaceID={workspaceID}
            onToast={onToast}
          />
        }
      />
    )
  }
  if (!capability) return null

  const agentEngine = agentEngineOf(agent)
  const incompatible = !agentEngineSupportsCapability(agentEngine, capability.type)
  const followsLatest = agentCapabilityFollowsLatest(binding, capability)
  const upgradable = mode === "enabled" && fromMarketplace && !!binding && !followsLatest && !!latest && latest.id !== binding.capability_version_id

  // One line per credential the capability needs and the reader has not set.
  // A credential that *is* set says nothing: the row's glyph already reports
  // that everything here is in order, and an "add credential" link beside a
  // credential you have added is an affordance with nowhere to go.
  const missingKinds = requiredCredentialKinds(capability).filter(
    (rc) => !hasUsableCredential(agent, binding, credentials, sharedSecrets, rc.kind, catalogID),
  )

  const notes: { key: string; text: React.ReactNode; action?: React.ReactNode }[] = binding ? [{
    key: "version-mode",
    text: t(`agents.detail.capabilities.bindings.${followsLatest ? "followingLatest" : binding.pinning_mode === "latest" ? "storedVersion" : "pinnedVersion"}`),
  }] : []
  for (const rc of missingKinds) {
    notes.push({
      key: `cred:${rc.kind}`,
      text: t("agents.detail.capabilities.credential.missingShort", {
        kind: credentialKindLabel(rc.kind, i18n.language, rc.kind),
      }),
      action: <CredentialLink kind={rc.kind} className="shrink-0 text-xs text-fg underline underline-offset-4" />,
    })
  }
  if (incompatible) {
    notes.push({
      key: "incompatible",
      text: t("agents.detail.capabilities.compatibility.unsupported", {
        engine: t(agentEngineLabel(agentEngine)),
        type: t(`agents.detail.capabilities.compatibility.types.${capability.type}`),
        engines: agentEnginesSupportingCapability(capability.type).map((engine) => t(agentEngineLabel(engine))).join(", "),
      }),
    })
  }
  if (mode === "enabled" && versionDeleted && binding) {
    notes.push({
      key: "version-deleted",
      text: t("agents.detail.capabilities.bindings.versionDeleted.warning"),
      // Only the remedy is conditional. The warning used to be gated on it
      // too, so a binding whose every version was gone showed a red glyph and
      // not one word.
      action: versions.length > 0 && (
        <CapabilityVersionDialog
          mode="switch"
          agent={agent}
          capability={capability}
          binding={binding}
          workspaceID={workspaceID}
          onToast={onToast}
          trigger={(open) => (
            <Button variant="link" size="sm" className="shrink-0 px-0" onClick={open}>
              {t("agents.detail.capabilities.bindings.versionDeleted.switchAction")}
            </Button>
          )}
        />
      ) || undefined,
    })
  }
  if (mode === "enabled" && deprecated) {
    notes.push({
      key: "deprecated",
      text: t("agents.detail.capabilities.marketplace.deprecatedBanner", {
        version: boundVersion?.version ?? "—",
      }),
    })
  }
  // Deprecated marketplace sources cannot be upgraded.
  if (upgradable && !deprecated) {
    notes.push({
      key: "upgrade",
      text: t("agents.detail.capabilities.marketplace.upgradeShort", { version: latest?.version ?? "—" }),
      action: (
        <UpgradeCapabilityDialog
          agent={agent}
          capability={capability}
          binding={binding as AgentCapability}
          latestVersion={latest}
          workspaceID={workspaceID}
          disabled={deprecated}
          onToast={onToast}
        />
      ),
    })
  }

  const blocked = versionDeleted || incompatible || missingKinds.length > 0
  // `interrupted`, not `running`: the spinning arc is reserved for work in
  // flight, and a capability with a newer version is doing nothing at all —
  // it would have spun forever inside the rail.
  const needsAttention = deprecated || upgradable
  const status: StatusKind = blocked ? "failed" : needsAttention ? "interrupted" : mode === "available" ? "queued" : "completed"
  const statusLabel = blocked
    ? t("agents.detail.capabilities.state.blocked")
    : deprecated
      ? t("agents.detail.capabilities.state.deprecated")
      : upgradable
        ? t("agents.detail.capabilities.state.attention")
        : t(`agents.detail.capabilities.state.${mode === "available" ? "available" : "ready"}`)

  return (
    <CapabilityLine
      status={status}
      statusLabel={statusLabel}
      name={capability.name}
      // The source workspace rides in the kind slot rather than a line of its
      // own — it is what the deprecation note points at, and it is why this
      // row has no "switch version" verb.
      kind={
        <>
          <CapabilityTypeBadge type={capability.type} className="inline" />
          {fromMarketplace && capability.source_workspace_name ? ` · ${capability.source_workspace_name}` : ""}
        </>
      }
      description={capability.description}
      version={versionOf(boundVersion?.version) ?? (mode === "available" ? versionOf(latest?.version) : undefined)}
      notes={notes}
      // In the add dialog the verb is the reason the list exists, so it does
      // not wait for a hover to appear.
      actionsAlways={mode === "available"}
      actions={
        mode === "available" ? (
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
            {canEditCredentials && binding.enabled && capability.required_credentials?.some((credential) => credential.required) && (
              <CapabilityCredentialsDialog agent={agent} binding={binding} capability={capability} workspaceID={workspaceID} onToast={onToast} />
            )}
            {(versions.length > 1 || (versions.length === 1 && binding.pinning_mode === "latest")) && !versionDeleted && !fromMarketplace && (
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
        ) : undefined
      }
    />
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
  onToast: ShowToast
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
      <ActionIconButton
        icon={Trash2}
        tone="danger"
        label={t("agents.detail.capabilities.actions.remove")}
        onClick={() => setOpen(true)}
      />
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
  onToast: ShowToast
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
  onToast: ShowToast
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
          detail={error instanceof Error ? error.message : undefined}
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
        <EmptyState size="compact" title={t("agents.detail.config.capabilities.empty")} />
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
                canEditCredentials={isAdmin}
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
  onToast: ShowToast
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
      <DialogContent className="max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-lg grid-cols-1 grid-rows-[auto_minmax(0,1fr)_auto]">
        <DialogHeader>
          <DialogTitle>{t("agents.detail.config.capabilities.add")}</DialogTitle>
          <DialogDescription>{t("agents.detail.config.capabilities.pickerHint")}</DialogDescription>
        </DialogHeader>
        <div className="flex min-h-0 min-w-0 flex-col gap-3">
          <div className="relative shrink-0">
            <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
            <Input
              type="search"
              value={q}
              onChange={(e) => setQ(e.target.value)}
              placeholder={t("capabilities.filters.search")}
              aria-label={t("capabilities.filters.search")}
              className="pl-7"
              autoFocus
            />
          </div>
          <div className="min-h-0 max-h-80 overflow-y-auto">
            {filtered.length === 0 ? (
              <EmptyState size="compact" title={t(q.trim() ? "capabilities.emptyFiltered.title" : "agents.detail.capabilities.emptyAvailable")} />
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
                    onToast={(msg, options) => {
                      onToast(msg, options)
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
