import { ApiError } from "../../../lib/api-client"
import { useId, useState } from "react"
import { useTranslation } from "react-i18next"
import { useMutation, useQueryClient } from "@tanstack/react-query"
import { coreTemplateKey, jsonObject, saveCoreTemplate, type CoreTemplate, type CoreTemplateInput } from "../../../lib/core-api"
import { Button } from "../../../components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../../components/ui/dialog"
import { Input } from "../../../components/ui/input"
import { Textarea } from "../../../components/ui/textarea"
import { Label } from "../../../components/ui/label"
import { Select, SelectOption } from "../../../components/ui/select"

const lines = (value: string) => value.split("\n").map(line => line.trim()).filter(Boolean)
export function EnvironmentTemplateDialog({ workspaceID, template, onClose, onSaved }: { workspaceID: string; template?: CoreTemplate; onClose: () => void; onSaved?: (template: CoreTemplate) => void }) {
  const { t } = useTranslation("admin")
  const id = useId()
  const queryClient = useQueryClient()
  const [name, setName] = useState(template?.name ?? "")
  const [access, setAccess] = useState<"enabled" | "restricted" | "disabled">(template?.network?.access ?? "enabled")
  const [domains, setDomains] = useState(template?.network?.allowed_domains?.join("\n") ?? "")
  const [packages, setPackages] = useState({ python: template?.packages?.python?.join("\n") ?? "", npm: template?.packages?.npm?.join("\n") ?? "", system: template?.packages?.system?.join("\n") ?? "" })
  const [commands, setCommands] = useState<{ command: string; cwd: string }[]>([])
  const [variables, setVariables] = useState<{ key: string; value: string }[]>([])
  const [advanced, setAdvanced] = useState("")
  const [error, setError] = useState<string | null>(null)
  const [uncertain, setUncertain] = useState(false)
  const save = useMutation({ mutationFn: (body: CoreTemplateInput) => saveCoreTemplate(workspaceID, body, template?.id) })
  return <Dialog open onOpenChange={open => { if (!open && !save.isPending) onClose() }}><DialogContent className="max-h-[90dvh] max-w-xl overflow-y-auto">
    <form className="space-y-4" onSubmit={async event => {
      event.preventDefault(); setError(null)
      try {
        const extra = jsonObject(advanced)
        if (Object.keys(extra).some(key => !["files", "plugins", "skills", "capability_directories"].includes(key))) throw new Error(t("core.templateAdvancedHint"))
        if (variables.some(value => !value.key.trim()) || new Set(variables.map(value => value.key.trim())).size !== variables.length) throw new Error(t("core.invalidVariables"))
        const body: CoreTemplateInput = { ...extra, name: name.trim() || null, network: { access, ...(access === "restricted" ? { allowed_domains: lines(domains) } : {}) }, packages: { python: lines(packages.python), npm: lines(packages.npm), system: lines(packages.system) }, ...(commands.length ? { setup_commands: commands.map(value => ({ command: value.command, ...(value.cwd ? { cwd: value.cwd } : {}) })) } : {}), ...(variables.length ? { env: Object.fromEntries(variables.map(value => [value.key.trim(), value.value])) } : {}) }
        const saved = await save.mutateAsync(body)
        onSaved?.(saved)
        await queryClient.invalidateQueries({ queryKey: coreTemplateKey(workspaceID) }); onClose()
      } catch (error) {
        const ambiguous = !template && error instanceof ApiError && (error.envelope.unreachable || error.envelope.status >= 500)
        setUncertain(!!ambiguous)
        setError(ambiguous ? t("core.uncertainCreate") : error instanceof Error ? error.message : t("core.failed"))
      }
    }}>
      <DialogHeader><DialogTitle>{t(template ? "core.editTemplate" : "core.createTemplate")}</DialogTitle><DialogDescription>{t("core.templateHint")} {t("core.templateSupport")}</DialogDescription></DialogHeader>
      <fieldset disabled={save.isPending} className="space-y-4">
        <div><Label htmlFor={`${id}-name`}>{t("core.name")}</Label><Input id={`${id}-name`} value={name} onChange={event => setName(event.target.value)} /></div>
        <div><Label htmlFor={`${id}-network`}>{t("core.network")}</Label><Select id={`${id}-network`} value={access} onValueChange={value => setAccess(value as typeof access)}>{(["enabled", "restricted", "disabled"] as const).map(value => <SelectOption key={value} value={value} disabled={value === "restricted"}>{t(`core.networkModes.${value}`)}{value === "restricted" ? ` — ${t("core.awaitingCore")}` : ""}</SelectOption>)}</Select></div>
        {access === "restricted" && <div><Label htmlFor={`${id}-domains`}>{t("core.allowedDomains")}</Label><Textarea id={`${id}-domains`} value={domains} onChange={event => setDomains(event.target.value)} required /></div>}
        <fieldset disabled className="space-y-2"><legend className="mb-2 font-medium">{t("core.packages")} — {t("core.awaitingCore")}</legend>{(["python", "npm", "system"] as const).map(manager => <div key={manager}><Label htmlFor={`${id}-${manager}`}>{t(`core.packageTypes.${manager}`)}</Label><Textarea id={`${id}-${manager}`} value={packages[manager]} onChange={event => setPackages({ ...packages, [manager]: event.target.value })} placeholder={t("core.onePerLine")} /></div>)}</fieldset>
        {template && <p className="text-xs text-fg-muted">{t("core.secretEditHint")}</p>}
        <fieldset disabled className="space-y-2"><legend className="mb-2 font-medium">{t("core.setupCommands")} — {t("core.awaitingCore")}</legend>{commands.map((value, index) => <div key={index} className="space-y-2 rounded-md border border-line p-2">
          <Textarea aria-label={t("core.command", { number: index + 1 })} value={value.command} required onChange={event => setCommands(commands.map((row, i) => i === index ? { ...row, command: event.target.value } : row))} />
          <Input aria-label={t("core.workspaceDirectory")} placeholder="/workspace" value={value.cwd} pattern="/.*" onChange={event => setCommands(commands.map((row, i) => i === index ? { ...row, cwd: event.target.value } : row))} />
          <Button type="button" variant="ghost" onClick={() => setCommands(commands.filter((_, i) => i !== index))}>{t("core.remove")}</Button>
        </div>)}<Button type="button" variant="outline" onClick={() => setCommands([...commands, { command: "", cwd: "" }])}>{t("core.addCommand")}</Button></fieldset>
        <fieldset disabled className="space-y-2"><legend className="mb-2 font-medium">{t("core.variables")} — {t("core.awaitingCore")}</legend>{variables.map((value, index) => <div key={index} className="flex flex-wrap gap-2">
          <Input className="min-w-0 flex-1 basis-32" aria-label={t("core.variableKey", { number: index + 1 })} value={value.key} required onChange={event => setVariables(variables.map((row, i) => i === index ? { ...row, key: event.target.value } : row))} />
          <Input className="min-w-0 flex-1 basis-32" type="password" autoComplete="off" aria-label={t("core.variableValue", { number: index + 1 })} value={value.value} onChange={event => setVariables(variables.map((row, i) => i === index ? { ...row, value: event.target.value } : row))} />
          <Button type="button" variant="ghost" onClick={() => setVariables(variables.filter((_, i) => i !== index))}>{t("core.remove")}</Button>
        </div>)}<Button type="button" variant="outline" onClick={() => setVariables([...variables, { key: "", value: "" }])}>{t("core.addVariable")}</Button></fieldset>
        <details><summary className="cursor-pointer text-sm font-medium">{t("core.advanced")}</summary><p className="my-2 text-xs text-fg-muted">{t("core.templateAdvancedHint")}</p><Textarea disabled aria-label={t("core.advanced")} value={advanced} onChange={event => setAdvanced(event.target.value)} placeholder="{}" spellCheck={false} /></details>
      </fieldset>
      <p className="text-xs text-fg-muted">{t("core.supportHint")}</p>
      {error && <p role="alert" className="text-sm text-fg-muted">{error}</p>}
      <DialogFooter><Button type="button" variant="outline" onClick={() => { void queryClient.invalidateQueries({ queryKey: coreTemplateKey(workspaceID) }); onClose() }} disabled={save.isPending}>{t("core.cancel")}</Button><Button type="submit" disabled={save.isPending || uncertain}>{t(save.isPending ? "core.saving" : "core.save")}</Button></DialogFooter>
    </form>
  </DialogContent></Dialog>
}
