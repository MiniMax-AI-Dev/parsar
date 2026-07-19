import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useAdminView, type AdminView } from "../../lib/admin-router"
import {
  MessageSquare,
  Play,
  CalendarClock,
  Bot,
  Wrench,
  Database,
  Plug,
  Users,
  Settings,
  type LucideIcon,
} from "lucide-react"
import { WorkspaceSwitcher } from "./WorkspaceSwitcher"
import { ThemeMenu } from "./ThemeMenu"
import { UserMenu } from "./UserMenu"
import { useTheme } from "../../lib/theme"

interface AdminLayoutProps {
  children: ReactNode
  activeMenu?: string
  fullBleed?: boolean
  hideSidebar?: boolean
  contentClassName?: string
}

interface MenuItem {
  id: AdminView
  /** key under nav.items.* — kept English to lock product semantics */
  itemKey: string
  icon: LucideIcon
  badge?: number | string
  hint?: "p1Hint"
}

interface MenuGroup {
  /** key under nav.*Group */
  groupKey: string
  items: MenuItem[]
}

const menuGroups: MenuGroup[] = [
  {
    groupKey: "collaborationGroup",
    items: [
      { id: "conversations", itemKey: "conversations", icon: MessageSquare },
      { id: "runs", itemKey: "runs", icon: Play },
      { id: "scheduled", itemKey: "scheduled", icon: CalendarClock },
    ],
  },
  {
    groupKey: "agentGroup",
    items: [
      { id: "agents", itemKey: "agents", icon: Bot },
      { id: "capabilities", itemKey: "capabilities", icon: Wrench },
      { id: "models", itemKey: "models", icon: Database },
      { id: "connections", itemKey: "connections", icon: Plug },
    ],
  },
  {
    groupKey: "teamGroup",
    items: [
      { id: "members", itemKey: "members", icon: Users },
      { id: "settings", itemKey: "settings", icon: Settings },
    ],
  },
]

export function AdminLayout({
  children,
  activeMenu = "agents",
  fullBleed = false,
  hideSidebar = false,
  contentClassName,
}: AdminLayoutProps) {
  const { t } = useTranslation("common")
  const { navigate } = useAdminView()
  const { resolvedTheme } = useTheme()

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-surface-subtle text-fg antialiased">
      <a
        href="#main-content"
        className="fixed left-4 top-3 z-50 -translate-y-20 rounded-lg bg-surface-emphasis px-3 py-2 text-sm font-medium text-fg-on-emphasis shadow-lg transition-transform focus:translate-y-0"
      >
        Skip to main content
      </a>
      <header className="flex h-16 shrink-0 items-center gap-4 border-b border-line/70 bg-surface/95 px-5 shadow-sm backdrop-blur">
        <div className="flex items-center gap-2.5" translate="no">
          <img
            src={resolvedTheme === "dark" ? "/parsar-logo-dark.png" : "/parsar-logo-light.png"}
            alt="Parsar"
            className="h-10 w-auto"
          />
        </div>

        <WorkspaceSwitcher />

        <div className="ml-auto flex items-center gap-2">
          <ThemeMenu />
          <UserMenu />
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {!hideSidebar && (
          <aside className="flex w-60 shrink-0 flex-col overflow-y-auto border-r border-line/70 bg-surface/80 px-3 py-4 backdrop-blur">
            {menuGroups.map((group, idx) => (
              <nav key={group.groupKey} className={cn("flex flex-col", idx > 0 && "mt-4")}>
                <span className="mb-1.5 px-2 text-xs font-semibold uppercase tracking-wide text-fg-faint">
                  {t(`nav.${group.groupKey}` as never)}
                </span>
                <ul className="flex flex-col gap-0.5">
                  {group.items.map((item) => {
                    const Icon = item.icon
                    const isActive = activeMenu === item.id
                    return (
                      <li key={item.id}>
                        <button
                          type="button"
                          onClick={() => navigate(item.id)}
                          className={cn(
                            "group relative flex h-9 w-full items-center gap-2.5 rounded-lg px-2.5 text-sm transition-[color,background-color,box-shadow] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-info/30",
                            isActive
                              ? "bg-surface font-semibold text-fg shadow-sm ring-1 ring-line-muted"
                              : "font-normal text-fg-muted hover:bg-surface-muted hover:text-fg",
                          )}
                          aria-current={isActive ? "page" : undefined}
                        >
                          <Icon
                            className={cn(
                              "h-4 w-4 shrink-0",
                              isActive
                                ? "text-fg-muted"
                                : "text-fg-faint group-hover:text-fg-muted",
                            )}
                            strokeWidth={1.75}
                            aria-hidden="true"
                          />
                          <span className="truncate">
                            {t(`nav.items.${item.itemKey}` as never)}
                          </span>
                          {item.badge !== undefined && (
                            <span className="ml-auto inline-flex h-[18px] min-w-[18px] items-center justify-center rounded-full bg-warning-subtle px-1.5 text-xs font-medium text-warning">
                              {item.badge}
                            </span>
                          )}
                          {item.hint && (
                            <span className="ml-auto text-xs uppercase tracking-wider text-fg-faint">
                              {t(`nav.${item.hint}` as never)}
                            </span>
                          )}
                        </button>
                      </li>
                    )
                  })}
                </ul>
              </nav>
            ))}
          </aside>
        )}

        <main id="main-content" className="relative flex-1 overflow-y-auto" tabIndex={-1}>
          {fullBleed ? (
            children
          ) : (
            <div className={cn("relative mx-auto max-w-6xl px-10 py-9", contentClassName)}>
              {children}
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
