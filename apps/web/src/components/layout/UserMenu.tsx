import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { KeyRound, LogOut } from "lucide-react"
import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { useAuth } from "../../lib/auth-context"
import { navigateProfileCredentials } from "../../lib/admin-router"
import { useMyWorkspaces } from "../../lib/api-workspaces"
import { useWorkspaceId } from "../../lib/workspace"
import { cn } from "../../lib/utils"

function initials(name: string, email: string): string {
  const source = name.trim() || email.trim()
  if (!source) return "?"
  const parts = source.split(/[\s._-]+/).filter(Boolean)
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase()
  return source.slice(0, 2).toUpperCase()
}

const menuItemClass = cn(
  "flex cursor-pointer items-center gap-2 rounded px-2 py-1.5 text-sm text-fg outline-none",
  "data-[highlighted]:app-pressed",
)

/**
 * Account row pinned to the bottom of the sidebar: 24px initials tile,
 * name, workspace role. Opens the account menu (credentials, sign out).
 */
export function UserMenu() {
  const { t } = useTranslation("common")
  const { t: ta } = useTranslation("admin")
  const { user, logout } = useAuth()
  const wsId = useWorkspaceId()
  const workspacesQuery = useMyWorkspaces()
  const role = useMemo(
    () => workspacesQuery.data?.workspaces.find((w) => w.id === wsId)?.role,
    [workspacesQuery.data?.workspaces, wsId],
  )

  if (!user) {
    return <div className="app-tile h-6 w-6 rounded" aria-hidden />
  }

  const displayName = user.name || user.email

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md px-1 py-1 text-left hover:app-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 data-[state=open]:app-pressed"
        >
          <span className="app-tile grid h-6 w-6 shrink-0 place-items-center overflow-hidden rounded text-xs font-medium text-fg">
            {user.avatar_url ? (
              <img src={user.avatar_url} alt="" className="h-full w-full object-cover" />
            ) : (
              initials(user.name, user.email)
            )}
          </span>
          <span className="min-w-0 flex-1 leading-tight">
            <span className="block truncate text-sm font-medium text-fg">{displayName}</span>
            {role && (
              <span className="block truncate text-xs text-fg-muted">
                {ta(`members.role.${role}` as never)}
              </span>
            )}
          </span>
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="start"
          side="top"
          sideOffset={6}
          className="app-shadow-floating z-50 min-w-[220px] overflow-hidden rounded-lg border border-line bg-surface p-1 animate-pop-in data-[state=closed]:animate-pop-out"
        >
          <DropdownMenu.Label className="px-2 py-1.5">
            <div className="truncate text-sm font-medium text-fg">{displayName}</div>
            <div className="truncate text-xs text-fg-muted">{user.email}</div>
          </DropdownMenu.Label>
          <DropdownMenu.Separator className="my-1 h-px bg-line" />
          <DropdownMenu.Item onSelect={() => navigateProfileCredentials()} className={menuItemClass}>
            <KeyRound className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
            <span>{t("userMenu.myCredentials")}</span>
          </DropdownMenu.Item>
          <DropdownMenu.Separator className="my-1 h-px bg-line" />
          <DropdownMenu.Item onSelect={() => void logout()} className={menuItemClass}>
            <LogOut className="h-3.5 w-3.5 text-fg-muted" strokeWidth={1.5} />
            <span>{t("userMenu.signOut")}</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
