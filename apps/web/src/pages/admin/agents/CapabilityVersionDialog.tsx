import { useMemo, useState, type ReactNode } from "react"
import { Check, Loader2, Replace } from "lucide-react"
import { useTranslation } from "react-i18next"
import { ActionIconButton } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import { Field } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
import { Skeleton } from "../../../components/ui/skeleton"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { CredentialBindingSelect } from "../../../components/admin/CredentialBindingSelect"
import type { ShowToast } from "../../../components/ui/toast"
import { useEnableAgentCapabilityMutation } from "../../../lib/api-capabilities"
import { agentCapabilityVersion, capabilitySupportsLatest } from "../../../lib/agent-capability-version"
import { catalogIDFromVersion, requiredCredentialKinds, useCapabilityVersions } from "../../../lib/capability-config"
import { hasCredentialKind, sharedSecretsForKind } from "../../../lib/credential-bindings"
import type { Agent, AgentCapability, Capability, CapabilityVersion, Secret, UserCredential } from "../../../lib/api-types"
import { credentialKindLabel } from "../capability-ui"
import { InlineError } from "./DetailSection"

/* Native <select> inside the shared CredentialBindingSelect, dressed as ui/Select. */
const SELECT_CLASS = "app-shadow-control h-7 w-full rounded-md border border-line-strong bg-surface px-2 text-sm text-fg focus-visible:border-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent"

function VersionSelect({ versions, value, onChange, knowledge }: { versions: CapabilityVersion[]; value: string; onChange: (value: string) => void; knowledge: boolean }) {
  const { t } = useTranslation("admin")
  return (
    <Select aria-label={t("agents.detail.capabilities.enableDialog.version")} value={value} onValueChange={(nextValue) => onChange(nextValue)}>
      {knowledge && <SelectOption value="latest">{t("capabilities.knowledge.followLatest")}</SelectOption>}
      {versions.map((version, index) => (
        <SelectOption key={version.id} value={version.id}>v{version.version}{index === 0 ? ` · ${t("agents.detail.capabilities.switchDialog.latest")}` : ""}</SelectOption>
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
              label={credentialKindLabel(rc.kind, i18n.language, rc.kind)}
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

export function CapabilityVersionDialog({
  mode,
  agent,
  capability,
  credentials = [],
  sharedSecrets = [],
  workspaceID,
  binding,
  trigger,
  disabled = false,
  onToast,
  open: controlledOpen,
  onOpenChange,
  onInstalled,
  onBack,
  credentialHelp,
}: {
  mode: "enable" | "switch"
  agent: Agent
  capability: Capability
  credentials?: UserCredential[]
  sharedSecrets?: Secret[]
  workspaceID: string | null
  binding?: AgentCapability
  /** Draws the control that opens this dialog; defaults to a row action. */
  trigger?: (open: () => void) => ReactNode
  disabled?: boolean
  onToast: ShowToast
  open?: boolean
  onOpenChange?: (open: boolean) => void
  onInstalled?: () => void
  onBack?: () => void
  credentialHelp?: (pending: boolean) => ReactNode
}) {
  const { t } = useTranslation(["admin", "common"])
  const [localOpen, setOpen] = useState(false)
  const open = controlledOpen ?? localOpen
  const mut = useEnableAgentCapabilityMutation(workspaceID, agent.id)
  const [selection, setSelected] = useState("")
  const changeOpen = (next: boolean) => {
    if (mut.isPending) return
    setOpen(next)
    onOpenChange?.(next)
    setSelected("")
  }
  const [credentialBindingChoices, setCredentialBindingChoices] = useState<Record<string, string>>({})
  const { latest, versions, versionsQ } = useCapabilityVersions(workspaceID, capability, open)
  const currentVersion = agentCapabilityVersion(binding, capability, versions)
  const knowledge = capability.type === "knowledge"
  const allowLatest = knowledge || (mode === "switch" && capabilitySupportsLatest(capability.type) && !capability.deprecated_at
    && (!capability.from_marketplace || capability.visibility === "public"))
  const selected = selection || (allowLatest && ((knowledge && mode === "enable") || binding?.pinning_mode === "latest") ? "latest" : currentVersion?.id || "")
  const followsLatest = allowLatest && selected === "latest"
  const switchVersions = capability.from_marketplace && currentVersion && !versions.some((version) => version.id === currentVersion.id)
    ? [...versions, currentVersion] : versions
  const selectedVersion = followsLatest ? latest : selected
    ? switchVersions.find((version) => version.id === selected) ?? (mode === "enable" ? latest : versions[0])
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
    && !versionsQ.isLoading
    && !versionsQ.isError
    && !mut.isPending
    && !disabled
    && (mode === "enable" ? !missingRequiredCredential : (binding?.pinning_mode === "latest") !== followsLatest || selectedVersion.id !== currentVersion?.id)

  const submit = () => {
    if (!selectedVersion || !canSubmit) return
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
      pinningMode: followsLatest ? "latest" : undefined,
      configuration: mode === "enable"
        ? { credential_bindings: capabilityBindings }
        : binding?.configuration,
    }, {
      onSuccess: () => {
        if (onInstalled) onInstalled()
        else {
          setOpen(false)
          onOpenChange?.(false)
          setSelected("")
        }
        onToast(mode === "enable"
          ? t("agents.detail.capabilities.toast.enabled", { cap: capability.name, agent: agent.name, version: selectedVersion.version })
          : t("agents.detail.capabilities.toast.switched", { cap: capability.name, version: selectedVersion.version }))
      },
    })
  }
  const isSwitch = mode === "switch"
  const confirmLabel = isSwitch
    ? followsLatest ? t("agents.detail.capabilities.switchDialog.followLatest") : selectedVersion
      ? t("agents.detail.capabilities.actions.switchConfirm", { version: selectedVersion.version })
      : t("agents.detail.capabilities.actions.switchVersion")
    : t("agents.detail.capabilities.actions.enableConfirm")

  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      {trigger ? (
        trigger(() => changeOpen(true))
      ) : isSwitch ? (
        <ActionIconButton
          icon={Replace}
          label={t("agents.detail.capabilities.actions.switchVersion")}
          disabled={disabled}
          onClick={() => changeOpen(true)}
        />
      ) : (
        <Button variant="outline" size="sm" disabled={disabled} onClick={() => changeOpen(true)}>
          {t("agents.detail.capabilities.actions.enable")}
        </Button>
      )}
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(isSwitch ? "agents.detail.capabilities.switchDialog.title" : "agents.detail.capabilities.enableDialog.title", { agent: agent.name, cap: capability.name })}</DialogTitle>
          <DialogDescription>{t(knowledge ? "capabilities.knowledge.versionHint" : isSwitch && allowLatest ? "agents.detail.capabilities.switchDialog.followDescription" : isSwitch ? "agents.detail.capabilities.switchDialog.description" : "agents.detail.capabilities.enableDialog.description")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3">
          {isSwitch ? (
            versionsQ.isLoading ? <Skeleton className="h-28 w-full" /> : (
              <ul className="m-0 list-none p-0">
                {allowLatest && <li><label className="flex h-7 cursor-pointer items-center gap-2 text-sm text-fg">
                  <input type="radio" name="capability-version" className="h-3.5 w-3.5 accent-accent" checked={selected === "latest"} onChange={() => setSelected("latest")} />
                  {t("agents.detail.capabilities.switchDialog.followLatest")}
                </label></li>}
                {switchVersions.map((version, index) => (
                  <li key={version.id}>
                    <label className="flex h-7 cursor-pointer items-center gap-2 text-sm text-fg">
                      <input type="radio" name="capability-version" className="h-3.5 w-3.5 accent-accent" checked={selected === version.id} onChange={() => setSelected(version.id)} />
                      <span className="font-mono text-xs">v{version.version}</span>
                      {index === 0 && <span className="text-xs text-fg-muted">· {t("agents.detail.capabilities.switchDialog.latest")}</span>}
                      {version.id === currentVersion?.id && <span className="text-xs text-fg-muted">· {t("agents.detail.capabilities.switchDialog.current")}</span>}
                    </label>
                  </li>
                ))}
              </ul>
            )
          ) : (
            <>
              <Field label={t("agents.detail.capabilities.enableDialog.version")}>
                {versionsQ.isLoading ? <Skeleton className="h-7 w-full" /> : <VersionSelect versions={versions} value={selected === "latest" ? selected : selectedVersion?.id ?? ""} onChange={setSelected} knowledge={knowledge} />}
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
          {credentialHelp?.(mut.isPending)}
          {versionsQ.error instanceof Error && <InlineError>{versionsQ.error.message}</InlineError>}
          {mut.error instanceof Error && <InlineError>{mut.error.message}</InlineError>}
        </div>
        <DialogFooter>
          {onBack && <Button variant="outline" disabled={mut.isPending} onClick={onBack}>{t("agents.pendingCapability.chooseAnother")}</Button>}
          <Button variant="outline" onClick={() => changeOpen(false)} disabled={mut.isPending}>{t("agents.detail.capabilities.actions.cancel")}</Button>
          <Button disabled={!canSubmit} onClick={submit}>{mut.isPending && <Loader2 className="animate-spin" strokeWidth={1.5} aria-hidden="true" />}{confirmLabel}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
