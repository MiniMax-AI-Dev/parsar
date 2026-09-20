import { useTranslation } from "react-i18next"
import { Select, SelectOption } from "../../../components/ui/select"
import { AgentCredentialField } from "./AgentCredentialField"

export interface AgentModelCredential { kind: string; source: "personal" | "shared"; secret_id?: string }

export function AgentModelCredentialField({ workspaceID, value, onChange, publicAgent }: {
  workspaceID: string | null; value?: AgentModelCredential; onChange: (value: AgentModelCredential | undefined) => void; publicAgent: boolean
}) {
  const { t } = useTranslation("admin")
  const kinds = ["openai_api_key", "anthropic_api_key"]
  return <section className="space-y-2">
    <h3 className="text-sm font-medium">{t("agentResources.modelCredential")}</h3>
    <Select aria-label={t("agentResources.modelCredential")} value={value?.kind ?? ""} onValueChange={kind => onChange(kind ? { kind, source: "personal" } : undefined)}>
      <SelectOption value="">{t("agentResources.providerCredential")}</SelectOption>
      {value && !kinds.includes(value.kind) && <SelectOption value={value.kind}>{value.kind}</SelectOption>}
      {kinds.map(kind => <SelectOption key={kind} value={kind}>{kind}</SelectOption>)}
    </Select>
    {value && <AgentCredentialField workspaceID={workspaceID} kind={value.kind} configuration={{ credential_bindings: { [value.kind]: value } }} publicAgent={publicAgent} onChange={config => onChange({ ...(config.credential_bindings as Record<string, AgentModelCredential>)[value.kind], kind: value.kind })} />}
  </section>
}
