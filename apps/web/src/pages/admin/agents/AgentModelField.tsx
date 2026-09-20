import { CatalogDialog, type CatalogEdit } from "../models/CatalogDialog"
import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"
import { Button } from "../../../components/ui/button"
import { useModelCatalog, supportsCatalogModel } from "../../../lib/model-catalog"
import type { CoreHarness } from "../../../lib/core-api"
export function AgentModelField({ workspaceID, value, legacyModel, harness, onChange, disabled }: {
  workspaceID: string | null; value: string; legacyModel: string; harness: CoreHarness | ""; onChange: (id: string) => void; disabled: boolean
}) {
  const { t } = useTranslation("admin")
  const id = useId()
  const catalog = useModelCatalog(workspaceID)
  const [edit, setEdit] = useState<CatalogEdit | null>(null)
  const models = catalog.data?.models ?? []
  const missing = value && !models.some(model => model.id === value)
  return <div><Label htmlFor={id}>{t("catalog.model")}</Label>
    <Select id={id} value={value} onValueChange={onChange} disabled={disabled || catalog.isLoading || !workspaceID} aria-required="true">
      <SelectOption value="" disabled>{legacyModel ? t("catalog.legacy", { model: legacyModel }) : t("catalog.selectModel")}</SelectOption>
      {missing && <SelectOption value={value} disabled>{t("catalog.unavailable")}</SelectOption>}
      {models.map(model => <SelectOption key={model.id} value={model.id} disabled={!supportsCatalogModel(model, harness)}>{model.provider_name} / {model.name} · {model.model_key}</SelectOption>)}
    </Select>
    <div className="mt-2 flex flex-wrap gap-2">
      <Button type="button" variant="outline" disabled={disabled} onClick={() => setEdit({ kind: "provider" })}>{t("catalog.addProvider")}</Button>
      <Select aria-label={t("catalog.addModel")} value="" onValueChange={id => { const provider = catalog.data?.providers.find(row => row.id === id); if (provider) setEdit({ kind: "model", provider }) }} disabled={disabled}>
        <SelectOption value="">{t("catalog.addModel")}</SelectOption>
        {catalog.data?.providers.map(provider => <SelectOption key={provider.id} value={provider.id}>{provider.name}</SelectOption>)}
      </Select>
    </div>
    {workspaceID && edit && <CatalogDialog workspace={workspaceID} edit={edit} onClose={() => setEdit(null)} onSaved={row => { if ("model_key" in row) onChange(row.id) }} />}
    <p className="mt-1 text-xs text-fg-muted">{t("catalog.agentHint")}</p>
    {catalog.error && <div role="alert"><p>{catalog.error.message}</p><Button type="button" variant="ghost" onClick={() => void catalog.refetch()}>{t("core.retry")}</Button></div>}
  </div>
}
