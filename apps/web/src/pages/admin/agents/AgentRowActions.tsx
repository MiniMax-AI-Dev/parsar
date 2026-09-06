import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Copy, MessageSquare, MoreHorizontal, Pencil, Trash2, type LucideIcon } from "lucide-react"
import { useTranslation } from "react-i18next"

import { ActionIconButton, RowActions } from "../../../components/ui/action-button"
import { Button } from "../../../components/ui/button"
import type { Agent } from "../../../lib/api-types"

export function AgentRowActions({
  agent,
  chatPending,
  deletePending,
  onChat,
  onEdit,
  onClone,
  onDelete,
}: {
  agent: Agent
  chatPending: boolean
  deletePending: boolean
  onChat: () => void
  onEdit: () => void
  onClone: () => void
  onDelete: () => void
}) {
  const { t } = useTranslation("admin")
  const enabled = agent.status === "active"

  return (
    <RowActions>
      <ActionIconButton
        icon={MessageSquare}
        label={t("agents.actions.chat")}
        busy={chatPending}
        disabled={!enabled}
        onClick={onChat}
      />
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("agents.actions.more")}
            className="data-[state=open]:app-pressed data-[state=open]:text-fg"
            onClick={(event) => event.stopPropagation()}
            onKeyDown={(event) => event.stopPropagation()}
          >
            <MoreHorizontal strokeWidth={1.5} aria-hidden="true" />
          </Button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="end"
            sideOffset={4}
            onClick={(event) => event.stopPropagation()}
            className="app-shadow-floating z-50 min-w-[160px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
          >
            <MenuItem icon={Pencil} label={t("agents.actions.edit")} onSelect={onEdit} />
            <MenuItem icon={Copy} label={t("agents.actions.clone")} onSelect={onClone} />
            <DropdownMenu.Separator className="my-1 h-px bg-line" />
            <MenuItem icon={Trash2} label={t("agents.actions.delete")} disabled={deletePending} onSelect={onDelete} />
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </RowActions>
  )
}

function MenuItem({
  icon: Icon,
  label,
  disabled,
  onSelect,
}: {
  icon: LucideIcon
  label: string
  disabled?: boolean
  onSelect: () => void
}) {
  return (
    <DropdownMenu.Item
      disabled={disabled}
      onSelect={onSelect}
      className="flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none data-[highlighted]:app-pressed data-[disabled]:pointer-events-none data-[disabled]:opacity-50"
    >
      <Icon className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} aria-hidden="true" />
      <span>{label}</span>
    </DropdownMenu.Item>
  )
}
