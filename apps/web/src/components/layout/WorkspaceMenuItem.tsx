import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Archive, Check, Pencil } from "lucide-react"
import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import type { UserWorkspace } from "../../lib/api-types"
import { cn } from "../../lib/utils"
import { setWorkspaceId } from "../../lib/workspace"

export function WorkspaceMenuItem({ workspace, active, onRename, onArchive }: {
  workspace: UserWorkspace
  active: boolean
  onRename: () => void
  onArchive: () => void
}) {
  const { t } = useTranslation("common")
  const canManage = workspace.role === "owner" || workspace.role === "admin"
  return (
    <div className="group/row grid grid-cols-[minmax(0,1fr)_1.75rem_1.75rem] items-center gap-1">
      <DropdownMenu.Item
        onSelect={() => { if (!active) setWorkspaceId(workspace.id) }}
        aria-current={active ? "true" : undefined}
        title={`${workspace.name}\n${workspace.slug}`}
        className={cn(
          "flex min-w-0 cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none data-[highlighted]:bg-surface-muted",
          active && "font-medium text-fg",
        )}
      >
        <span className="min-w-0 flex-1 break-words">{workspace.name}</span>
        <span className="w-14 shrink-0 text-right text-xs font-medium text-fg-faint">{workspace.role}</span>
        <span className="h-3.5 w-3.5 shrink-0" aria-hidden="true">
          {active && <Check className="h-3.5 w-3.5 text-fg-muted" strokeWidth={2} />}
        </span>
      </DropdownMenu.Item>
      {canManage && <>
        <RowAction title={t("workspaceCrud.workspace.renameTitle")} onSelect={onRename} icon={<Pencil className="h-3.5 w-3.5" strokeWidth={1.75} />} />
        <RowAction title={t("workspaceCrud.workspace.archiveTitle")} onSelect={onArchive} icon={<Archive className="h-3.5 w-3.5" strokeWidth={1.75} />} danger />
      </>}
    </div>
  )
}

function RowAction({ title, icon, onSelect, danger }: {
  title: string
  icon: ReactNode
  onSelect: () => void
  danger?: boolean
}) {
  return (
    <DropdownMenu.Item
      onSelect={(e) => { e.preventDefault(); onSelect() }}
      title={title}
      aria-label={title}
      className={cn(
        "invisible flex h-7 w-7 cursor-pointer items-center justify-center rounded outline-none text-fg-faint hover:text-fg-muted data-[highlighted]:text-fg-muted group-hover/row:visible group-focus-within/row:visible",
        danger && "hover:text-danger data-[highlighted]:text-danger",
      )}
    >
      {icon}
    </DropdownMenu.Item>
  )
}
