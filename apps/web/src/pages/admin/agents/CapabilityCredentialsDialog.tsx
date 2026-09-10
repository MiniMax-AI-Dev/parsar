import { useState } from "react"
import { KeyRound } from "lucide-react"
import { useTranslation } from "react-i18next"
import { CredentialBindingSelect } from "../../../components/admin/CredentialBindingSelect"
import { ErrorState } from "../../../components/ui/error-state"
import { ActionIconButton } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { Field } from "../../../components/ui/label"
import { Skeleton } from "../../../components/ui/skeleton"
import type { ShowToast } from "../../../components/ui/toast"
import { useCapabilityVersionsQuery, useEnableAgentCapabilityMutation } from "../../../lib/api-capabilities"
import { useSecrets } from "../../../lib/api-secrets"
import type { Agent, AgentCapability, Capability } from "../../../lib/api-types"
import { catalogIDFromVersion, requiredCredentialKinds } from "../../../lib/capability-config"
import { credentialBinding, sharedSecretsForKind } from "../../../lib/credential-bindings"
import { credentialKindLabel } from "../capability-ui"

interface Props {
  agent: Agent
  binding: AgentCapability
  capability: Capability
  workspaceID: string | null
  onToast: ShowToast
}

export function CapabilityCredentialsDialog(props: Props) {
  const { t } = useTranslation("admin")
  const [open, setOpen] = useState(false)
  const mut = useEnableAgentCapabilityMutation(props.workspaceID, props.agent.id)
  return (
    <Dialog open={open} onOpenChange={(next) => { if (!mut.isPending) { setOpen(next); mut.reset() } }}>
      <ActionIconButton icon={KeyRound} label={t("agents.detail.capabilities.credentials.edit")} onClick={() => setOpen(true)} />
      {open && <CredentialForm {...props} mut={mut} close={() => { setOpen(false); mut.reset() }} />}
    </Dialog>
  )
}

function CredentialForm({ agent, binding, capability, workspaceID, onToast, close, mut }: Props & { close: () => void; mut: ReturnType<typeof useEnableAgentCapabilityMutation> }) {
  const { t, i18n } = useTranslation(["admin", "common"])
  const secretsQ = useSecrets(workspaceID)
  const foreign = capability.from_marketplace === true || (!!capability.workspace_id && capability.workspace_id !== workspaceID)
  const versionsQ = useCapabilityVersionsQuery(workspaceID, foreign ? null : binding.capability_id)
  const localVersion = versionsQ.data?.versions.find((item) => item.id === binding.capability_version_id)
  const version = foreign ? {
    id: binding.capability_version_id,
    required_credentials: binding.required_credentials ?? capability.required_credentials,
  } : localVersion
  const [choices, setChoices] = useState<Record<string, string>>({})
  const secrets = secretsQ.data?.secrets ?? []
  const fields = requiredCredentialKinds({ ...capability, required_credentials: version?.required_credentials }).map((field) => {
    const original = credentialBinding(binding.configuration, field.kind) ?? credentialBinding(agent.config, field.kind)
    const value = choices[field.kind] ?? original?.secretID ?? ""
    const available = sharedSecretsForKind(secrets, field.kind, catalogIDFromVersion(foreign ? undefined : localVersion))
    const valid = value ? available.some((secret) => secret.id === value)
      : agent.visibility !== "public"
    return { ...field, value, available, valid }
  })
  const loading = secretsQ.isLoading || (!foreign && versionsQ.isLoading)
  const failed = secretsQ.isError || (!foreign && (versionsQ.isError || (versionsQ.isSuccess && !version)))
  const changes = Object.entries(choices).filter(([kind, value]) => value !== ((credentialBinding(binding.configuration, kind) ?? credentialBinding(agent.config, kind))?.secretID ?? ""))
  const canSubmit = !loading && !failed && !mut.isPending && changes.length > 0 && fields.every((field) => field.valid)
  const retry = () => {
    void secretsQ.refetch()
    if (!foreign) void versionsQ.refetch()
  }
  const save = () => {
    if (!canSubmit) return
    const existing = binding.configuration.credential_bindings
    const credentialBindings = existing && typeof existing === "object" && !Array.isArray(existing) ? { ...existing } : {}
    for (const [kind, value] of changes) {
      Object.assign(credentialBindings, { [kind]: value ? { source: "shared", secret_id: value } : { source: "personal" } })
    }
    mut.mutate({
      capabilityVersionID: binding.capability_version_id,
      pinningMode: binding.pinning_mode ?? "pinned",
      configuration: { ...binding.configuration, credential_bindings: credentialBindings },
    }, {
      onSuccess: () => {
        onToast(t("agents.detail.capabilities.credentials.saved", { name: capability.name }))
        close()
      },
    })
  }
  return (
    <DialogContent>
      <DialogHeader>
        <DialogTitle>{t("agents.detail.capabilities.credentials.title", { name: capability.name })}</DialogTitle>
        <DialogDescription>{t("agents.detail.capabilities.credentials.description")}</DialogDescription>
      </DialogHeader>
      <div className="flex flex-col gap-4">
        {failed ? <ErrorState appearance="panel" title={t("agents.detail.capabilities.credentials.loadError")} onRetry={retry} />
          : loading ? <Skeleton className="h-8 w-full" />
            : fields.map((field) => (
              <Field key={field.kind} label={credentialKindLabel(field.kind, i18n.language, field.kind)}>
                <CredentialBindingSelect
                  label={credentialKindLabel(field.kind, i18n.language, field.kind)}
                  value={field.value}
                  secrets={field.available}
                  allowPersonal={agent.visibility !== "public"}
                  personalLabel={t("credentialCheck.sourcePersonal")}
                  sharedLabel={t("credentialCheck.sourceShared")}
                  personalPlaceholder={t("credentialCheck.sharedPlaceholder")}
                  unavailableLabel={t("agents.detail.capabilities.credentials.unavailable")}
                  onChange={(value) => setChoices((previous) => ({ ...previous, [field.kind]: value }))}
                />
                {!field.valid && <p className="mt-1 text-xs text-status-failed">{t("agents.detail.capabilities.credentials.required")}</p>}
              </Field>
            ))}
        {mut.error != null && <ErrorState appearance="panel" title={t("agents.detail.capabilities.credentials.saveError")} detail={mut.error instanceof Error ? mut.error.message : undefined} />}
      </div>
      <DialogFooter>
        <Button variant="outline" disabled={mut.isPending} onClick={close}>{t("common:actions.cancel")}</Button>
        <Button disabled={!canSubmit} onClick={save}>{t("common:actions.save")}</Button>
      </DialogFooter>
    </DialogContent>
  )
}
