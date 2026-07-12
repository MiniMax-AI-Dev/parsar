import * as DropdownMenu from "@radix-ui/react-dropdown-menu"
import { KeyRound, LogOut } from "lucide-react"
import { useTranslation } from "react-i18next"
import { useAuth } from "../../lib/auth-context"
import { navigateProfileCredentials } from "../../lib/admin-router"
import { cn } from "../../lib/utils"

function initials(name: string, email: string): string {
  const source = name.trim() || email.trim()
  if (!source) return "?"
  return source.slice(0, 1).toUpperCase()
}

export function UserMenu() {
  const { t } = useTranslation("common")
  const { user, logout } = useAuth()

  if (!user) {
    return <div className="h-8 w-8 rounded-full bg-surface-muted" aria-hidden />
  }

  return (
    <DropdownMenu.Root>
      <DropdownMenu.Trigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1.5 rounded-full px-1 py-0.5 hover:bg-surface-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-line-strong data-[state=open]:bg-surface-muted"
        >
          <span className="grid h-8 w-8 place-items-center overflow-hidden rounded-full bg-surface-muted text-sm font-semibold text-fg-muted">
            {user.avatar_url ? (
              <img src={user.avatar_url} alt="" className="h-full w-full object-cover" />
            ) : (
              initials(user.name, user.email)
            )}
          </span>
        </button>
      </DropdownMenu.Trigger>

      <DropdownMenu.Portal>
        <DropdownMenu.Content
          align="end"
          sideOffset={6}
          className="z-50 min-w-[220px] overflow-hidden rounded-md border border-line bg-surface p-1 text-sm text-fg-muted shadow-lg data-[state=open]:animate-in data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0"
        >
          <DropdownMenu.Label className="px-2 py-1.5">
            <div className="truncate text-sm font-medium text-fg">{user.name || user.email}</div>
            <div className="truncate text-sm text-fg-faint">{user.email}</div>
          </DropdownMenu.Label>
          <DropdownMenu.Separator className="my-1 h-px bg-surface-muted" />
          <DropdownMenu.Item
            onSelect={() => navigateProfileCredentials()}
            className={cn(
              "flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none text-fg-muted",
              "data-[highlighted]:bg-surface-muted data-[highlighted]:text-fg"
            )}
          >
            <KeyRound className="h-3.5 w-3.5" strokeWidth={1.75} />
            <span>{t("userMenu.myCredentials")}</span>
          </DropdownMenu.Item>
          <DropdownMenu.Separator className="my-1 h-px bg-surface-muted" />
          <DropdownMenu.Item
            onSelect={() => void logout()}
            className={cn(
              "flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 outline-none text-fg-muted",
              "data-[highlighted]:bg-surface-muted data-[highlighted]:text-fg"
            )}
          >
            <LogOut className="h-3.5 w-3.5" strokeWidth={1.75} />
            <span>{t("userMenu.signOut")}</span>
          </DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu.Root>
  )
}
