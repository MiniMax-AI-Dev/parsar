import { useTranslation } from "react-i18next"
import { CredentialBindingSelect } from "../../../components/admin/CredentialBindingSelect"
import { Field } from "../../../components/ui/label"
import { credentialKindLabel } from "../../../lib/credential-kind-ui"
import type { useAgentCloneCredentials } from "./useAgentCloneCredentials"
import { AgentCloneCredentialRefresh } from "./AgentCloneCredentialRefresh"

export function AgentCloneCredentials({ credentials, workspaceID, failed, fetching, onRefresh }: {
  credentials: ReturnType<typeof useAgentCloneCredentials>
  workspaceID: string | null
  failed: boolean
  fetching: boolean
  onRefresh: () => void
}) {
  const { t, i18n } = useTranslation("admin")
  if (!credentials.needsCredentials) return null
  return <section className="flex flex-col gap-3">
    <h3 className="text-sm font-medium text-fg">{t("agents.form.sections.credentials")}</h3>
    {credentials.rows.filter((row) => row.fields.length > 0).map((row) => (
      <div key={row.capabilityID} className="space-y-3 rounded-md border border-line p-3">
        <h4 className="break-words text-sm font-medium">{row.name}</h4>
        {row.fields.map((field) => (
          <Field key={field.kind} label={credentialKindLabel(field.kind, i18n.language, field.kind)}>
            <CredentialBindingSelect
              label={`${row.name} · ${credentialKindLabel(field.kind, i18n.language, field.kind)}`}
              value={field.value} secrets={field.available} allowPersonal={false}
              personalLabel={t("credentialCheck.sharedPlaceholder")}
              sharedLabel={t("credentialCheck.sourceShared")}
              onChange={(value) => credentials.choose(row.capabilityID, field.kind, value)}
            />
          </Field>
        ))}
      </div>
    ))}
    <a href={`/?ws=${encodeURIComponent(workspaceID ?? "")}&admin=credentials`} target="_blank" rel="noopener noreferrer"
      className="text-sm text-fg underline underline-offset-4">{t("agents.form.clone.manageCredentials")}</a>
    <AgentCloneCredentialRefresh failed={failed} fetching={fetching} onRefresh={onRefresh} />
  </section>
}
