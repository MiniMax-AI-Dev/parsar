import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useAdminView, type AdminView } from "../../lib/admin-router"
import {
  MessageSquare,
  Inbox,
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
import { ListSlot } from "../plugin/SlotRenderer"
import { ResizeHandle } from "../ui/resize-handle"
import { PageTransition, type PageLevel } from "../ui/page-transition"
import { LayoutPrompt } from "./LayoutPrompt"
import { useResizableWidth } from "../../lib/layout-width"

interface AdminLayoutProps {
  children: ReactNode
  activeMenu?: string
  /** Page owns the whole main column (ledger pages, conversations). */
  fullBleed?: boolean
  hideSidebar?: boolean
  contentClassName?: string
  /**
   * Entrance animation level. Defaults to "detail" when the route carries
   * an entity id on a view that opens a separate detail page, "page"
   * otherwise (views whose id only selects a rail stay level one).
   */
  level?: PageLevel
}

/** Views where `?id=` selects a rail on the same page instead of a detail page. */
const RAIL_VIEWS = new Set<string>(["runs", "approvals", "conversations", "connections"])

interface MenuItem {
  id: AdminView
  /** key under nav.items.* — kept English to lock product semantics */
  itemKey: string
  icon: LucideIcon
  badge?: number | string
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
      { id: "approvals", itemKey: "approvals", icon: Inbox },
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

/**
 * Console shell: a 232px panel-toned sidebar (workspace row, three nav
 * groups, account row pinned to the bottom) and a main column. There is
 * no top bar; each page renders its own 48px <PageHeader />.
 */
export function AdminLayout({
  children,
  activeMenu = "agents",
  fullBleed = false,
  hideSidebar = false,
  contentClassName,
  level: levelProp,
}: AdminLayoutProps) {
  const { t } = useTranslation("common")
  const { navigate, view, entityId } = useAdminView()
  const level: PageLevel = levelProp ?? (entityId && view && !RAIL_VIEWS.has(view) ? "detail" : "page")
  const sidebar = useResizableWidth({ storageKey: "sidebar", defaultWidth: 232, min: 200, max: 360, edge: "right" })

  return (
    <div className="flex h-screen overflow-hidden bg-surface text-fg">
      <a
        href="#main-content"
        className="fixed left-4 top-3 z-50 -translate-y-20 rounded-md bg-surface-emphasis px-3 py-2 text-sm font-medium text-fg-on-emphasis app-shadow-floating transition-transform focus:translate-y-0"
      >
        Skip to main content
      </a>

      {!hideSidebar && (
        <div
          className={cn("relative flex shrink-0", sidebar.restoring && "transition-[width] duration-[420ms] ease-spring")}
          style={{ width: sidebar.width }}
        >
        <aside className="app-sidebar flex w-full flex-col overflow-y-auto border-r border-line bg-surface-subtle p-2.5">
          <WorkspaceSwitcher />

          {menuGroups.map((group) => (
            <nav key={group.groupKey} aria-label={t(`nav.${group.groupKey}` as never)}>
              <div className="px-2 pb-1 pt-3.5 text-xs text-fg-muted">
                {t(`nav.${group.groupKey}` as never)}
              </div>
              <ul className="flex flex-col gap-px">
                {group.items.map((item) => {
                  const Icon = item.icon
                  const isActive = activeMenu === item.id
                  return (
                    <li key={item.id}>
                      <button
                        type="button"
                        onClick={() => navigate(item.id)}
                        aria-current={isActive ? "page" : undefined}
                        className={cn(
                          "flex h-[30px] w-full items-center gap-2 rounded-md px-2 text-left text-base transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40",
                          isActive
                            ? "app-pressed font-medium text-fg"
                            : "text-inherit hover:app-hover",
                        )}
                      >
                        <Icon
                          className={cn("h-4 w-4 shrink-0", isActive ? "text-fg" : "text-fg-muted")}
                          strokeWidth={1.5}
                          aria-hidden="true"
                        />
                        <span className="min-w-0 flex-1 truncate">
                          {t(`nav.items.${item.itemKey}` as never)}
                        </span>
                        {item.badge !== undefined && (
                          <span className="app-tile rounded-full px-1.5 text-xs tabular-nums text-fg-muted">
                            {item.badge}
                          </span>
                        )}
                      </button>
                    </li>
                  )
                })}
              </ul>
            </nav>
          ))}
          <ListSlot slotId="layout.nav.bottom" />

          <div className="mt-auto flex items-center gap-2 border-t border-line pl-1.5 pr-0.5 pt-2.5">
            <UserMenu />
            <ListSlot slotId="layout.header.actions" />
            <ThemeMenu />
          </div>
        </aside>
        <ResizeHandle edge="right" dragging={sidebar.dragging} label={t("layout.adjusted")} {...sidebar.handleProps} />
        <LayoutPrompt open={sidebar.dirty} onSave={sidebar.save} onTemporary={sidebar.keepTemporary} onRestore={sidebar.restore} />
        </div>
      )}

      <main
        id="main-content"
        className="relative flex min-w-0 flex-1 flex-col overflow-hidden"
        tabIndex={-1}
      >
        <PageTransition viewKey={level === "detail" ? `${activeMenu}:${entityId ?? ""}` : activeMenu} level={level}>
          {fullBleed ? (
            children
          ) : (
            <div className={cn("flex-1 overflow-y-auto px-6 pb-10", contentClassName)}>
              {children}
            </div>
          )}
        </PageTransition>
      </main>
    </div>
  )
}
