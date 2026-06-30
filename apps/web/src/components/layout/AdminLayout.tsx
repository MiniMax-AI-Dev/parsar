import type { ReactNode } from "react"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { cn } from "../../lib/utils"
import { useAdminView, type AdminView } from "../../lib/admin-router"
import {
  MessageSquare, Play, Inbox,
  Bot, Wrench, Database, Plug,
  Users, Settings,
  ChevronDown,
  type LucideIcon,
} from "lucide-react"
import { WorkspaceSwitcher } from "./WorkspaceSwitcher"
import { UserMenu } from "./UserMenu"
import { useWorkspaceProjects } from "../../lib/api-workspaces"
import {
  setProjectId,
  useProjectId,
  useWorkspaceId,
} from "../../lib/workspace"

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
      { id: "approvals", itemKey: "approvals", icon: Inbox },
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
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({})
  const toggle = (key: string) =>
    setCollapsed((prev) => ({ ...prev, [key]: !prev[key] }))

  // Self-heal stale pid when the bound workspace doesn't own it.
  // setWorkspaceId clears pid on dropdown/URL changes, but a pid from
  // an earlier session can survive a page reload; without this, project-
  // scoped hooks would surface another workspace's data.
  const wsId = useWorkspaceId()
  const pid = useProjectId()
  const projectsQ = useWorkspaceProjects(wsId)
  useEffect(() => {
    if (!wsId) return
    if (projectsQ.isLoading || projectsQ.isError || !projectsQ.data) return

    if (!pid) {
      if (projectsQ.data.projects.length === 1) {
        setProjectId(projectsQ.data.projects[0].id)
      }
      return
    }

    const owned = projectsQ.data.projects.some((p) => p.id === pid)
    if (!owned) setProjectId(null)
  }, [wsId, pid, projectsQ.isLoading, projectsQ.isError, projectsQ.data])

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-surface-subtle/60 text-fg antialiased">
      <header className="flex h-14 shrink-0 items-center gap-3 border-b border-line/70 bg-surface px-4">
        <div className="flex items-center gap-2">
          <img
            src="/favicon.png"
            alt=""
            className="h-7 w-7"
          />
          <span className="text-sm font-semibold tracking-display">{t("appName")}</span>
        </div>

        <WorkspaceSwitcher />

        <div className="ml-auto flex items-center gap-2">
          <UserMenu />
        </div>
      </header>

      <div className="flex flex-1 overflow-hidden">
        {!hideSidebar && <aside className="flex w-60 shrink-0 flex-col gap-4 overflow-y-auto border-r border-line/70 bg-surface px-2.5 py-3">
          {menuGroups.map((group, idx) => {
            const isCollapsed = !!collapsed[group.groupKey]
            return (
              <nav
                key={group.groupKey}
                className={cn(
                  "flex flex-col gap-px",
                  idx === 0 ? "mt-0" : "mt-1.5"
                )}
              >
                <button
                  type="button"
                  onClick={() => toggle(group.groupKey)}
                  className="group/header mb-0.5 flex h-5 w-full items-center gap-1 rounded px-2 text-sm font-normal text-fg-subtle transition-colors hover:text-fg-muted"
                >
                  <span>
                    {t(`nav.${group.groupKey}` as never)}
                  </span>
                  <ChevronDown
                    className={cn(
                      "h-3 w-3 text-fg-faint transition-transform group-hover/header:text-fg-subtle",
                      isCollapsed && "-rotate-90"
                    )}
                    strokeWidth={2}
                  />
                </button>
                {!isCollapsed &&
                  group.items.map((item) => {
                    const Icon = item.icon
                    const isActive = activeMenu === item.id
                    return (
                      <button
                        key={item.id}
                        type="button"
                        onClick={() => navigate(item.id)}
                        className={cn(
                          "group flex h-8 w-full items-center gap-2.5 rounded-md px-2 text-base transition-colors",
                          isActive
                            ? "bg-surface-muted font-medium text-fg"
                            : "font-normal text-fg-muted hover:bg-surface-muted/60 hover:text-fg"
                        )}
                      >
                        <Icon
                          className={cn(
                            "h-4 w-4 shrink-0",
                            isActive
                              ? "text-fg-muted"
                              : "text-fg-faint group-hover:text-fg-muted"
                          )}
                          strokeWidth={1.75}
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
                    )
                  })}
              </nav>
            )
          })}
        </aside>}

        <main className="flex-1 overflow-y-auto">
          {fullBleed ? (
            children
          ) : (
            <div className={cn("mx-auto max-w-screen-2xl px-8 py-8", contentClassName)}>
              {children}
            </div>
          )}
        </main>
      </div>
    </div>
  )
}
