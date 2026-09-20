import { ApiError } from "../../../lib/api-client"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { CredentialBindingSelect } from "../../../components/admin/CredentialBindingSelect"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { Input } from "../../../components/ui/input"
import { useCreateMyCredential, useMyCredentials } from "../../../lib/api-credentials"
import { useCreateSecret, useSecrets } from "../../../lib/api-secrets"
import { credentialBinding, hasCredentialKind, sharedSecretsForKind } from "../../../lib/credential-bindings"
import { CredentialDialog } from "../credentials/CredentialDialogs"
import type { UserCredentialCreateRequest } from "../../../lib/api-types"

export function AgentCredentialField({ workspaceID, kind, configuration, defaults, onChange, publicAgent, catalogID = "" }: {
  workspaceID: string | null; kind: string; configuration: Record<string, unknown>; defaults?: Record<string, unknown>
  onChange: (configuration: Record<string, unknown>) => void; publicAgent: boolean; catalogID?: string
}) {
  const { t } = useTranslation("admin")
  const shared = useSecrets(workspaceID)
  const personal = useMyCredentials()
  const createPersonal = useCreateMyCredential()
  const createShared = useCreateSecret(workspaceID)
  const [adding, setAdding] = useState<"personal" | "shared" | null>(null)
  const [name, setName] = useState("")
  const [token, setToken] = useState("")
  const binding = credentialBinding(configuration, kind) ?? credentialBinding(defaults, kind)
  const selected = binding?.source === "shared" ? binding.secretID : ""
  const secrets = sharedSecretsForKind(shared.data?.secrets ?? [], kind, catalogID)
  const error = shared.error ?? personal.error
  const loading = shared.isLoading || personal.isLoading
  const ready = selected ? secrets.some(secret => secret.id === selected) : !publicAgent && hasCredentialKind(personal.data?.credentials ?? [], kind)
  const choose = (secretID: string) => onChange({ ...configuration, credential_bindings: { ...(configuration.credential_bindings as Record<string, unknown> ?? {}), [kind]: secretID ? { source: "shared", secret_id: secretID } : { source: "personal" } } })
  return <div className="space-y-2">
    <p className="text-sm font-medium">{kind}</p>
    <CredentialBindingSelect label={kind} value={selected} secrets={secrets} allowPersonal={!publicAgent} personalLabel={t("agentResources.personal")} sharedLabel={t("agentResources.shared")} unavailableLabel={t("agentResources.unavailable")} onChange={choose} />
    <p role={error || !ready ? "status" : undefined} className="text-xs text-fg-muted">{loading ? t("agentResources.loading") : error ? t("agentResources.loadFailed") : ready ? t(selected ? "agentResources.sharedReady" : "agentResources.personalReady") : t("agentResources.missingCredential")}</p>
    {Boolean(error) && <Button type="button" variant="ghost" onClick={() => { void shared.refetch(); void personal.refetch() }}>{t("agentResources.retry")}</Button>}
    <div className="flex flex-wrap gap-2">
      {!publicAgent && !hasCredentialKind(personal.data?.credentials ?? [], kind) && <Button type="button" variant="outline" onClick={() => setAdding("personal")}>{t("agentResources.addPersonal")}</Button>}
      {kind !== "mcp_oauth" && <Button type="button" variant="outline" onClick={() => setAdding("shared")}>{t("agentResources.addShared")}</Button>}
    </div>
    {adding === "personal" && <CredentialDialog mode="create" initialKind={kind} pending={createPersonal.isPending} error={createPersonal.error instanceof ApiError ? createPersonal.error : undefined} onClose={() => setAdding(null)} onSubmit={async body => { try { await createPersonal.mutateAsync(body as UserCredentialCreateRequest); choose(""); setAdding(null) } catch { /* Mutation state displays the error. */ } }} />}
    <Dialog open={adding === "shared"} onOpenChange={open => { if (!open) { setAdding(null); setToken("") } }}>
      <DialogContent>
        <DialogHeader><DialogTitle>{t("agentResources.addShared")} · {kind}</DialogTitle></DialogHeader>
        <Input aria-label={t("agentResources.credentialName")} placeholder={t("agentResources.credentialName")} value={name} onChange={event => setName(event.target.value)} />
        <Input type="password" autoComplete="new-password" aria-label={t("agentResources.credentialValue")} placeholder={t("agentResources.credentialValue")} value={token} onChange={event => setToken(event.target.value)} />
        {createShared.error && <p role="alert">{createShared.error.message}</p>}
        <Button type="button" disabled={!name.trim() || !token.trim() || createShared.isPending} onClick={async () => { try { const secret = await createShared.mutateAsync({ body: { name: name.trim(), kind: "capability_inline", provider: kind, auth_type: "api_key", credential_kind_code: kind, payload: { value: token } } }); choose(secret.id); setToken(""); setAdding(null) } catch { /* Mutation state displays the error. */ } }}>{t("agents.core.save")}</Button>
      </DialogContent>
    </Dialog>
  </div>
}
