import { useTranslation } from "react-i18next"
import { Select, SelectOption } from "../../../components/ui/select"
import type { useAgentCloneCredentials } from "./useAgentCloneCredentials"

type Choice = { pinningMode: "latest" | "pinned"; versionID: string; pinnedVersion?: string }

export function AgentCloneVersionPicker({ label, row, choice, onChange }: {
  label: string
  row: ReturnType<typeof useAgentCloneCredentials>["rows"][number] | undefined
  choice: Choice | undefined
  onChange: (choice: Choice) => void
}) {
  const { t } = useTranslation("admin")
  return <Select aria-label={label} value={choice?.pinningMode === "pinned" ? choice.versionID : "__latest__"}
    disabled={!row?.ready} wrapperClassName="ml-1 w-auto shrink-0" className="w-auto font-mono text-xs"
    onClick={(event) => event.stopPropagation()}
    onValueChange={(value) => onChange(value === "__latest__"
      ? { pinningMode: "latest", versionID: row?.latestVersionID ?? "" }
      : { pinningMode: "pinned", versionID: value, pinnedVersion: row?.versions.find((version) => version.id === value)?.version })}>
    <SelectOption value="__latest__">{t("agents.form.versionPicker.latest")}{row?.latestVersion ? ` (v${row.latestVersion})` : ""}</SelectOption>
    {choice?.pinningMode === "pinned" && !row?.versions.some((version) => version.id === choice.versionID) &&
      <SelectOption value={choice.versionID}>v{choice.pinnedVersion ?? "?"}</SelectOption>}
    {row?.versions.map((version) => <SelectOption key={version.id} value={version.id}>v{version.version}</SelectOption>)}
  </Select>
}
