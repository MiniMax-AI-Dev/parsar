import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { Copy, Loader2, MessageSquare, MoreHorizontal, Pencil, Trash2 } from "lucide-react"
import { useTranslation } from "react-i18next"

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
    <div
      className="flex min-h-9 items-center justify-end gap-2"
      onClick={(event) => event.stopPropagation()}
    >
      <Button variant="outline" size="sm" disabled={!enabled || chatPending} onClick={onChat}>
        {chatPending ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" strokeWidth={1.8} />
        ) : (
          <MessageSquare className="h-3.5 w-3.5" strokeWidth={1.8} />
        )}
        {t("agents.actions.chat")}
      </Button>
      <DropdownMenu.Root>
        <DropdownMenu.Trigger asChild>
          <button
            type="button"
            aria-label={t("agents.actions.more")}
            className="inline-flex h-9 w-9 items-center justify-center rounded-md text-fg-muted transition-colors hover:bg-surface-muted hover:text-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong data-[state=open]:bg-surface-muted"
          >
            <MoreHorizontal className="h-4 w-4" strokeWidth={1.8} />
          </button>
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content
            align="end"
            sideOffset={6}
            className="z-50 min-w-[180px] overflow-hidden rounded-md border border-line bg-surface p-1 text-sm text-fg-muted shadow-lg"
          >
            <MenuItem icon={Pencil} label={t("agents.actions.edit")} onSelect={onEdit} />
            <MenuItem icon={Copy} label={t("agents.actions.clone")} onSelect={onClone} />
            <DropdownMenu.Separator className="my-1 h-px bg-line" />
            <MenuItem
              icon={Trash2}
              label={t("agents.actions.delete")}
              tone="danger"
              disabled={deletePending}
              onSelect={onDelete}
            />
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu.Root>
    </div>
  )
}

function MenuItem({
  icon: Icon,
  label,
  tone = "default",
  disabled,
  onSelect,
}: {
  icon: typeof Pencil
  label: string
  tone?: "default" | "danger" | "success"
  disabled?: boolean
  onSelect: () => void
}) {
  const toneClass =
    tone === "danger"
      ? "text-danger data-[highlighted]:bg-danger-subtle data-[highlighted]:text-danger-emphasis"
      : tone === "success"
        ? "text-success data-[highlighted]:bg-success-subtle data-[highlighted]:text-success-emphasis"
        : "text-fg-muted data-[highlighted]:bg-surface-muted data-[highlighted]:text-fg"

  return (
    <DropdownMenu.Item
      disabled={disabled}
      onSelect={onSelect}
      className={`flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none data-[disabled]:pointer-events-none data-[disabled]:opacity-45 ${toneClass}`}
    >
      <Icon className="h-3.5 w-3.5" strokeWidth={1.75} />
      <span>{label}</span>
    </DropdownMenu.Item>
  )
}
